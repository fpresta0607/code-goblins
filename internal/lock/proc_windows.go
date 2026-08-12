package lock

import (
	"syscall"
	"time"
)

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// processStatus indicates whether a process is verifiably alive, dead, or unknown.
type processStatus int

const (
	statusDead    processStatus = iota // Process does not exist
	statusAlive                        // Process exists and is running
	statusUnknown                      // Cannot verify (fail closed: treat as alive)
)

// STILL_ACTIVE is the exit code for a running process.
const stillActive = 259

// processStart returns pid's creation time and status. If status is statusDead,
// the process verifiably does not exist. If statusUnknown, the process existence
// cannot be determined (e.g., elevated process queried by non-elevated caller),
// so Alive() must treat it as alive (fail closed: cannot steal what cannot be verified).
func processStart(pid int) (time.Time, processStatus) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER (87) means the process does not exist.
		// Any other error (e.g., ERROR_ACCESS_DENIED) means unknown.
		if err == syscall.Errno(87) {
			return time.Time{}, statusDead
		}
		return time.Time{}, statusUnknown
	}
	defer syscall.CloseHandle(h)

	// Check if the process has exited. Even after exit, OpenProcess can succeed
	// if a handle remains open (debugger, Task Manager), keeping the lock stuck.
	// GetExitCodeProcess works under PROCESS_QUERY_LIMITED_INFORMATION.
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		// If we cannot query the exit code, treat as unknown (fail closed).
		return time.Time{}, statusUnknown
	}
	// If the process has exited (exit code is not STILL_ACTIVE), it is dead.
	if exitCode != stillActive {
		return time.Time{}, statusDead
	}

	// Process is alive; fetch its creation time.
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		// If we cannot get process times, treat as unknown (fail closed).
		return time.Time{}, statusUnknown
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), statusAlive
}
