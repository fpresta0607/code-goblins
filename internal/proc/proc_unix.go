//go:build linux || darwin

package proc

import "time"

// StartTime returns pid's start, and whether the process was found running.
func StartTime(pid int) (time.Time, bool) {
	entry, err := Lookup(pid)
	return entry.Start, err == nil
}

// Use is what one running process uses now (UseOf): the processor time it
// has used, and its private memory, which no other process shares.
type Use struct {
	CPU          time.Duration
	PrivateBytes uint64
}
