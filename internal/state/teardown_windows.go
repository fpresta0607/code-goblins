package state

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

func pendingTeardown(processes []TeardownProcess) []TeardownProcess {
	var pending []TeardownProcess
	for _, process := range processes {
		handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(process.PID))
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			continue
		}
		if err != nil {
			pending = append(pending, process)
			continue
		}
		var creation, exit, kernel, user windows.Filetime
		err = windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user)
		isPending := err != nil || time.Unix(0, creation.Nanoseconds()).Equal(process.Started)
		if err == nil && isPending {
			wait, waitErr := windows.WaitForSingleObject(handle, 0)
			isPending = waitErr != nil || wait != uint32(windows.WAIT_OBJECT_0)
		}
		windows.CloseHandle(handle)
		if isPending {
			pending = append(pending, process)
		}
	}
	return pending
}
