//go:build !windows

package fsx

import "os"

// open is os.Open: elsewhere a reader never blocks a rename over its file.
func open(path string) (*os.File, error) {
	return os.Open(path)
}

// heldByAnother reports false: elsewhere no process holds a file against
// another's open or rename.
func heldByAnother(error) bool {
	return false
}
