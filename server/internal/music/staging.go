package music

import (
	"context"
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

	"github.com/google/uuid"
	"musicserver/internal/activity"
)

const uploadStageTTL = 30 * time.Minute

type uploadAssets struct {
	Lyrics      []byte
	Cover       []byte
	CoverSource string
}

// Keep edits separate so an unchanged preview retains its metadata sources.
type stagedRecord struct {
	GenreValues []string          `json:"genre_values"`
	Track       Track             `json:"track"`
	Overrides   map[string]string `json:"overrides"`
	Cover       []byte            `json:"cover,omitempty"`
	Sidecar     string            `json:"sidecar,omitempty"`
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

func (s *Store) StageAudio(name string, reader io.Reader, fields map[string]string, options ...MutationOptions) (UploadPreview, error) {
	return s.stageAudioWithAssets(name, reader, fields, uploadAssets{}, options...)
}

func (s *Store) stageAudioWithAssets(name string, reader io.Reader, fields map[string]string, assets uploadAssets, options ...MutationOptions) (UploadPreview, error) {
	return s.stageAudioWithAssetsContext(context.Background(), name, reader, fields, assets, options...)
}

type uploadActivityKey struct{}

func (s *Store) stageAudioWithAssetsContext(ctx context.Context, name string, reader io.Reader, fields map[string]string, assets uploadAssets, options ...MutationOptions) (result UploadPreview, resultErr error) {
	started := time.Now()
	metrics, _ := ctx.Value(uploadActivityKey{}).(activity.Fields)
	if metrics.UserID == "" && len(options) > 0 {
		metrics.UserID = options[0].UserID
	}
	timings := map[string]int64{}
	for key, value := range metrics.Timings {
		timings[key] = value
	}
	defer func() {
		timings["total_ms"] = time.Since(started).Milliseconds() + timings["receive_ms"]
		metrics.Timings = timings
		metrics.DurationMS = timings["total_ms"]
		result.Timings = timings
		code := "upload.ready"
		if resultErr != nil {
			code = "upload.failed"
			metrics.Reason = "UPLOAD_STORAGE"
			switch {
			case errors.Is(resultErr, context.Canceled):
				metrics.Reason = "CLIENT_CANCELED"
			case strings.Contains(resultErr.Error(), "FFPROBE_REQUIRED"):
				metrics.Reason = "FFPROBE_REQUIRED"
			case strings.Contains(resultErr.Error(), "AUDIO_INVALID"):
				metrics.Reason = "AUDIO_INVALID"
			case strings.Contains(resultErr.Error(), "upload limit"):
				metrics.Reason = "UPLOAD_LIMIT"
			}
		}
		_ = s.activity.Emit(code, metrics)
	}()
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
	owner := ""
	if len(options) > 0 {
		owner = options[0].UserID
	}
	if owner == "" {
		if err := s.db.QueryRow("SELECT id FROM users WHERE role='admin' AND enabled=1 ORDER BY created_at,id LIMIT 1").Scan(&owner); err != nil {
			return UploadPreview{}, err
		}
	}
	if err := checkStageUploadActor(s.db, owner, options); err != nil {
		return UploadPreview{}, err
	}
	waitStarted := time.Now()
	select {
	case s.stageSlots <- struct{}{}:
		defer func() { <-s.stageSlots }()
	case <-ctx.Done():
		return UploadPreview{}, ctx.Err()
	}
	s.stageMu.Lock()
	locked := true
	defer func() {
		if locked {
			s.stageMu.Unlock()
		}
	}()
	timings["wait_ms"] = time.Since(waitStarted).Milliseconds()
	if err := s.cleanupUploadStagesLocked(); err != nil {
		return UploadPreview{}, err
	}
	id, err := randomToken(16)
	if err != nil {
		return UploadPreview{}, err
	}
	if resumeID, ok := ctx.Value(resumableStageIDKey{}).(string); ok && validResumeID(resumeID) {
		id = resumeID
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
	if err := s.checkSpace(s.mediaRoot, 0); err != nil {
		return UploadPreview{}, err
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), 1<<20); err != nil {
		return UploadPreview{}, err
	}
	file, err := root.OpenFile(relative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return UploadPreview{}, err
	}
	s.activeStages[relative] = true
	s.stageMu.Unlock()
	locked = false
	defer func() { s.stageMu.Lock(); delete(s.activeStages, relative); s.stageMu.Unlock() }()
	settings := s.Settings()
	max := settings.MaxUploadBytes
	writeStarted := time.Now()
	count, copyErr := io.Copy(storageWriter{s, s.mediaRoot, file}, io.LimitReader(contextReader{ctx, reader}, max+1))
	closeErr := file.Close()
	metrics.Bytes = count
	timings["write_ms"] = time.Since(writeStarted).Milliseconds()
	if copyErr != nil || closeErr != nil || count > max {
		_ = root.Remove(relative)
		if count > max {
			return UploadPreview{}, errors.New("file exceeds configured upload limit")
		}
		return UploadPreview{}, errors.Join(copyErr, closeErr)
	}
	_ = s.activity.Emit("upload.probing", metrics)
	probeStarted := time.Now()
	technical, probeErr := validatedAudioContext(ctx, filepath.Join(s.mediaRoot, filepath.FromSlash(relative)), settings.FFprobePath)
	timings["probe_ms"] = time.Since(probeStarted).Milliseconds()
	if probeErr != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, probeErr
	}
	metadataStarted := time.Now()
	track, metadata, err := s.readTrackWithTechnical(ctx, filepath.Join(s.mediaRoot, filepath.FromSlash(relative)), name, settings, &technical)
	timings["metadata_ms"] = time.Since(metadataStarted).Milliseconds()
	if err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	record := stagedRecord{Track: track, Overrides: map[string]string{}, Cover: metadata.Cover, Sidecar: lyrics, GenreValues: metadata.GenreValues}
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
	if genre, ok := fields["genre"]; ok {
		record.GenreValues = genreImportValues(genre)
	}
	created := time.Now().UTC()
	expires := created.Add(uploadStageTTL)
	if err := checkStageUploadActor(s.db, owner, options); err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	record.Track.OwnerID, record.Track.UploadedBy = owner, owner
	persistStarted := time.Now()
	encoded, err := json.Marshal(record)
	if err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	if err := s.checkSpace(filepath.Dir(s.dataPath), int64(len(encoded))*2+(1<<20)); err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	if err := s.transaction(func(tx *sql.Tx) error {
		if err := checkStageUploadActor(tx, owner, options); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO upload_staging (id,original_name,size,content_hash,temp_path,track_json,created_at,expires_at,user_id) VALUES (?,?,?,?,?,?,?,?,?)`, id, filepath.Base(name), count, track.ContentHash, relative, string(encoded), persistedTime(created), persistedTime(expires), owner)
		return err
	}); err != nil {
		_ = root.Remove(relative)
		return UploadPreview{}, err
	}
	timings["persist_ms"] = time.Since(persistStarted).Milliseconds()
	return s.previewLocked(id, filepath.Base(name), record, created, expires), nil
}

func (s *Store) previewLocked(id, name string, record stagedRecord, created, expires time.Time) UploadPreview {
	preview := UploadPreview{ID: id, OriginalName: name, Size: record.Track.Size, ContentHash: record.Track.ContentHash, Track: record.Track, CreatedAt: created, ExpiresAt: expires, Validation: "audio_stream_verified"}
	_ = s.db.QueryRow("SELECT id FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id LIMIT 1", record.Track.ContentHash).Scan(&preview.DuplicateOf)
	s.uploadPreviewTags(&preview, record)
	return preview
}

func (s *Store) UploadStage(id string) (UploadPreview, bool, error) {
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
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
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
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
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	return s.deleteUploadStageLocked(id)
}

func (s *Store) CommitUploadStage(id string, options ...MutationOptions) (Track, bool, error) {
	return s.commitUploadStageWithMetadata(id, nil, options...)
}

func (s *Store) commitUploadStageWithMetadata(id string, fields map[string]string, options ...MutationOptions) (Track, bool, error) {
	return s.commitUploadStageWithTags(id, fields, nil, options...)
}

func genreImportValues(value string) []string {
	if value == "" {
		return []string{}
	}
	return []string{value}
}

func (s *Store) commitUploadStageWithTags(id string, fields map[string]string, edits *UploadTagEdits, options ...MutationOptions) (Track, bool, error) {
	if err := validMetadataFields(fields); err != nil {
		return Track{}, false, err
	}
	if edits != nil {
		if err := requireTagsSchema(s.db); err != nil {
			return Track{}, false, err
		}
		if err := validateUploadTagEdits(edits); err != nil {
			return Track{}, false, err
		}
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()
	preview, record, relative, err := s.uploadStageLocked(id)
	if err != nil {
		return Track{}, false, err
	}
	var stageOwner string
	if err := s.db.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&stageOwner); err != nil {
		return Track{}, false, err
	}
	if len(options) > 0 && options[0].UserID != "" && options[0].UserID != stageOwner {
		return Track{}, false, errResourceHidden
	}
	if err := checkStageUploadActor(s.db, stageOwner, options); err != nil {
		return Track{}, false, err
	}
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, false, err
	}
	defer root.Close()
	if err := root.MkdirAll("uploads", 0o755); err != nil {
		return Track{}, false, err
	}
	final := "uploads/" + id + strings.ToLower(filepath.Ext(preview.OriginalName))
	if err := s.checkSpace(filepath.Dir(s.dataPath), int64(len(record.Cover)+len(record.Sidecar))*2+(1<<20)); err != nil {
		return Track{}, false, err
	}
	if err := s.checkSpace(s.mediaRoot, preview.Size+int64(len(record.Sidecar)+len(record.Cover))); err != nil {
		return Track{}, false, err
	}
	source, err := root.Open(relative)
	if err != nil {
		return Track{}, false, err
	}
	defer source.Close()
	destination, err := root.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Track{}, false, err
	}
	_, copyErr := io.Copy(storageWriter{s, s.mediaRoot, destination}, source)
	syncErr := destination.Sync()
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
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return Track{}, false, errors.Join(copyErr, syncErr, closeErr)
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
	track := record.Track
	track.Path, track.ID = final, uuid.NewString()
	if info, err := root.Stat(final); err != nil {
		return Track{}, false, err
	} else {
		track.ModifiedAt = info.ModTime().UTC()
	}
	track.Version = 1
	if record.Overrides == nil {
		record.Overrides = map[string]string{}
	}
	for key, value := range fields {
		record.Overrides[key] = value
	}
	applyMetadata(&track, fields)
	if genre, ok := fields["genre"]; ok {
		record.GenreValues = genreImportValues(genre)
	}
	if len(record.Cover) > 0 {
		track.CoverID = hashBytes(record.Cover)
		if track.CoverSource == "manual" {
			record.Overrides["cover_id"] = track.CoverID
			record.Overrides["cover_mime"] = track.CoverMIME
			record.Overrides["cover_source"] = "manual"
		}
	}
	duplicate := false
	publicTagChanged := false
	// Media and SQLite may share a volume; recheck after publishing temporary
	// copies and sidecars, before database assets consume the remaining headroom.
	if err := s.checkSpace(filepath.Dir(s.dataPath), int64(len(record.Cover)+len(record.Sidecar))*2+(1<<20)); err != nil {
		return Track{}, false, err
	}
	transact := s.transaction
	if edits != nil {
		transact = func(run func(*sql.Tx) error) error { return s.tagTransaction(false, run) }
	}
	if err := transact(func(tx *sql.Tx) error {
		if err := checkStageUploadActor(tx, stageOwner, options); err != nil {
			return err
		}
		var existingID string
		err := tx.QueryRow("SELECT id FROM tracks WHERE content_hash=? AND removed=0 AND missing=0 ORDER BY id LIMIT 1", track.ContentHash).Scan(&existingID)
		if err == nil {
			track, err = readTrackFrom(tx, existingID, false)
			if err != nil {
				return err
			}
			duplicate = true
			publicTagChanged, err = applyUploadTagsTx(tx, track.ID, edits, options)
			if err != nil {
				return err
			}
			track, err = readTrackFrom(tx, track.ID, false)
			if err != nil {
				return err
			}
			_, err = tx.Exec("DELETE FROM upload_staging WHERE id=?", id)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var removed int
		if err := tx.QueryRow("SELECT count(*) FROM tracks WHERE content_hash=? AND removed=1", track.ContentHash).Scan(&removed); err != nil {
			return err
		}
		if removed > 0 {
			return errors.New("this audio was removed; contact an administrator to restore it")
		}
		if err := s.checkAccountQuota(tx, stageOwner, track.Size); err != nil {
			return err
		}
		if err := checkStageUploadActor(tx, stageOwner, options); err != nil {
			return err
		}
		if err := saveCover(tx, record.Cover, track.CoverMIME); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT user_id FROM upload_staging WHERE id=?", id).Scan(&track.OwnerID); err != nil {
			return err
		}
		track.UploadedBy = track.OwnerID
		if err := writeTrack(tx, track); err != nil {
			return err
		}
		if err := writeOverrides(tx, track.ID, record.Overrides); err != nil {
			return err
		}
		publicTagChanged, err = applyUploadTagsTx(tx, track.ID, edits, options)
		if err != nil {
			return err
		}
		importChanged, err := syncImportedGenreTags(tx, track.ID, record.GenreValues)
		if err != nil {
			return err
		}
		track, err = readTrackFrom(tx, track.ID, false)
		if err != nil {
			return err
		}
		if importChanged {
			track.Version++
			if err := writeTrack(tx, track); err != nil {
				return err
			}
		}
		if err := recordChange(tx, "track", track.ID, false); err != nil {
			return err
		}
		if err := refreshLibrary(tx); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM upload_staging WHERE id=?", id); err != nil {
			return err
		}
		return auditMutation(tx, "track.upload", track.ID, nil, track, options)
	}); err != nil {
		return Track{}, false, err
	}
	if edits != nil && (!duplicate || publicTagChanged) {
		s.notifyCommunityCommit()
	}
	committed = !duplicate
	_ = source.Close()
	if err := root.Remove(relative); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("remove committed upload staging file %q: %v", relative, err)
	}
	return track, duplicate, nil
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
	s.stageMu.Lock()
	err := s.cleanupUploadStagesLocked()
	s.stageMu.Unlock()
	if err != nil {
		return err
	}
	return s.CleanupResumableUploads()
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
		if !entry.Type().IsRegular() || !generatedStageFilename(entry.Name()) || retained[path] || s.activeStages[path] {
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
