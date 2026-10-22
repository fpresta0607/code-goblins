//go:build !windows

package fsx

import "os"

// removeAll is os.RemoveAll where a process's loaded file never refuses the
// removal of a name of it.
func removeAll(path string) error {
	return os.RemoveAll(path)
}
