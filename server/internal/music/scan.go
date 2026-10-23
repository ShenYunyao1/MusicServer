package music

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"musicserver/internal/activity"
)

type scanCandidate struct {
	track    Track
	metadata audioMetadata
	stamp    string
}

type contextReader struct {
	context context.Context
	reader  io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.context.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func (s *Store) readTrack(path string) (Track, error) {
	track, _, err := s.readTrackWithMetadata(path, filepath.Base(path))
	return track, err
}
func (s *Store) readTrackWithMetadata(path, name string) (Track, audioMetadata, error) {
	return s.readTrackWithSettings(context.Background(), path, name, s.Settings())
}
func (s *Store) readTrackWithSettings(ctx context.Context, path, name string, settings ServerSettings) (Track, audioMetadata, error) {
	return s.readTrackWithTechnical(ctx, path, name, settings, nil)
}
func (s *Store) readTrackWithTechnical(ctx context.Context, path, name string, settings ServerSettings, validated *audioTechnical) (Track, audioMetadata, error) {
	relative, err := filepath.Rel(s.mediaRoot, path)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	relative = filepath.ToSlash(relative)
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	defer root.Close()
	file, err := root.Open(relative)
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, contextReader{ctx, file}); err != nil {
		return Track{}, audioMetadata{}, err
	}
	metadata, _ := readAudioMetadata(file)
	if settings.ScanExternalCovers {
		metadata = selectCover(root, relative, metadata)
	}
	var technical audioTechnical
	if validated != nil {
		technical = *validated
	} else {
		technical = technicalMetadataContext(ctx, path, settings.FFprobePath)
	}
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	title, artist := base, ""
	if parts := strings.SplitN(base, " - ", 2); len(parts) == 2 {
		title, artist = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	if metadata.Title != "" {
		title = metadata.Title
	}
	if metadata.Artist != "" {
		artist = metadata.Artist
	}
	lyrics, source := metadata.Lyrics, metadata.LyricsSource
	if settings.ScanExternalLyrics {
		if text, origin := readExternalLyrics(root, relative); origin != "" {
			lyrics, source = text, origin
		}
	}
	after, err := file.Stat()
	if err != nil {
		return Track{}, audioMetadata{}, err
	}
	if info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return Track{}, audioMetadata{}, errors.New("file changed during inspection; retry scan")
	}
	return Track{ID: uuid.NewString(), Title: title, Artist: artist, Album: metadata.Album, Genre: metadata.Genre, Comment: metadata.Comment, Lyrics: lyrics, EmbeddedLyrics: metadata.Lyrics != "", LyricsSource: source, CoverID: hashCover(metadata), CoverMIME: metadata.CoverMIME, CoverSource: metadata.CoverSource, DurationMS: technical.DurationMS, BitrateKbps: int64(technical.BitrateKbps), SampleRate: technical.SampleRate, Channels: technical.Channels, BitsPerSample: technical.BitsPerSample, Path: relative, Size: info.Size(), ContentHash: hex.EncodeToString(hasher.Sum(nil)), Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), "."), ModifiedAt: info.ModTime().UTC()}, metadata, nil
}
func hashCover(metadata audioMetadata) string {
	if len(metadata.Cover) == 0 {
		return ""
	}
	return hashBytes(metadata.Cover)
}

func resourceStamp(path string, settings ServerSettings) (string, error) {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%t/%t/%s", settings.ScanExternalLyrics, settings.ScanExternalCovers, settings.FFprobePath)
	for _, entry := range entries {
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if ext != ".lrc" && ext != ".elrc" && ext != ".txt" && ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "/%s/%d/%d", entry.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return hashBytes([]byte(builder.String())), nil
}

func (s *Store) StartScan(full bool, options ...MutationOptions) (ScanStatus, error) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.closed {
		return ScanStatus{}, errors.New("store is closed")
	}
	if s.scanCancel != nil {
		return s.scanStatus, errors.New("scan already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scanCancel = cancel
	s.scanStatus = ScanStatus{ID: uuid.NewString(), State: "running", Full: full, Errors: []string{}, StartedAt: time.Now().UTC()}
	initial := s.scanStatus
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		changed, err := s.runScan(ctx, full, options)
		s.scanMu.Lock()
		defer s.scanMu.Unlock()
		s.scanStatus.Changed = changed
		s.scanStatus.FinishedAt = time.Now().UTC()
		s.scanStatus.State = "completed"
		if errors.Is(err, context.Canceled) {
			s.scanStatus.State = "cancelled"
		} else if err != nil {
			s.scanStatus.State = "failed"
			s.scanStatus.Errors = append(s.scanStatus.Errors, err.Error())
		}
		s.scanCancel = nil
		cancel()
	}()
	return initial, nil
}
func (s *Store) ScanStatus() ScanStatus {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	result := s.scanStatus
	result.Errors = cloneStrings(result.Errors)
	return result
}
func (s *Store) CancelScan() {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.scanCancel != nil {
		s.scanCancel()
	}
}
func (s *Store) Scan() (int, error) {
	s.scanMu.Lock()
	if s.closed {
		s.scanMu.Unlock()
		return 0, errors.New("store is closed")
	}
	if s.scanCancel != nil {
		s.scanMu.Unlock()
		return 0, errors.New("scan already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.scanCancel = cancel
	s.scanStatus = ScanStatus{ID: uuid.NewString(), State: "running", Full: true, Errors: []string{}, StartedAt: time.Now().UTC()}
	s.workers.Add(1)
	s.scanMu.Unlock()
	defer s.workers.Done()
	defer cancel()
	changed, err := s.runScan(ctx, true, nil)
	s.scanMu.Lock()
	s.scanCancel = nil
	s.scanStatus.Changed = changed
	s.scanStatus.State = "completed"
	s.scanStatus.FinishedAt = time.Now().UTC()
	if err != nil {
		s.scanStatus.State = "failed"
		s.scanStatus.Errors = append(s.scanStatus.Errors, err.Error())
	}
	s.scanMu.Unlock()
	return len(s.Tracks()), err
}

func (s *Store) runScan(ctx context.Context, full bool, options []MutationOptions) (changedResult int, scanErr error) {
	started := time.Now()
	fields := activity.Fields{}
	if len(options) > 0 {
		fields.UserID = options[0].UserID
	}
	_ = s.activity.Emit("scan.started", fields)
	defer func() {
		fields.DurationMS = time.Since(started).Milliseconds()
		code := "scan.completed"
		if scanErr != nil {
			code = "scan.failed"
		}
		_ = s.activity.Emit(code, fields)
	}()
	baseline, err := s.allTracks()
	if err != nil {
		return 0, err
	}
	byPath := map[string]Track{}
	for _, track := range baseline {
		byPath[track.Path] = track
	}
	settings := s.Settings()
	stamps := map[string]string{}
	rows, err := s.db.Query("SELECT track_id,stamp FROM scan_stamps")
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var id, stamp string
		if err := rows.Scan(&id, &stamp); err != nil {
			_ = rows.Close()
			return 0, err
		}
		stamps[id] = stamp
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	candidates := []scanCandidate{}
	newHashes := map[string]int{}
	directoryStamps := map[string]string{}
	dataPath, err := filepath.Abs(s.dataPath)
	if err != nil {
		return 0, err
	}
	dataDirectory := filepath.Dir(dataPath)
	err = filepath.WalkDir(s.mediaRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != s.mediaRoot && (entry.Name() == ".staging" || entry.Name() == ".server-archive" || path == dataDirectory) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !audioExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		relative, err := filepath.Rel(s.mediaRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		s.mediaMu.RLock()
		defer s.mediaMu.RUnlock()
		seen[relative] = true
		previous, exists := byPath[relative]
		info, err := entry.Info()
		directory := filepath.Dir(path)
		stamp, cached := directoryStamps[directory]
		var stampErr error
		if !cached {
			stamp, stampErr = resourceStamp(path, settings)
			if stampErr == nil {
				directoryStamps[directory] = stamp
			}
		}
		if err == nil {
			err = stampErr
		}
		if err == nil && !full && exists && !previous.Missing && previous.Size == info.Size() && previous.ModifiedAt.Equal(info.ModTime()) && stamps[previous.ID] == stamp {
			return nil
		}
		var track Track
		var metadata audioMetadata
		if err == nil {
			track, metadata, err = s.readTrackWithSettings(ctx, path, entry.Name(), settings)
		}
		s.scanMu.Lock()
		s.scanStatus.Processed++
		if err != nil && len(s.scanStatus.Errors) < 100 {
			s.scanStatus.Errors = append(s.scanStatus.Errors, relative+": "+err.Error())
		}
		s.scanMu.Unlock()
		if err != nil {
			return ctx.Err()
		}
		candidates = append(candidates, scanCandidate{track, metadata, stamp})
		if !exists {
			newHashes[track.ContentHash]++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	missingHashes := map[string][]Track{}
	for _, track := range baseline {
		if !seen[track.Path] {
			missingHashes[track.ContentHash] = append(missingHashes[track.ContentHash], track)
		}
	}
	changed := 0
	err = s.transaction(func(tx *sql.Tx) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		matched := map[string]bool{}
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return err
			}
			track := candidate.track
			previous, exists := byPath[track.Path]
			if !exists && newHashes[track.ContentHash] == 1 && len(missingHashes[track.ContentHash]) == 1 {
				previous = missingHashes[track.ContentHash][0]
				exists = true
			}
			if exists {
				current, err := readTrackFrom(tx, previous.ID, true)
				if err != nil {
					return err
				}
				if current.Path != previous.Path || current.ContentHash != previous.ContentHash {
					continue
				}
				track.ID = current.ID
				track.Version = current.Version
				track.Removed = current.Removed
				track.OwnerID, track.UploadedBy = current.OwnerID, current.UploadedBy
				previous = current
			} else {
				var count int
				if err := tx.QueryRow("SELECT count(*) FROM tracks WHERE path=?", track.Path).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					continue
				}
				if len(options) > 0 && options[0].UserID != "" {
					track.OwnerID = options[0].UserID
				} else {
					if err := tx.QueryRow("SELECT value FROM meta WHERE key='scan_owner_id'").Scan(&track.OwnerID); err != nil {
						return errors.New("scan owner is not configured; initialize an administrator first")
					}
				}
				owner, err := readUser(tx, track.OwnerID)
				if err != nil || owner.Role != "admin" || !owner.Enabled || owner.MustChangePassword {
					return errors.New("scan owner must be an enabled administrator")
				}
				track.UploadedBy = track.OwnerID
			}
			matched[track.ID] = true
			overrides, err := readOverrides(tx, track.ID)
			if err != nil {
				return err
			}
			applyMetadata(&track, overrides)
			if !reflect.DeepEqual(track, previous) {
				if err := saveCover(tx, candidate.metadata.Cover, candidate.metadata.CoverMIME); err != nil {
					return err
				}
				track.Version++
				if err := writeTrack(tx, track); err != nil {
					return err
				}
				if !track.Removed {
					if err := recordChange(tx, "track", track.ID, false); err != nil {
						return err
					}
				}
				if err := auditMutation(tx, "track.scan", track.ID, previous, track, options); err != nil {
					return err
				}
				changed++
			}
			if _, err := tx.Exec("INSERT INTO scan_stamps VALUES(?,?) ON CONFLICT(track_id) DO UPDATE SET stamp=excluded.stamp", track.ID, candidate.stamp); err != nil {
				return err
			}
		}
		for _, previous := range baseline {
			if seen[previous.Path] || matched[previous.ID] || previous.Missing {
				continue
			}
			current, err := readTrackFrom(tx, previous.ID, true)
			if err != nil {
				return err
			}
			if current.Path != previous.Path || current.Version != previous.Version {
				continue
			}
			before := current
			current.Missing = true
			current.Version++
			if err := writeTrack(tx, current); err != nil {
				return err
			}
			if !current.Removed {
				if err := recordChange(tx, "track", current.ID, false); err != nil {
					return err
				}
			}
			if err := auditMutation(tx, "track.missing", current.ID, before, current, options); err != nil {
				return err
			}
			changed++
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return refreshLibrary(tx)
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}
