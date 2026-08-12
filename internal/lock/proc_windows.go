package lock

import (
	"syscall"
	"time"
)

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// processStart returns pid's creation time, or ok=false when the process does
// not exist or cannot be queried (which, for lock custody, means "not alive").
func processStart(pid int) (time.Time, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)
	// Exit time is NOT checked: MSDN documents it as undefined while the
	// process runs. An exited process fails OpenProcess once its handles
	// close, and a recycled PID is caught by the creation-time comparison.
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), true
}
