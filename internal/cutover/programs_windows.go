//go:build windows

package cutover

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// RunningPrograms are the programs running on this machine, each with the
// image path Windows reports for it. A process this user cannot open, a
// system or another user's, is left out, since it runs nothing from a home.
type RunningPrograms struct{}

func (RunningPrograms) Running() ([]Process, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var running []Process
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if image, isOpened := imagePath(entry.ProcessID); isOpened {
			running = append(running, Process{PID: int(entry.ProcessID), Image: image})
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return running, nil
}

func imagePath(pid uint32) (string, bool) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer windows.CloseHandle(process)
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", false
	}
	return windows.UTF16ToString(buffer[:size]), true
}
