package proc

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// zombie is the state sys/proc.h names SZOMB: the process has exited and
// only waits for its parent to collect it.
const zombie = 5

// readProcess reads what the kernel keeps of pid (kern.proc.pid). A process
// that is gone, or that has exited and only waits for its parent to collect
// it, is fs.ErrNotExist.
func readProcess(pid int) (unix.KinfoProc, error) {
	read, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		return unix.KinfoProc{}, fmt.Errorf("proc: read process %d: %v", pid, err)
	}
	if len(read) == 0 {
		return unix.KinfoProc{}, fmt.Errorf("proc: read process %d: %w", pid, fs.ErrNotExist)
	}
	if read[0].Proc.P_stat == zombie {
		return unix.KinfoProc{}, fmt.Errorf("proc: process %d has exited: %w", pid, fs.ErrNotExist)
	}
	return read[0], nil
}

// entryOf is the entry of a process as the kernel keeps it. Its start is
// the time the kernel stamped it with as it was made, which no later step of
// the clock changes, so every reading of one process gives the same start.
func entryOf(process unix.KinfoProc) Entry {
	started := process.Proc.P_starttime
	return Entry{
		PID:       int(process.Proc.P_pid),
		ParentPID: int(process.Eproc.Ppid),
		ExeBase:   unix.ByteSliceToString(process.Proc.P_comm[:]),
		Start:     time.Unix(started.Sec, int64(started.Usec)*int64(time.Microsecond)).UTC(),
	}
}

// Instance names the one run of a program that has pid now, as
// "<boot>/<ticks>": the UUID of this start of the machine
// (kern.bootsessionuuid), and when the process started in the ticks of the
// machine's absolute clock, which counts from that start and is never
// stepped. Neither is worked out from the wall clock, so every reading of
// one process gives the same instance,
// and a process that took the ID later, on this start of the machine or
// another, gives a different one. A process that is gone or has exited is
// fs.ErrNotExist, and any other error says only that the process could not
// be read, never that it is gone.
func Instance(pid int) (string, error) {
	if _, err := readProcess(pid); err != nil {
		return "", err
	}
	ticks, err := startTicks(pid)
	if err != nil {
		return "", err
	}
	// A boot session that cannot be read says nothing of the process, so its
	// error is never fs.ErrNotExist.
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", fmt.Errorf("proc: read the machine's boot session: %v", err)
	}
	if boot == "" {
		return "", errors.New("proc: the machine's boot session is empty")
	}
	return boot + "/" + strconv.FormatUint(ticks, 10), nil
}

// Lookup reads one running process: its parent, its name, which the kernel
// cuts to 16 bytes, and its start. A process that is gone or has exited is
// fs.ErrNotExist, and any other error says only that the process could not
// be read, never that it is gone.
func Lookup(pid int) (Entry, error) {
	process, err := readProcess(pid)
	if err != nil {
		return Entry{}, err
	}
	return entryOf(process), nil
}

// Processes lists every running process, with its parent, its name and its
// start, which Identify proves it by.
func Processes() ([]Entry, error) {
	listed, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("proc: list processes: %w", err)
	}
	entries := make([]Entry, 0, len(listed))
	for _, process := range listed {
		if process.Proc.P_stat != zombie {
			entries = append(entries, entryOf(process))
		}
	}
	return entries, nil
}

// Ancestry walks the parent chain starting at pid (included as the first
// entry) up to maxHops entries. The walk stops early when a process is gone,
// has no parent, or its parent started after it did, which a parent never
// does: the kernel gave an ended parent's ID to another process.
func Ancestry(pid int, maxHops int) ([]Entry, error) {
	var entries []Entry
	for hop := 0; hop < maxHops; hop++ {
		entry, err := Lookup(pid)
		if err != nil {
			break
		}
		if hop > 0 && entry.Start.After(entries[hop-1].Start) {
			break
		}
		entries = append(entries, entry)
		if entry.ParentPID == 0 {
			break
		}
		pid = entry.ParentPID
	}
	return entries, nil
}
