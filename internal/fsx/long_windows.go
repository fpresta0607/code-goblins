//go:build windows

package fsx

import "syscall"

// LongPath spells path with the long name of every 8.3 short component, so
// C:\Users\RUNNER~1 and C:\Users\runneradmin compare as the one directory
// they are. Unlike Canonical it never resolves a symlink or junction. A path
// Windows cannot expand, such as one that does not exist, is returned as
// given, and whatever opens it next reports that.
func LongPath(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return path
	}
	buf := make([]uint16, syscall.MAX_LONG_PATH)
	n, err := syscall.GetLongPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return path
	}
	return syscall.UTF16ToString(buf[:n])
}
