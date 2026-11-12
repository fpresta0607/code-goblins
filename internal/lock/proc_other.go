//go:build !windows

package lock

import "time"

// processStatus indicates whether a process is verifiably alive, dead, or unknown.
type processStatus int

const (
	statusDead    processStatus = iota // Process does not exist
	statusAlive                        // Process exists and is running
	statusUnknown                      // Cannot verify (fail closed: treat as alive)
)

// processStart reports every process as unknown, which a holder's check
// treats as alive: reading a process's creation time is not ported to this
// system yet.
func processStart(pid int) (time.Time, processStatus) {
	return time.Time{}, statusUnknown
}
