//go:build linux || darwin

package supervise

import "github.com/fpresta0607/code-goblins/internal/proc"

// pidAlive reports whether pid names a running process, as the system reads
// it. As on Windows it is a plain check that the process is there, and a
// process that cannot be read is not alive.
func pidAlive(pid int) bool {
	_, isRunning := proc.StartTime(pid)
	return isRunning
}
