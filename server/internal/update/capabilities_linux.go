//go:build linux

package update

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func readWorkerCapabilities(path string) (workerCapabilities, error) {
	var c workerCapabilities
	if err := rootOwned(filepath.Dir(path), true); err != nil {
		return c, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return c, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return c, errors.New("updater capabilities must be root-owned and not group/world writable")
	}
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return c, err
	}
	return decodeCapabilities(data)
}

// WriteCapabilities is called only after the active independent worker is installed.
func (i *Installer) WriteCapabilities() error {
	if os.Geteuid() != 0 {
		return errors.New("updater capabilities must be written by root")
	}
	if err := rootOwned(i.Config.State, true); err != nil {
		return err
	}
	if err := verifyInstalledWorker(); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(i.Config.State, capabilitiesName), currentCapabilities(), 0644)
}
