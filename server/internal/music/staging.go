package music

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const uploadStageTTL = 30 * time.Minute

type uploadAssets struct {
	Lyrics      []byte
	Cover       []byte
	CoverSource string
}

// Keep edits separate so an unchanged preview retains its metadata sources.
type stagedRecord struct {
	Track     Track             `json:"track"`
	Overrides map[string]string `json:"overrides"`
	Cover     []byte            `json:"cover,omitempty"`
	Sidecar   string            `json:"sidecar,omitempty"`
}

func validMetadataFields(fields map[string]string) error {
	for key, value := range fields {
		if key != "title" && key != "artist" && key != "album" && key != "genre" && key != "comment" && key != "lyrics" {
			return fmt.Errorf("unsupported metadata field: %s", key)
		}
		if len(value) > maxEmbeddedLyricsBytes {
			return fmt.Errorf("metadata field %s exceeds 2 MiB", key)
		}
	}
	return nil
}

func (s *Store) StageAudio(name string, reader io.Reader, fields map[string]string) (UploadPreview, error) {
	return s.stageAudioWithAssets(name, reader, fields, uploadAssets{})
}

func (s *Store) stageAudioWithAssets(name string, reader io.Reader, fields map[string]string, assets uploadAssets) (UploadPreview, error) {
	ext := strings.ToLower(filepath.Ext(filepath.Base(name)))
	if !audioExtensions[ext] {
		return UploadPreview{}, errors.New("unsupported audio extension")
	}
	if err := validMetadataFields(fields); err != nil {
		return UploadPreview{}, err
	}
	coverSource := assets.CoverSource
	if coverSource == "" {
		coverSource = "manual"
	}
	if coverSource != "manual" && coverSource != "external" {
		return UploadPreview{}, errors.New("cover_source must be manual or external")
	}
	var lyrics string
	if assets.Lyrics != nil {
		var err error
		lyrics, err = decodeLyricsFile(assets.Lyrics)
		if err != nil || strings.TrimSpace(lyrics) == "" {
			return UploadPreview{}, errors.New("lyrics file is empty, invalid or exceeds 2 MiB")
		}
	}
	var cover []byte
	var coverMIME string
	if assets.Cover != nil {
		var ok bool
		cover, coverMIME, ok = validateCover(assets.Cover, "")
		if !ok {
			return UploadPreview{}, errors.New("invalid cover (JPEG, PNG, GIF, WebP; maximum 8 MiB and 20 MP)")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.cleanupUploadStagesLocked(); err != nil {
		return UploadPreview{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return UploadPreview{}, err
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return UploadPreview{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(".staging", 0o700); err != nil {
		return UploadPreview{}, err
	}
	relative := ".staging/" + id + ext
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return UploadPreview{}, err
	}
	max := s.data.Settings.MaxUploadBytes
	count, copyErr := io.Copy(file, io.LimitReader(reader, max+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || count > max {
		_ = root.Remove(relative)
		if count > max {
			return UploadPreview{}, errors.New("file exceeds configured upload limit")
		}
		return UploadPreview{}, errors.Join(copyErr, closeErr)
	}
	track, metadata, err := s.readTrackWithMetadata(filepath.Join(s.mediaRoot, filepath.FromSlash(relative)), name)
	if err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	record := stagedRecord{Track: track, Overrides: map[string]string{}, Cover: metadata.Cover, Sidecar: lyrics}
	if metadata.Title == "" {
		record.Overrides["title"] = track.Title
	}
	if metadata.Artist == "" && track.Artist != "" {
		record.Overrides["artist"] = track.Artist
	}
	if assets.Lyrics != nil {
		record.Track.Lyrics, record.Track.LyricsSource = lyrics, "sidecar"
	}
	if assets.Cover != nil && (coverSource == "manual" || metadata.CoverSource != "embedded") {
		record.Cover = cover
		record.Track.CoverMIME, record.Track.CoverSource = coverMIME, coverSource
	}
	if len(record.Cover) > 0 {
		record.Track.CoverID = hashBytes(record.Cover)
	}
	for key, value := range fields {
		record.Overrides[key] = value
	}
	applyMetadata(&record.Track, fields)
	encoded, err := json.Marshal(record)
	if err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	created := time.Now().UTC()
	expires := created.Add(uploadStageTTL)
	if _, err := s.db.Exec(`INSERT INTO upload_staging (id,original_name,size,content_hash,temp_path,track_json,created_at,expires_at) VALUES (?,?,?,?,?,?,?,?)`, id, filepath.Base(name), count, track.ContentHash, relative, string(encoded), persistedTime(created), persistedTime(expires)); err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	return s.previewLocked(id, filepath.Base(name), record, created, expires), nil
}

func (s *Store) previewLocked(id, name string, record stagedRecord, created, expires time.Time) UploadPreview {
	preview := UploadPreview{ID: id, OriginalName: name, Size: record.Track.Size, ContentHash: record.Track.ContentHash, Track: record.Track, CreatedAt: created, ExpiresAt: expires}
	for _, trackID := range sortedTrackIDs(s.data.Tracks) {
		if preview.ContentHash != "" && s.data.Tracks[trackID].ContentHash == preview.ContentHash {
			preview.DuplicateOf = trackID
			break
		}
	}
	return preview
}

func (s *Store) UploadStage(id string) (UploadPreview, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, _, _, err := s.uploadStageLocked(id)
	if errors.Is(err, os.ErrNotExist) {
		return UploadPreview{}, false, nil
	}
	return preview, err == nil, err
}

func (s *Store) uploadStageLocked(id string) (UploadPreview, stagedRecord, string, error) {
	var record stagedRecord
	var name, path, encoded, created, expires string
	err := s.db.QueryRow(`SELECT original_name,temp_path,track_json,created_at,expires_at FROM upload_staging WHERE id = ?`, id).Scan(&name, &path, &encoded, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return UploadPreview{}, record, "", os.ErrNotExist
	}
	if err != nil {
		return UploadPreview{}, record, "", err
	}
	expiresAt, err := parsePersistedTime(expires)
	if err != nil || expiresAt.IsZero() || time.Now().UTC().After(expiresAt) {
		_ = s.deleteUploadStageLocked(id)
		return UploadPreview{}, record, "", os.ErrNotExist
	}
	if !validStagePath(path) {
		return UploadPreview{}, record, "", errors.New("invalid staging path")
	}
	if err := json.Unmarshal([]byte(encoded), &record); err != nil {
		return UploadPreview{}, record, "", err
	}
	createdAt, _ := parsePersistedTime(created)
	return s.previewLocked(id, name, record, createdAt, expiresAt), record, path, nil
}

func validStagePath(path string) bool {
	return strings.HasPrefix(path, ".staging/") && filepath.Base(path) != "." && filepath.ToSlash(filepath.Clean(path)) == path && !strings.Contains(strings.TrimPrefix(path, ".staging/"), "/")
}

func (s *Store) UploadStageCover(id string) (CoverAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, record, _, err := s.uploadStageLocked(id)
	if err != nil {
		return CoverAsset{}, err
	}
	if len(record.Cover) == 0 {
		return CoverAsset{}, os.ErrNotExist
	}
	return CoverAsset{ID: hashBytes(record.Cover), MIME: record.Track.CoverMIME, Data: record.Cover}, nil
}

func (s *Store) DeleteUploadStage(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteUploadStageLocked(id)
}

func (s *Store) CommitUploadStage(id string) (Track, bool, error) {
	return s.commitUploadStageWithMetadata(id, nil)
}

func (s *Store) commitUploadStageWithMetadata(id string, fields map[string]string) (Track, bool, error) {
	if err := validMetadataFields(fields); err != nil {
		return Track{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	preview, record, relative, err := s.uploadStageLocked(id)
	if err != nil {
		return Track{}, false, err
	}
	if preview.DuplicateOf != "" {
		if err := s.deleteUploadStageLocked(id); err != nil {
			return Track{}, false, err
		}
		return s.data.Tracks[preview.DuplicateOf], true, nil
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, false, err
	}
	defer root.Close()
	if err := root.MkdirAll("uploads", 0o755); err != nil {
		return Track{}, false, err
	}
	final := "uploads/" + id + strings.ToLower(filepath.Ext(preview.OriginalName))
	source, err := root.Open(relative)
	if err != nil {
		return Track{}, false, err
	}
	defer source.Close()
	destination, err := root.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Track{}, false, err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	lyricsPath := strings.TrimSuffix(final, filepath.Ext(final)) + ".lrc"
	coverPath := ""
	committed := false
	lyricsCreated := false
	coverCreated := false
	defer func() {
		if !committed {
			_ = root.Remove(final)
			if lyricsCreated {
				_ = root.Remove(lyricsPath)
			}
			if coverCreated {
				_ = root.Remove(coverPath)
			}
		}
	}()
	if copyErr != nil || closeErr != nil {
		return Track{}, false, errors.Join(copyErr, closeErr)
	}
	if record.Sidecar != "" {
		file, err := root.OpenFile(lyricsPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return Track{}, false, err
		}
		lyricsCreated = true
		_, writeErr := io.WriteString(file, record.Sidecar)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return Track{}, false, err
		}
	}
	if record.Track.CoverSource == "external" && len(record.Cover) > 0 {
		extensions := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}
		extension, ok := extensions[record.Track.CoverMIME]
		if !ok {
			return Track{}, false, errors.New("unsupported external cover MIME type")
		}
		coverPath = strings.TrimSuffix(final, filepath.Ext(final)) + extension
		file, err := root.OpenFile(coverPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return Track{}, false, err
		}
		coverCreated = true
		_, writeErr := file.Write(record.Cover)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return Track{}, false, err
		}
	}
	previous := s.cloneLocked()
	track := record.Track
	track.Path, track.ID = final, hashBytes([]byte(final))
	if record.Overrides == nil {
		record.Overrides = map[string]string{}
	}
	for key, value := range fields {
		record.Overrides[key] = value
	}
	applyMetadata(&track, fields)
	if len(record.Cover) > 0 {
		track.CoverID, err = s.saveCoverLocked(record.Cover, track.CoverMIME, track.CoverSource)
		if err != nil {
			s.data = previous
			return Track{}, false, err
		}
		if track.CoverSource == "manual" {
			record.Overrides["cover_id"] = track.CoverID
			record.Overrides["cover_mime"] = track.CoverMIME
			record.Overrides["cover_source"] = "manual"
		}
	}
	s.data.Tracks[track.ID] = track
	s.data.Overrides[track.ID] = record.Overrides
	playlist := s.data.Playlists["library"]
	playlist.ID = "library"
	if playlist.Name == "" {
		playlist.Name = "All music"
	}
	playlist.TrackIDs = sortedTrackIDs(s.data.Tracks)
	playlist.UpdatedAt = time.Now().UTC()
	s.data.Playlists["library"] = playlist
	s.data.Revision++
	if err := saveSQLiteSnapshot(s.db, s.data, id); err != nil {
		s.data = previous
		return Track{}, false, err
	}
	dropMissingPlaylistGrants(&s.data)
	committed = true
	_ = source.Close()
	if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("remove committed upload staging file %q: %v", relative, err)
	}
	return track, false, nil
}

func (s *Store) deleteUploadStageLocked(id string) error {
	var path string
	err := s.db.QueryRow(`SELECT temp_path FROM upload_staging WHERE id = ?`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if !validStagePath(path) {
		return errors.New("invalid staging path")
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM upload_staging WHERE id = ?`, id)
	return err
}

func (s *Store) CleanupUploadStages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cleanupUploadStagesLocked()
}

func (s *Store) cleanupUploadStagesLocked() error {
	rows, err := s.db.Query(`SELECT id,temp_path,expires_at FROM upload_staging`)
	if err != nil {
		return err
	}
	var expired []string
	retained := map[string]bool{}
	for rows.Next() {
		var id, path, expires string
		if err := rows.Scan(&id, &path, &expires); err != nil {
			_ = rows.Close()
			return err
		}
		retained[path] = true
		when, parseErr := parsePersistedTime(expires)
		if parseErr != nil || when.IsZero() || time.Now().UTC().After(when) {
			expired = append(expired, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, id := range expired {
		if err := s.deleteUploadStageLocked(id); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove expired upload staging record %q: %v", id, err)
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	defer root.Close()
	directory, err := root.Open(".staging")
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(cleanupErrors...)
	}
	if err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return errors.Join(append(cleanupErrors, err)...)
	}
	// Interrupted writes and post-commit delete failures have no database row.
	// The mutex protects live stages while this sweep removes only our files.
	for _, entry := range entries {
		path := ".staging/" + entry.Name()
		if !entry.Type().IsRegular() || !generatedStageFilename(entry.Name()) || retained[path] {
			continue
		}
		if err := root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove orphaned upload staging file %q: %v", path, err)
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func generatedStageFilename(name string) bool {
	ext := filepath.Ext(name)
	if !audioExtensions[ext] {
		return false
	}
	id := strings.TrimSuffix(name, ext)
	if len(id) != 32 {
		return false
	}
	for _, value := range id {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}
