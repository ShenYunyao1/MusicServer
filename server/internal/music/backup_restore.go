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
	"path/filepath"
	"strings"

	"musicserver/internal/version"
)

func readBackupManifest(archive *tar.Reader) (BackupManifest, error) {
	var manifest BackupManifest
	header, err := archive.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > maxBackupManifest {
		return manifest, errors.New("invalid backup manifest header")
	}
	decoder := json.NewDecoder(io.LimitReader(archive, maxBackupManifest+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, errors.New("invalid backup manifest JSON")
	}
	if decoder.Decode(new(any)) != io.EOF || manifest.Format != 1 || manifest.Schema != version.Schema || manifest.CreatedAt.IsZero() || len(manifest.Files) < 2 || len(manifest.Files) > maxBackupFiles {
		return manifest, errors.New("invalid or incompatible backup manifest")
	}
	if _, err := backupArchiveName(manifest.ID); err != nil {
		return manifest, err
	}
	seen := map[string]bool{}
	var total int64
	for _, file := range manifest.Files {
		if !validBackupPath(file.Path) || seen[file.Path] || file.Size < 0 || file.Size > maxBackupBytes || len(file.SHA256) != 64 || !manifest.IncludeMedia && strings.HasPrefix(file.Path, "media/") {
			return manifest, errors.New("invalid backup file declaration")
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return manifest, errors.New("invalid backup checksum")
		}
		seen[file.Path] = true
		total += file.Size
		if total > maxBackupBytes {
			return manifest, errors.New("backup exceeds restore size limit")
		}
	}
	if !seen["database.sqlite"] || !seen["config.json"] {
		return manifest, errors.New("backup database or configuration is missing")
	}
	return manifest, nil
}

func (s *Store) BackupArchives() ([]BackupArchive, error) {
	items := []BackupArchive{}
	entries, err := os.ReadDir(s.backupDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), "backup-") || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "backup-"), ".tar.gz")
		if _, err := backupArchiveName(id); err != nil {
			continue
		}
		file, err := os.Open(filepath.Join(s.backupDirectory(), entry.Name()))
		if err != nil {
			return nil, err
		}
		compressed, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("cannot read backup %s", id)
		}
		manifest, readErr := readBackupManifest(tar.NewReader(compressed))
		info, statErr := file.Stat()
		compressed.Close()
		file.Close()
		if readErr != nil || statErr != nil || manifest.ID != id {
			return nil, fmt.Errorf("invalid backup %s", id)
		}
		items = append(items, BackupArchive{ID: id, Name: entry.Name(), CreatedAt: manifest.CreatedAt, Size: info.Size(), IncludeMedia: manifest.IncludeMedia, FileCount: len(manifest.Files), MissingCount: len(manifest.MissingMedia), Automatic: manifest.Automatic})
	}
	return items, nil
}

// RestoreBackup never overwrites a running library: the destination must be a
// new or empty directory. All extraction and verification happens in a sibling.
func RestoreBackup(ctx context.Context, archivePath, destination string) (BackupManifest, error) {
	var manifest BackupManifest
	target, err := filepath.Abs(destination)
	if err != nil || strings.TrimSpace(destination) == "" {
		return manifest, errors.New("restore destination is required")
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return manifest, errors.New("restore destination must be a real empty directory")
		}
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) != 0 {
			return manifest, errors.New("refusing to overwrite a non-empty restore destination")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return manifest, err
	}
	input, err := os.Open(archivePath)
	if err != nil {
		return manifest, err
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return manifest, err
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	manifest, err = readBackupManifest(archive)
	if err != nil {
		return manifest, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return manifest, err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(target), ".musicserver-restore-*")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(temporary)
	expected := map[string]BackupFile{}
	for _, file := range manifest.Files {
		expected[file.Path] = file
	}
	seen := map[string]bool{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return manifest, err
		}
		declaration, exists := expected[header.Name]
		if !exists || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size != declaration.Size {
			return manifest, errors.New("unexpected, duplicate or non-regular backup entry")
		}
		seen[header.Name] = true
		relative := strings.TrimPrefix(header.Name, "media/")
		if header.Name == "database.sqlite" {
			relative = "data/library.sqlite"
		} else if header.Name == "config.json" {
			relative = "data/backup-settings.json"
		}
		outputPath := filepath.Join(temporary, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
			return manifest, err
		}
		file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return manifest, err
		}
		hash := sha256.New()
		n, copyErr := backupCopy(ctx, io.MultiWriter(file, hash), archive, nil)
		err = errors.Join(copyErr, file.Sync(), file.Close())
		if err != nil || n != declaration.Size || hex.EncodeToString(hash.Sum(nil)) != declaration.SHA256 {
			return manifest, errors.New("backup file size or SHA-256 mismatch")
		}
	}
	if len(seen) != len(expected) {
		return manifest, errors.New("backup archive is incomplete")
	}
	if n, err := backupCopy(ctx, io.Discard, io.LimitReader(compressed, 1), nil); err != nil || n != 0 {
		return manifest, errors.New("invalid backup compression checksum or trailing data")
	}
	if err := prepareRestoredDatabase(filepath.Join(temporary, "data/library.sqlite"), target); err != nil {
		return manifest, err
	}
	if err := ctx.Err(); err != nil {
		return manifest, err
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return manifest, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func prepareRestoredDatabase(path, mediaRoot string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	var schema int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schema); err != nil || schema != version.Schema {
		return errors.New("restored database schema is incompatible")
	}
	if err := initializeSQLiteSchema(db); err != nil {
		return err
	}
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return errors.New("restored database integrity check failed")
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	hasViolation := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return rowErr
	}
	if hasViolation {
		return errors.New("restored database has invalid references")
	}
	var raw string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='server'").Scan(&raw); err != nil {
		return err
	}
	var settings ServerSettings
	if json.Unmarshal([]byte(raw), &settings) != nil {
		return errors.New("restored application settings are invalid")
	}
	settings.MediaRoot = mediaRoot
	if _, err := os.Stat(settings.FFprobePath); err != nil {
		settings.FFprobePath = ""
	}
	encoded, _ := json.Marshal(settings)
	secret, err := randomToken(32)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM sessions"); err != nil {
		return err
	}
	// Temporary upload files are deliberately excluded from archives.
	if _, err := tx.Exec("DELETE FROM upload_staging"); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE settings SET value=? WHERE key='server'", string(encoded)); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE meta SET value=? WHERE key='sync_secret'", secret); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE settings SET value=json_set(value,'$.enabled',json('false')) WHERE key='backup_policy'"); err != nil {
		return err
	}
	return tx.Commit()
}
