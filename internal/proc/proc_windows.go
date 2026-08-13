// Package proc walks Windows process ancestry (parent-of-parent links) via the
// Toolhelp32 snapshot API, resolving each hop's creation time to detect PID
// reuse. It is the Windows replacement for upstream's /proc-based harness
// ancestry walk.
package proc

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
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

// snapshotEntry holds the fields of a process needed for ancestry walking,
// captured from a single Toolhelp32 snapshot.
type snapshotEntry struct {
	parentPID uint32
	exeBase   string
}

// snapshotProcesses returns every running process keyed by PID, taken from a
// single CreateToolhelp32Snapshot call.
func snapshotProcesses() (map[uint32]snapshotEntry, error) {
	handle, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("proc: CreateToolhelp32Snapshot: %w", err)
	}
	defer syscall.CloseHandle(handle)

	processes := make(map[uint32]snapshotEntry)
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	err = syscall.Process32First(handle, &entry)
	for err == nil {
		processes[entry.ProcessID] = snapshotEntry{
			parentPID: entry.ParentProcessID,
			exeBase:   syscall.UTF16ToString(entry.ExeFile[:]),
		}
		err = syscall.Process32Next(handle, &entry)
	}
	return processes, nil
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
		start, ok := processStart(int(currentPID))
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

// processStart returns pid's creation time, and whether the process was
// found and its times could be resolved. Mirrors internal/lock's
// OpenProcess + GetProcessTimes technique.
func processStart(pid int) (time.Time, bool) {
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
