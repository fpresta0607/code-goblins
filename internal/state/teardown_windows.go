package state

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func pendingTeardown(processes []TeardownProcess) []TeardownProcess {
	var pending []TeardownProcess
	for _, process := range processes {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if err != nil {
			pending = append(pending, process)
			continue
		}
		var creation, exit, kernel, user windows.Filetime
		err = windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
		windows.CloseHandle(handle)
		if err != nil || time.Unix(0, creation.Nanoseconds()).Equal(process.Started) {
			pending = append(pending, process)
		}
	}
	return pending
}
