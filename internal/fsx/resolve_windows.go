package fsx

import (
	"os"

	"golang.org/x/sys/windows"
)

// GetFinalPathNameByHandle flags golang.org/x/sys/windows does not name: the
// normalized long name, on its drive letter.
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
)

// resolve returns the final path Windows reports for an open handle to path,
// which follows every symlink and directory junction on the way. Go's
// filepath.EvalSymlinks cannot stand in for it: it sees a junction as neither
// a directory nor a symlink and fails for any path below one.
func resolve(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(longPathName(path))
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	// FILE_FLAG_BACKUP_SEMANTICS lets a directory open; no access is asked
	// for, and every share is granted, so the open blocks no one.
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "resolve", Path: path, Err: err}
	}
	defer windows.CloseHandle(handle)
	// A buffer too small is answered with the size the name needs.
	size := uint32(windows.MAX_PATH)
	for range 2 {
		buf := make([]uint16, size)
		n, err := windows.GetFinalPathNameByHandle(handle, &buf[0], size, fileNameNormalized|volumeNameDOS)
		if err != nil {
			return "", &os.PathError{Op: "resolve", Path: path, Err: err}
		}
		if n < size {
			return windows.UTF16ToString(buf[:n]), nil
		}
		size = n
	}
	return "", &os.PathError{Op: "resolve", Path: path, Err: windows.ERROR_INSUFFICIENT_BUFFER}
}
