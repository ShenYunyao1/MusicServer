//go:build linux || darwin || freebsd || openbsd || netbsd

package music

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockProcessFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func processLockContended(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
