//go:build windows

package uninstall

import (
	"os"
	"syscall"
	"time"
)

// createdAt is when the file at path was made.
func createdAt(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return info.ModTime(), true
	}
	return time.Unix(0, data.CreationTime.Nanoseconds()), true
}
