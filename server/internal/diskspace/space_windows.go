package diskspace

import "golang.org/x/sys/windows"

func read(path string) (Space, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Space{}, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(name, &available, &total, &free)
	return Space{Total: total, Available: available}, err
}
