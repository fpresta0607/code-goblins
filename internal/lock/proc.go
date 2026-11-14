package lock

import "time"

// processStatus indicates whether a process is verifiably alive, dead, or unknown.
type processStatus int

const (
	statusDead    processStatus = iota // Process does not exist
	statusAlive                        // Process exists and is running
	statusUnknown                      // Cannot verify (fail closed: treat as alive)
)

// processInstance reads which run of its program the process with that ID is
// now, on a system that sets it: Linux and macOS (proc_unix.go). On Linux the
// start a process reads at moves when the clock is stepped, so a start cannot
// say which process a record names, and on both the line of waiters names
// its tickets by it. It is a variable so that a system with nothing to read
// defines nothing: on Windows a process's creation time is fixed as the
// process is made, and the start alone knows the process.
var processInstance func(pid int) (string, processStatus)

// readProcess reads the process that has pid now: its start and, where the
// system reads one, its instance.
func readProcess(pid int) (start time.Time, instance string, status processStatus) {
	start, status = processStart(pid)
	if status == statusAlive && processInstance != nil {
		instance, status = processInstance(pid)
	}
	return start, instance, status
}
