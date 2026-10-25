//go:build !windows && !linux

package diskspace

import "errors"

func read(string) (Space, error) {
	return Space{}, errors.New("disk space reporting is supported on Windows and Linux")
}
