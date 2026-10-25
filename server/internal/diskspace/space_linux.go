package diskspace

import "golang.org/x/sys/unix"

func read(path string) (Space, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	return Space{Total: stat.Blocks * uint64(stat.Bsize), Available: stat.Bavail * uint64(stat.Bsize)}, err
}
