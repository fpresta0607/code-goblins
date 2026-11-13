//go:build !windows && !linux

package proc

import "time"

// Processes refuses: listing the machine's processes is not ported yet.
func Processes() ([]Entry, error) {
	return nil, notPorted("listing processes")
}

// Ancestry refuses: walking a process's parents is not ported yet.
func Ancestry(pid int, maxHops int) ([]Entry, error) {
	return nil, notPorted("walking a process's parents")
}

// StartTime reports pid as not found: reading a process's creation time is
// not ported yet.
func StartTime(pid int) (time.Time, bool) {
	return time.Time{}, false
}

// WorkingDirectory refuses: reading another process's working directory is
// not ported yet.
func WorkingDirectory(pid int) (string, error) {
	return "", notPorted("reading a process's working directory")
}

// Arguments refuses, as CommandLine does.
func Arguments(pid int) ([]string, error) {
	return nil, notPorted("reading a process's arguments")
}
