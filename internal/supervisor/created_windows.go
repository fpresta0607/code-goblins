package supervisor

import (
	"io/fs"
	"syscall"
	"time"
)

// created is when the file info describes was created, zero when info does
// not say.
func created(info fs.FileInfo) time.Time {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}
	}
	return time.Unix(0, data.CreationTime.Nanoseconds()).UTC()
}
