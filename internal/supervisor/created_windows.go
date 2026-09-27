package supervisor

import (
	"os"
	"syscall"
	"time"
)

// fileCreated is when a file was created, zero when it cannot be read.
func fileCreated(path string) time.Time {
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
