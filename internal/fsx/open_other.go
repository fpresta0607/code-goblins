//go:build !windows

package fsx

import (
	"errors"
	"os"
	"syscall"
)

// open is os.Open: elsewhere a reader never blocks a rename over its file.
func open(path string) (*os.File, error) {
	return os.Open(path)
}

func createNewUnshared(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
}

// heldByAnother reports whether err is the kernel's lock on a file (flock)
// refused because another holds it, as RemoveFileWhen claims a file. Nothing
// else is held here: no process holds a file against another's open or
// rename.
func heldByAnother(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK)
}
