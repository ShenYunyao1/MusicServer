package music

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

func backupTemporaryName(name string) bool {
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if strings.HasPrefix(name, ".backup-snapshot-") && strings.HasSuffix(name, suffix) {
			name = strings.TrimSuffix(name, suffix)
			break
		}
	}
	if strings.HasPrefix(name, ".backup-config-") {
		_, err := strconv.ParseUint(strings.TrimPrefix(name, ".backup-config-"), 10, 32)
		return err == nil
	}
	if strings.HasPrefix(name, ".backup-snapshot-") && strings.HasSuffix(name, ".sqlite") {
		id := strings.TrimSuffix(strings.TrimPrefix(name, ".backup-snapshot-"), ".sqlite")
		parsed, err := uuid.Parse(id)
		return err == nil && parsed.String() == id
	}
	if strings.HasSuffix(name, ".tar.gz.partial") {
		id := strings.TrimSuffix(strings.TrimPrefix(name, "backup-"), ".tar.gz.partial")
		expected, err := backupArchiveName(id)
		return err == nil && name == expected+".partial"
	}
	return false
}

func (s *Store) CleanupBackupTemporary() error {
	if !s.backupBundleMu.TryLock() {
		return nil
	}
	defer s.backupBundleMu.Unlock()
	return s.cleanupBackupTemporaryLocked()
}
func (s *Store) cleanupBackupTemporaryLocked() error {
	entries, err := os.ReadDir(s.backupDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".backup-media-") {
			if _, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), ".backup-media-"), 10, 32); err == nil {
				// Exact managed prefix, numeric random suffix, and a resolved
				// backup directory; never follow a symlink entry.
				root, e := os.OpenRoot(s.backupDirectory())
				if e != nil {
					failures = append(failures, e)
					continue
				}
				e = root.RemoveAll(entry.Name())
				root.Close()
				if e != nil {
					failures = append(failures, e)
				}
			}
			continue
		}
		// Never follow symlinks or recursively remove arbitrary directories.
		if !entry.Type().IsRegular() || !backupTemporaryName(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(s.backupDirectory(), entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
