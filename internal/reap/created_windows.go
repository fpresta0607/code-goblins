package reap

import (
	"os"
	"syscall"
	"time"
)

// createdAt is when a directory was made, zero when that cannot be read.
func createdAt(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, data.CreationTime.Nanoseconds()).UTC()
}
