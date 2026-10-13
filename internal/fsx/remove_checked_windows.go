package fsx

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// RemoveFileWhen claims a file against writes and replacement, checks its
// contents, and deletes that same file through its handle when check approves.
// A missing file or a declined check returns false without deleting anything.
func RemoveFileWhen(ctx context.Context, path string, check func([]byte) (bool, error)) (isRemoved bool, err error) {
	name, err := windows.UTF16PtrFromString(longPathName(path))
	if err != nil {
		return false, err
	}
	var handle windows.Handle
	err = retryTransientContext(ctx, func() (err error) {
		handle, err = windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		return err
	})
	opens.Add(1)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, &os.PathError{Op: "claim removal", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() { err = errors.Join(err, file.Close()) }()
	data, err := io.ReadAll(file)
	if err != nil {
		return false, err
	}
	isApproved, err := check(data)
	if err != nil || !isApproved {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var isDeleted byte = 1
	if err := windows.SetFileInformationByHandle(handle, windows.FileDispositionInfo, &isDeleted, 1); err != nil {
		return false, &os.PathError{Op: "delete claimed file", Path: path, Err: err}
	}
	return true, nil
}
