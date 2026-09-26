package host

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Running reports whether pid may still run: only a process Windows shows as
// ended, or as never started, does not.
func Running(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == stillActive
}

// stillActive is the exit code Windows reports for a process that runs.
const stillActive = 259
