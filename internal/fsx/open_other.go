//go:build !windows

package fsx

import "os"

// open is os.Open: elsewhere a reader never blocks a rename over its file.
func open(path string) (*os.File, error) {
	return os.Open(path)
}

func createNewUnshared(path string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
}

// heldByAnother reports false: elsewhere no process holds a file against
// another's open or rename.
func heldByAnother(error) bool {
	return false
}
