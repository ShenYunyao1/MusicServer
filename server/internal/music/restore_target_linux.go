package music

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func restoreDestinationMount(target string) (bool, error) {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	parent := filepath.Dir(target)
	if parent == target {
		return true, nil
	}
	var a, b unix.Statx_t
	if err := unix.Statx(unix.AT_FDCWD, target, 0, unix.STATX_MNT_ID, &a); err != nil {
		return false, err
	}
	if err := unix.Statx(unix.AT_FDCWD, parent, 0, unix.STATX_MNT_ID, &b); err != nil {
		return false, err
	}
	if a.Mask&unix.STATX_MNT_ID == 0 || b.Mask&unix.STATX_MNT_ID == 0 {
		return false, errors.New("cannot verify restore filesystem boundary; choose a new child directory inside the mounted volume")
	}
	return a.Mnt_id != b.Mnt_id, nil
}
