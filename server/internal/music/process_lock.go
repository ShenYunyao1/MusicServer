package music

import (
	"fmt"
	"os"
	"path/filepath"
)

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
		return nil, fmt.Errorf("database is in use; stop the server before administration: %w", err)
	}
	return func() { _ = file.Close() }, nil
}
