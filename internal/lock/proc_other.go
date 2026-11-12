//go:build !windows

package lock

import "time"

// processStart reports every process as unknown, which a holder's check
// treats as alive: reading a process's creation time is not ported to this
// system yet.
func processStart(pid int) (time.Time, processStatus) {
	return time.Time{}, statusUnknown
}
