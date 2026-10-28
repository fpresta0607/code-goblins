//go:build !windows

package fsx

import "os"

// renameNew links to to from only if to is missing, as a rename replaces an
// existing to here, and then removes the name from.
func renameNew(from, to string) error {
	if err := os.Link(from, to); err != nil {
		return err
	}
	os.Remove(from)
	return nil
}
