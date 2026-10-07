package fleettree

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Processes lists every running process from one system process list, which
// needs no handle to any process: its parent, image, creation time, the
// processor time it has used and its private working set.
func Processes() ([]Process, error) {
	buffer := make([]byte, 1<<20)
	for {
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &needed)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, err
		}
		// The list grows between calls as processes start.
		buffer = make([]byte, max(int(needed), 2*len(buffer)))
	}
	var processes []Process
	for offset := uint32(0); ; {
		entry := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[offset]))
		processes = append(processes, Process{
			PID:       int(entry.UniqueProcessID),
			ParentPID: int(entry.InheritedFromUniqueProcessID),
			Exe:       entry.ImageName.String(),
			Created:   entry.CreateTime,
			Started:   filetime(entry.CreateTime),
			CPU:       time.Duration(entry.UserTime+entry.KernelTime) * 100,
			Memory:    uint64(entry.WorkingSetPrivateSize),
		})
		if entry.NextEntryOffset == 0 {
			break
		}
		offset += entry.NextEntryOffset
	}
	return processes, nil
}

// unixEpochFiletime is 1970-01-01 in Windows' units, 100 ns intervals since
// 1601.
const unixEpochFiletime = 116444736000000000

// filetime is the instant a Windows time names.
func filetime(value int64) time.Time {
	return time.Unix(0, (value-unixEpochFiletime)*100).UTC()
}
