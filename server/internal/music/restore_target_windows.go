package music

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func restoreDestinationMount(target string) (bool, error) {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if filepath.Dir(target) == target {
		return true, nil
	}
	volume := func(path string) (string, error) {
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return "", err
		}
		buffer := make([]uint16, 32768)
		if err := windows.GetVolumePathName(name, &buffer[0], uint32(len(buffer))); err != nil {
			return "", err
		}
		return windows.UTF16ToString(buffer), nil
	}
	a, err := volume(target)
	if err != nil {
		return false, err
	}
	b, err := volume(filepath.Dir(target))
	return !strings.EqualFold(a, b), err
}
