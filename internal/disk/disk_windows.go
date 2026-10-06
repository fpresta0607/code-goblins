package disk

import "golang.org/x/sys/windows"

func space(path string) (free, total uint64, err error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var all uint64
	if err := windows.GetDiskFreeSpaceEx(name, &free, &total, &all); err != nil {
		return 0, 0, err
	}
	return free, total, nil
}
