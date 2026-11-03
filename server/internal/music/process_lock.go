package music

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrDatabaseInUse = errors.New("database is in use; stop the server before administration")

// Both the HTTP server and local administrator commands hold this lock.
// The OS releases it when the owning process exits, including after a crash.
func LockDatabaseProcess(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockProcessFile(file); err != nil {
		_ = file.Close()
		if processLockContended(err) {
			return nil, fmt.Errorf("%w (%s): %w", ErrDatabaseInUse, path, err)
		}
		return nil, fmt.Errorf("cannot lock database %q; check directory permissions and local filesystem support: %w", path, err)
	}
	return func() { _ = file.Close() }, nil
}
