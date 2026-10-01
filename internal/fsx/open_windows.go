package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// open opens path for reading as os.Open does, except that it shares the
// file for deletion as well as reading and writing: Go's own open does not,
// and a rename over a file someone holds that way fails with "Access is
// denied".
func open(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(longPathName(path))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// FILE_FLAG_BACKUP_SEMANTICS lets a directory open, as os.Open allows.
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// longPathName spells an absolute path of 248 characters or more in the
// \\?\ form, as os.Open does, so it is not cut off at MAX_PATH.
func longPathName(path string) string {
	if len(path) < 248 || !filepath.IsAbs(path) || strings.HasPrefix(path, `\\?\`) {
		return path
	}
	path = filepath.Clean(path)
	if unc, ok := strings.CutPrefix(path, `\\`); ok {
		return `\\?\UNC\` + unc
	}
	return `\\?\` + path
}

// heldByAnother reports whether err is another process holding the file,
// which passes: a sharing violation, or access denied as a rename over a
// file someone holds open, or an open of one a replace has marked for
// deletion, reports it.
func heldByAnother(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
