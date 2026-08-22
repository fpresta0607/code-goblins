//go:build windows

package auth

import (
	"syscall"
)

// hardLinkCount reports how many directory entries point at one file's data.
// Windows exposes it only through an open handle, so the file is opened for
// metadata alone: zero desired access queries attributes without reading the
// contents, and every share mode is allowed so a goblin holding the file open
// cannot make this fail.
func hardLinkCount(path string) (uint32, error) {
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := syscall.CreateFile(
		pathPtr,
		0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(handle)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, err
	}
	return info.NumberOfLinks, nil
}
