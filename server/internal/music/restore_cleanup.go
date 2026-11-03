package music

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Explicit maintenance for abandoned restore destinations. A directory is
// removable only while we own its exact destination lock. Lock files stay:
// unlinking them can create two independent locks for a concurrent restore.
func CleanupRestoreStaging(parent string) (removed, active int, failure error) {
	absolute, err := filepath.Abs(parent)
	if err != nil {
		return 0, 0, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return 0, 0, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return 0, 0, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return 0, 0, err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return 0, 0, err
	}
	var failures []error
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, ".musicserver-restore-") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(name, ".musicserver-restore-"), "-")
		if len(parts) != 2 || len(parts[0]) != 32 {
			continue
		}
		decoded, e := hex.DecodeString(parts[0])
		if e != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != parts[0] {
			continue
		}
		if _, e := strconv.ParseUint(parts[1], 10, 32); e != nil {
			continue
		}
		unlock, e := LockDatabaseProcess(filepath.Join(absolute, ".musicserver-restorelock-"+parts[0]))
		if e != nil {
			if errors.Is(e, ErrDatabaseInUse) {
				active++
			} else {
				failures = append(failures, fmt.Errorf("restore staging %q: %w", name, e))
			}
			continue
		}
		owned, verifyErr := managedRestoreStage(absolute, name)
		if verifyErr != nil {
			failures = append(failures, verifyErr)
		}
		if !owned || verifyErr != nil {
			unlock()
			continue
		}
		e = root.RemoveAll(name)
		unlock()
		if e != nil {
			failures = append(failures, e)
		} else {
			removed++
		}
	}
	return removed, active, errors.Join(failures...)
}

// The caller owns the destination-specific OS lock. A crash releases that lock;
// the next restore may remove only that destination's abandoned staging dirs.
func cleanupRestoreTemporary(parent, prefix string) error {
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	dir.Close()
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), prefix), 10, 32); err != nil {
			continue
		}
		owned, verifyErr := managedRestoreStage(parent, entry.Name())
		if verifyErr != nil {
			failures = append(failures, verifyErr)
			continue
		}
		if !owned {
			continue
		}
		if err := root.RemoveAll(entry.Name()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
