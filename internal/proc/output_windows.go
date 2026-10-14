package proc

import (
	"errors"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// OutputFile names the disk file a process's stdout writes, without reading
// its handle or consuming a pipe. The start time prevents following a reused pid.
func OutputFile(pid int, started time.Time) (string, error) {
	handle, err := syscall.OpenProcess(processQueryInformation|processVMRead|windows.PROCESS_DUP_HANDLE, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", err
	}
	if !time.Unix(0, creation.Nanoseconds()).Equal(started) {
		return "", errors.New("the output process has changed")
	}
	var path string
	err = walkSteady(handle, pid, func() (uintptr, error) {
		path = ""
		parameters, err := parameterBlock(handle, pid)
		if err != nil {
			return 0, err
		}
		output, err := readPointer(handle, parameters+unsafe.Offsetof(windows.RTL_USER_PROCESS_PARAMETERS{}.StandardOutput))
		if err != nil {
			return parameters, err
		}
		var copied windows.Handle
		if err := windows.DuplicateHandle(windows.Handle(handle), windows.Handle(output), windows.CurrentProcess(), &copied, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return parameters, err
		}
		defer windows.CloseHandle(copied)
		kind, err := windows.GetFileType(copied)
		if err != nil || kind != windows.FILE_TYPE_DISK {
			return parameters, err
		}
		buffer := make([]uint16, 32768)
		length, err := windows.GetFinalPathNameByHandle(copied, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return parameters, err
		}
		if length >= uint32(len(buffer)) {
			return parameters, errors.New("the output path is too long")
		}
		path = windows.UTF16ToString(buffer[:length])
		if strings.HasPrefix(path, `\\?\UNC\`) {
			path = ""
		} else {
			path = strings.TrimPrefix(path, `\\?\`)
		}
		return parameters, nil
	})
	return path, err
}
