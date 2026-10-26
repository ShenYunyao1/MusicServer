//go:build !windows

package music

import (
	"errors"
	"os"
)

func ensureMailSecretDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid mail secret directory")
	}
	return os.Chmod(path, 0700)
}
func protectMailSecretFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("invalid mail secret file")
	}
	return os.Chmod(path, 0600)
}
