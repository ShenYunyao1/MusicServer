//go:build linux || darwin || freebsd || openbsd || netbsd

package music

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockProcessFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}
