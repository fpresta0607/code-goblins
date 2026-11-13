//go:build !windows

package proc

import "time"

// OwnerOf refuses: reading whose a process is is not ported yet.
func OwnerOf(pid int, start time.Time) (Owner, error) {
	return Owner{}, notPorted("reading a process's owner")
}
