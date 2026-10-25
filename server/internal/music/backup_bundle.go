package music

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"musicserver/internal/version"
)

const maxBackupManifest = 16 << 20
const maxBackupFiles = 100000
const maxBackupBytes = int64(1 << 40)

type BackupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type BackupManifest struct {
	Format       int          `json:"format"`
	ID           string       `json:"id"`
	CreatedAt    time.Time    `json:"created_at"`
	Version      string       `json:"version"`
	Schema       int          `json:"schema"`
	IncludeMedia bool         `json:"include_media"`
	Automatic    bool         `json:"automatic,omitempty"`
	Files        []BackupFile `json:"files"`
	MissingMedia []string     `json:"missing_media"`
}
type BackupArchive struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	Size         int64     `json:"size"`
	IncludeMedia bool      `json:"include_media"`
	FileCount    int       `json:"file_count"`
	MissingCount int       `json:"missing_count"`
	Automatic    bool      `json:"automatic"`
}
type backupInput struct {
	BackupFile
	Local string
}

func backupArchiveName(id string) (string, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return "", errors.New("invalid backup ID")
	}
	return "backup-" + id + ".tar.gz", nil
}
func validBackupPath(name string) bool {
	if name == "database.sqlite" || name == "config.json" {
		return true
	}
	if !strings.HasPrefix(name, "media/") || strings.ContainsAny(name, "\\:") || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(strings.TrimPrefix(name, "media/"))) {
		return false
	}
	relative := strings.TrimPrefix(name, "media/")
	first := strings.ToLower(strings.Split(relative, "/")[0])
	if first == "data" || first == ".staging" || first == ".server-archive" || first == "backups" || first == "updates" {
		return false
	}
	ext := strings.ToLower(path.Ext(relative))
	return audioExtensions[ext] || ext == ".lrc" || ext == ".elrc" || ext == ".txt" || ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" || ext == ".webp"
}
func backupFoldName(name string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return minimum
	}, name)
}
func backupAcquire(ctx context.Context, try func() bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if try() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func backupCopy(ctx context.Context, w io.Writer, r io.Reader, progress func(int64)) (int64, error) {
	var copied int64
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
		n, readErr := r.Read(buffer)
		if n > 0 {
			written, err := w.Write(buffer[:n])
			copied += int64(written)
			if progress != nil {
				progress(int64(written))
			}
			if err != nil {
				return copied, err
			}
			if written != n {
				return copied, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return copied, nil
		}
		if readErr != nil {
			return copied, readErr
		}
	}
}
func backupFileInfo(ctx context.Context, file *os.File) (int64, string, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, "", errors.New("backup input is not a regular file")
	}
	if info.Size() > maxBackupBytes {
		return 0, "", errors.New("backup input exceeds format limit")
	}
	hash := sha256.New()
	n, err := backupCopy(ctx, hash, file, nil)
	return n, hex.EncodeToString(hash.Sum(nil)), err
}
func (s *Store) backupDirectory() string { return filepath.Join(filepath.Dir(s.dataPath), "backups") }

// CreateBackupBundle freezes the database first. Only media referenced by that
// snapshot is copied. Application media writes publish new immutable paths, so
// compression need not hold the publication lock after taking the snapshot.
func (s *Store) CreateBackupBundle(ctx context.Context, includeMedia bool, progress func(string, int64, int64), automatic ...bool) (BackupArchive, error) {
	if err := backupAcquire(ctx, s.backupBundleMu.TryLock); err != nil {
		return BackupArchive{}, err
	}
	defer s.backupBundleMu.Unlock()
	if err := backupAcquire(ctx, s.mediaMu.TryRLock); err != nil {
		return BackupArchive{}, err
	}
	report := func(stage string, done, total int64) {
		if progress != nil {
			progress(stage, done, total)
		}
	}
	report("snapshot", 0, 0)
	snapshot, err := s.backupSnapshot(ctx)
	s.mediaMu.RUnlock()
	if err != nil {
		return BackupArchive{}, err
	}
	defer os.Remove(snapshot)
	db, err := openSQLite(snapshot)
	if err != nil {
		return BackupArchive{}, err
	}
	defer db.Close()
	tracks, err := queryJSON[Track](db, "SELECT payload FROM tracks ORDER BY path")
	if err != nil {
		return BackupArchive{}, err
	}
	var settings string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='server'").Scan(&settings); err != nil {
		return BackupArchive{}, err
	}
	config, err := os.CreateTemp(s.backupDirectory(), ".backup-config-*")
	if err != nil {
		return BackupArchive{}, err
	}
	configPath := config.Name()
	defer os.Remove(configPath)
	_, writeErr := config.WriteString(settings)
	closeErr := config.Close()
	if writeErr != nil || closeErr != nil {
		return BackupArchive{}, errors.Join(writeErr, closeErr)
	}
	manifest := BackupManifest{Format: 1, ID: uuid.NewString(), CreatedAt: time.Now().UTC(), Version: version.Version, Schema: version.Schema, IncludeMedia: includeMedia, Files: []BackupFile{}, MissingMedia: []string{}}
	manifest.Automatic = len(automatic) > 0 && automatic[0]
	inputs := []backupInput{{BackupFile: BackupFile{Path: "database.sqlite"}, Local: snapshot}, {BackupFile: BackupFile{Path: "config.json"}, Local: configPath}}
	root, err := os.OpenRoot(s.mediaRoot)
	if err != nil {
		return BackupArchive{}, err
	}
	defer root.Close()
	media := map[string]string{}
	directoryNames := map[string]map[string][]string{}
	if includeMedia {
		for _, track := range tracks {
			name := filepath.ToSlash(track.Path)
			if !validBackupPath("media/" + name) {
				return BackupArchive{}, fmt.Errorf("unsupported media path: %s", name)
			}
			file, err := root.Open(track.Path)
			if errors.Is(err, os.ErrNotExist) {
				manifest.MissingMedia = append(manifest.MissingMedia, name)
				continue
			}
			if err != nil {
				return BackupArchive{}, err
			}
			info, err := file.Stat()
			metadata, _ := readAudioMetadata(file)
			file.Close()
			if err != nil || !info.Mode().IsRegular() || info.Size() != track.Size || track.ContentHash == "" {
				return BackupArchive{}, fmt.Errorf("media changed since snapshot: %s", name)
			}
			inputs = append(inputs, backupInput{BackupFile: BackupFile{Path: "media/" + name, Size: track.Size, SHA256: track.ContentHash}, Local: track.Path})
			media[name] = track.ContentHash
			directory := filepath.Dir(track.Path)
			index := directoryNames[directory]
			if index == nil {
				dir, err := root.Open(directory)
				if err != nil {
					return BackupArchive{}, err
				}
				entries, readErr := dir.ReadDir(-1)
				dir.Close()
				if readErr != nil {
					return BackupArchive{}, readErr
				}
				index = map[string][]string{}
				for _, entry := range entries {
					if !entry.IsDir() {
						key := backupFoldName(entry.Name())
						index[key] = append(index[key], entry.Name())
					}
				}
				directoryNames[directory] = index
			}
			candidates := externalCoverNamesForMetadata(track.Path, metadata)
			stem := strings.TrimSuffix(name, path.Ext(name))
			for _, ext := range []string{".lrc", ".elrc", ".txt"} {
				candidates = append(candidates, stem+ext)
			}
			for _, candidate := range candidates {
				for _, actual := range index[backupFoldName(filepath.Base(candidate))] {
					media[path.Join(path.Dir(name), actual)] = ""
				}
			}
		}
		for name, hash := range media {
			if hash == "" {
				inputs = append(inputs, backupInput{BackupFile: BackupFile{Path: "media/" + name}, Local: filepath.FromSlash(name)})
			}
		}
	}
	report("checking", 0, 0)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	selected := inputs[:0]
	var total int64
	for _, input := range inputs {
		if !validBackupPath(input.Path) {
			return BackupArchive{}, errors.New("unsupported backup path")
		}
		if input.SHA256 == "" {
			var file *os.File
			if strings.HasPrefix(input.Path, "media/") {
				file, err = root.Open(input.Local)
			} else {
				file, err = os.Open(input.Local)
			}
			if errors.Is(err, os.ErrNotExist) && strings.HasPrefix(input.Path, "media/") {
				continue
			}
			if err != nil {
				return BackupArchive{}, err
			}
			input.Size, input.SHA256, err = backupFileInfo(ctx, file)
			file.Close()
			if err != nil {
				return BackupArchive{}, err
			}
		}
		total += input.Size
		if total > maxBackupBytes || len(selected) >= maxBackupFiles {
			return BackupArchive{}, errors.New("backup exceeds format limits")
		}
		selected = append(selected, input)
		manifest.Files = append(manifest.Files, input.BackupFile)
	}
	inputs = selected
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded) > maxBackupManifest {
		return BackupArchive{}, errors.New("backup manifest exceeds limit")
	}
	name, _ := backupArchiveName(manifest.ID)
	final := filepath.Join(s.backupDirectory(), name)
	partial := final + ".partial"
	output, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return BackupArchive{}, err
	}
	defer os.Remove(partial)
	success := false
	defer func() {
		output.Close()
		if !success {
			os.Remove(final)
		}
	}()
	compressed, _ := gzip.NewWriterLevel(output, gzip.BestSpeed)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(encoded))}); err != nil {
		return BackupArchive{}, err
	}
	if _, err := archive.Write(encoded); err != nil {
		return BackupArchive{}, err
	}
	var done int64
	report("packing", 0, total)
	for _, input := range inputs {
		var file *os.File
		if strings.HasPrefix(input.Path, "media/") {
			file, err = root.Open(input.Local)
		} else {
			file, err = os.Open(input.Local)
		}
		if err != nil {
			return BackupArchive{}, err
		}
		err = archive.WriteHeader(&tar.Header{Name: input.Path, Mode: 0600, Size: input.Size})
		hash := sha256.New()
		var n int64
		if err == nil {
			n, err = backupCopy(ctx, io.MultiWriter(archive, hash), io.LimitReader(file, input.Size+1), func(n int64) { done += n; report("packing", done, total) })
		}
		file.Close()
		if err != nil || n != input.Size || hex.EncodeToString(hash.Sum(nil)) != input.SHA256 {
			return BackupArchive{}, fmt.Errorf("backup input changed or copy failed (%s): %w", input.Path, errors.Join(err, errors.New("size/hash mismatch")))
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close(), output.Sync(), output.Close()); err != nil {
		return BackupArchive{}, err
	}
	if err := ctx.Err(); err != nil {
		return BackupArchive{}, err
	}
	if err := os.Rename(partial, final); err != nil {
		return BackupArchive{}, err
	}
	info, err := os.Stat(final)
	if err != nil {
		return BackupArchive{}, err
	}
	success = true
	report("completed", total, total)
	return BackupArchive{ID: manifest.ID, Name: name, CreatedAt: manifest.CreatedAt, Size: info.Size(), IncludeMedia: includeMedia, FileCount: len(inputs), MissingCount: len(manifest.MissingMedia), Automatic: manifest.Automatic}, nil
}
