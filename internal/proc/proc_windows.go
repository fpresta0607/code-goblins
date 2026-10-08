// Package proc walks Windows process ancestry (parent-of-parent links) via the
// system process list, resolving each hop's creation time to detect PID
// reuse. It is the Windows replacement for upstream's /proc-based harness
// ancestry walk.
package proc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows access right allowing process metadata queries without wider rights.
const processQueryLimitedInformation = 0x1000

// Entry is one hop in a process ancestry chain.
type Entry struct {
	PID       int
	ParentPID int
	ExeBase   string
	Start     time.Time
}

// Self returns the current process's PID.
func Self() int {
	return os.Getpid()
}

// snapshotEntry holds the fields of a process the system process list gives,
// all read at the one moment the list was taken.
type snapshotEntry struct {
	parentPID uint32
	exeBase   string
	start     time.Time
}

// snapshotProcesses returns every running process keyed by PID, from one
// system process list, which needs no handle to any process and carries each
// one's creation time.
func snapshotProcesses() (map[uint32]snapshotEntry, error) {
	buffer := make([]byte, 1<<20)
	for {
		var needed uint32
		err := windows.NtQuerySystemInformation(windows.SystemProcessInformation, unsafe.Pointer(&buffer[0]), uint32(len(buffer)), &needed)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
			return nil, fmt.Errorf("proc: NtQuerySystemInformation: %w", err)
		}
		// The list grows between calls as processes start.
		buffer = make([]byte, max(int(needed), 2*len(buffer)))
	}
	processes := make(map[uint32]snapshotEntry)
	for offset := uint32(0); ; {
		entry := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[offset]))
		created := windows.Filetime{LowDateTime: uint32(entry.CreateTime), HighDateTime: uint32(entry.CreateTime >> 32)}
		processes[uint32(entry.UniqueProcessID)] = snapshotEntry{
			parentPID: uint32(entry.InheritedFromUniqueProcessID),
			exeBase:   entry.ImageName.String(),
			start:     time.Unix(0, created.Nanoseconds()).UTC(),
		}
		if entry.NextEntryOffset == 0 {
			break
		}
		offset += entry.NextEntryOffset
	}
	return processes, nil
}

// Processes lists every running process from one system process list, with
// its parent, executable and start, the creation time Identify proves it by.
// No process is opened, so the list costs the same however many there are.
func Processes() ([]Entry, error) {
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(processes))
	for pid, process := range processes {
		entries = append(entries, Entry{PID: int(pid), ParentPID: int(process.parentPID), ExeBase: process.exeBase, Start: process.start})
	}
	return entries, nil
}

// Ancestry walks the parent chain starting at pid (included as the first
// entry) up to maxHops entries. The walk stops early when a pid is missing
// from the snapshot, its ParentPID is 0, or the parent's creation time is
// after the child's (treated as PID reuse breaking the chain's integrity).
func Ancestry(pid int, maxHops int) ([]Entry, error) {
	processes, err := snapshotProcesses()
	if err != nil {
		return nil, err
	}

	var entries []Entry
	currentPID := uint32(pid)
	var childStart time.Time

	for hop := 0; hop < maxHops; hop++ {
		snap, ok := processes[currentPID]
		if !ok {
			break
		}
		start, ok := StartTime(int(currentPID))
		if !ok {
			break
		}
		if hop > 0 && start.After(childStart) {
			// Parent created after child: PID reuse, chain integrity broken.
			break
		}
		entries = append(entries, Entry{
			PID:       int(currentPID),
			ParentPID: int(snap.parentPID),
			ExeBase:   snap.exeBase,
			Start:     start,
		})
		if snap.parentPID == 0 {
			break
		}
		childStart = start
		currentPID = snap.parentPID
	}
	return entries, nil
}

// FindAncestor returns the first entry (including self) in pid's ancestry,
// walked up to maxHops, whose lowercased ExeBase with a trailing .exe
// stripped equals any of names.
func FindAncestor(pid int, maxHops int, names ...string) (Entry, bool) {
	entries, err := Ancestry(pid, maxHops)
	if err != nil {
		return Entry{}, false
	}
	for _, entry := range entries {
		base := baseNoExe(entry.ExeBase)
		for _, name := range names {
			if base == baseNoExe(name) {
				return entry, true
			}
		}
	}
	return Entry{}, false
}

// baseNoExe lowercases name and strips a trailing ".exe", if present.
func baseNoExe(name string) string {
	lower := strings.ToLower(name)
	lower = lower[strings.LastIndexAny(lower, `\/`)+1:]
	return strings.TrimSuffix(lower, ".exe")
}

// CPUTime returns the total processor time pid has consumed since it started,
// kernel plus user, and whether the process could be opened and measured.
//
// It exists for the one question log age cannot answer: whether a process
// that looks quiet is actually doing nothing. A goblin waiting on an API
// response writes no logs and moves no pane text for minutes, yet a goblin
// mid-turn burns CPU continuously. Two of these readings a few seconds apart
// separate the two, which is why nothing may be killed on log age alone.
func CPUTime(pid int) (time.Duration, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, false
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	// Kernel and user time are durations counted in 100-nanosecond
	// intervals. Filetime.Nanoseconds reads a date and subtracts the 1601
	// epoch, which turns a duration into a large negative number.
	return filetimeDuration(kernel) + filetimeDuration(user), true
}

func filetimeDuration(ft syscall.Filetime) time.Duration {
	return time.Duration(int64(ft.HighDateTime)<<32|int64(ft.LowDateTime)) * 100
}

// StartTime returns pid's creation time, and whether the process was
// found and its times could be resolved. Mirrors internal/lock's
// OpenProcess + GetProcessTimes technique.
func StartTime(pid int) (time.Time, bool) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return time.Time{}, false
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, creation.Nanoseconds()).UTC(), true
}
