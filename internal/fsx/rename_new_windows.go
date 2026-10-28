package fsx

import (
	"os"

	"golang.org/x/sys/windows"
)

// renameNew renames from to to only if to is missing. A rename without
// MOVEFILE_REPLACE_EXISTING refuses an existing to with
// ERROR_ALREADY_EXISTS.
func renameNew(from, to string) error {
	fromName, err := windows.UTF16PtrFromString(longPathName(from))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	toName, err := windows.UTF16PtrFromString(longPathName(to))
	if err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	if err := windows.MoveFileEx(fromName, toName, 0); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
