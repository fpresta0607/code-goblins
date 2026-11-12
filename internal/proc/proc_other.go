//go:build !windows

package proc

import (
	"errors"
	"fmt"
	"time"
)

// notPorted is the error of a reading this package does only on Windows so
// far.
func notPorted(what string) error {
	return fmt.Errorf("proc: %s is not ported to this system yet: %w", what, errors.ErrUnsupported)
}

// Lists is how many times this process has listed the machine's processes,
// which it never does on this system yet.
func Lists() int64 { return 0 }

// Processes refuses: listing the machine's processes is not ported yet.
func Processes() ([]Entry, error) {
	return nil, notPorted("listing processes")
}

// Ancestry refuses: walking a process's parents is not ported yet.
func Ancestry(pid int, maxHops int) ([]Entry, error) {
	return nil, notPorted("walking a process's parents")
}

// FindAncestor finds no ancestor, since Ancestry is not ported yet.
func FindAncestor(pid int, maxHops int, names ...string) (Entry, bool) {
	return Entry{}, false
}

// CPUTime reports pid as not measured: reading a process's processor time is
// not ported yet.
func CPUTime(pid int) (time.Duration, bool) {
	return 0, false
}

// StartTime reports pid as not found: reading a process's creation time is
// not ported yet.
func StartTime(pid int) (time.Time, bool) {
	return time.Time{}, false
}
