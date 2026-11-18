//go:build linux || darwin

package access

import (
	"fmt"
	"os"
	"syscall"
)

// For a report: the accounts a narrowed folder admits, what keeps it to
// them, and that a narrowing changes and walks nothing beside the folder.
const (
	Admitted = "you"
	Guard    = "mode"
	Reach    = ""
	Walk     = ""
)

// Narrow keeps path to the account this process runs as: it takes every
// permission from path's group and from everyone else, so no other account
// can open path or reach anything in it, which needs no walk of what path
// holds. It changes only the mode, so path must be this account's own, and
// an access list the file system keeps beside the mode is left as it is.
func Narrow(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.Chmod(path, info.Mode().Perm()&^0o077)
}

// IsNarrow reports whether path is narrowed already: this account owns it
// and its mode gives its group and everyone else nothing.
func IsNarrow(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	owner, isKnown := info.Sys().(*syscall.Stat_t)
	if !isKnown {
		return false, fmt.Errorf("access: %s names no owner", path)
	}
	return int(owner.Uid) == os.Getuid() && info.Mode().Perm()&0o077 == 0, nil
}
