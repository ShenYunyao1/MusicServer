package music

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

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
		if err := root.RemoveAll(entry.Name()); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
