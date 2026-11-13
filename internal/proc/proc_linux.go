package proc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ticksPerSecond is how many of the ticks /proc counts a process's start in
// make a second: the kernel's USER_HZ, which is 100 on every architecture Go
// builds for, whatever rate the kernel's own timer runs at.
const ticksPerSecond = 100

// status is what /proc/<pid>/stat says of a process: its name, which the
// kernel cuts to 15 bytes, its state, its parent and how many ticks after
// the machine started it was created.
type status struct {
	name    string
	state   byte
	parent  int
	started uint64
}

// readStatus reads pid's line of /proc. A process that is gone, or that has
// exited and only waits for its parent to collect it, is fs.ErrNotExist.
func readStatus(pid int) (status, error) {
	line, err := fsx.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		// A process that exits between the open and the read fails the read
		// with ESRCH.
		if errors.Is(err, syscall.ESRCH) {
			err = fs.ErrNotExist
		}
		return status{}, fmt.Errorf("proc: read process %d: %w", pid, err)
	}
	read, err := parseStatus(line)
	if err != nil {
		return status{}, fmt.Errorf("proc: read process %d: %w", pid, err)
	}
	if read.state == 'Z' || read.state == 'X' {
		return status{}, fmt.Errorf("proc: process %d has exited: %w", pid, fs.ErrNotExist)
	}
	return read, nil
}

// parseStatus reads a line of /proc/<pid>/stat. The name stands in brackets
// and may itself hold spaces and brackets, so the fields after it are counted
// from the last closing bracket: the state is the first, the parent the
// second and the start the twentieth.
func parseStatus(line []byte) (status, error) {
	open, shut := bytes.IndexByte(line, '('), bytes.LastIndexByte(line, ')')
	if open < 0 || shut < open {
		return status{}, fmt.Errorf("its line %q names no program", line)
	}
	fields := bytes.Fields(line[shut+1:])
	if len(fields) < 20 || len(fields[0]) != 1 {
		return status{}, fmt.Errorf("its line %q is cut short", line)
	}
	parent, err := strconv.Atoi(string(fields[1]))
	if err != nil {
		return status{}, fmt.Errorf("its parent %q is no number", fields[1])
	}
	started, err := strconv.ParseUint(string(fields[19]), 10, 64)
	if err != nil {
		return status{}, fmt.Errorf("its start %q is no number", fields[19])
	}
	return status{name: string(line[open+1 : shut]), state: fields[0][0], parent: parent, started: started}, nil
}

// bootTime is when the machine started, as /proc/stat gives it in whole
// seconds. Every process's start is counted from it, so two readings of one
// process agree to the tick. The kernel works it out from the clock as it
// stands, so a step of the clock moves it, and with it the start of every
// process read after the step: a start recorded before a step of a second or
// more no longer matches its process, which then reads as another program.
func bootTime() (time.Time, error) {
	stat, err := fsx.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("proc: read the machine's start: %w", err)
	}
	for line := range bytes.Lines(stat) {
		if value, isBoot := bytes.CutPrefix(line, []byte("btime ")); isBoot {
			seconds, err := strconv.ParseInt(string(bytes.TrimSpace(value)), 10, 64)
			if err != nil {
				return time.Time{}, fmt.Errorf("proc: the machine's start %q is no number", value)
			}
			return time.Unix(seconds, 0).UTC(), nil
		}
	}
	return time.Time{}, errors.New("proc: /proc/stat names no start of the machine")
}

// entry is pid as read, started that long after boot.
func (s status) entry(pid int, boot time.Time) Entry {
	return Entry{PID: pid, ParentPID: s.parent, ExeBase: s.name, Start: boot.Add(time.Duration(s.started) * (time.Second / ticksPerSecond))}
}

// Lookup reads one running process from /proc: its parent, its name and its
// start. A process that is gone or has exited is fs.ErrNotExist, and any
// other error says only that the process could not be read, never that it is
// gone.
func Lookup(pid int) (Entry, error) {
	read, err := readStatus(pid)
	if err != nil {
		return Entry{}, err
	}
	boot, err := bootTime()
	if err != nil {
		return Entry{}, err
	}
	return read.entry(pid, boot), nil
}

// Processes lists every running process from /proc, with its parent, its
// name and its start, which Identify proves it by.
func Processes() ([]Entry, error) {
	boot, err := bootTime()
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("proc: list processes: %w", err)
	}
	entries := make([]Entry, 0, len(names))
	for _, name := range names {
		pid, err := strconv.Atoi(name.Name())
		if err != nil {
			continue
		}
		read, err := readStatus(pid)
		if errors.Is(err, fs.ErrNotExist) {
			// It ended while the list was read.
			continue
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, read.entry(pid, boot))
	}
	return entries, nil
}

// Ancestry walks the parent chain starting at pid (included as the first
// entry) up to maxHops entries. The walk stops early when a process is gone,
// has no parent, or its parent started after it did, which a parent never
// does: the kernel gave an ended parent's ID to another process.
func Ancestry(pid int, maxHops int) ([]Entry, error) {
	boot, err := bootTime()
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for hop := 0; hop < maxHops; hop++ {
		read, err := readStatus(pid)
		if err != nil {
			break
		}
		entry := read.entry(pid, boot)
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

// StartTime returns pid's start, and whether the process was found running.
func StartTime(pid int) (time.Time, bool) {
	entry, err := Lookup(pid)
	return entry.Start, err == nil
}
