//go:build linux || darwin

package proc

import "time"

// StartTime returns pid's start, and whether the process was found running.
func StartTime(pid int) (time.Time, bool) {
	entry, err := Lookup(pid)
	return entry.Start, err == nil
}
