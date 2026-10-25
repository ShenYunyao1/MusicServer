// Package diskspace reports space available to this process, rather than space
// reserved for the operating system or another account.
package diskspace

import (
	"errors"
	"os"
	"path/filepath"
)

type Space struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
}

const DefaultReserve int64 = 512 << 20

func Read(path string) (Space, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Space{}, err
	}
	for {
		_, err = os.Stat(path)
		if err == nil {
			return read(path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Space{}, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return Space{}, err
		}
		path = parent
	}
}
