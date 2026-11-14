//go:build linux || darwin

package lock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/awake"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// place is a waiter's place in a line: its ticket in the line's file, which
// names its process, by ID and instance, and which of that process's waiters
// it is.
type place struct {
	path   string
	ticket string
}

// places holds, by lock, the place in the line this process stands at while
// it holds that lock: ReleaseNamed gives it up after the lock's record.
var places = struct {
	sync.Mutex
	held map[string]place
}{held: map[string]place{}}

// tickets counts the tickets this process has taken, so no two of its
// waiters share one.
var tickets atomic.Uint64

// longestLook is the longest a waiter sleeps between two looks at the line,
// so the longest a place can stand free before the next waiter takes it.
const longestLook = 10 * time.Millisecond

// lineUp waits until waiting ends for this process's place at the head of
// the line of waiters at path, and returns it held.
//
// The line is the file itself: one ticket to a line, in the order the
// waiters asked, and the first ticket of a process that still runs is the
// head. A lock the kernel keeps on a file (flock) goes to whichever asker
// runs first once it is free, so a process that lets go and asks again at
// once takes it back past everyone who waited, which is what a waiter that
// only looked for the lock's record every so often lost to on 2026-10-09. So
// the kernel's lock is held only for the moment a ticket is written, read or
// struck, and the order is what the file says. A waiter looks at the line
// again after a millisecond, and after twice as long each next time up to
// longestLook: its place cannot be taken between two looks. A process that
// dies leaves its tickets behind, and the next look strikes them.
func lineUp(path string, waiting context.Context) (place, error) {
	instance, status := readInstance(os.Getpid())
	if status != statusAlive {
		return place{}, fmt.Errorf("lock: this process could not read which run it is to line up at %s", path)
	}
	at := place{path: path, ticket: fmt.Sprintf("%d %s %d", os.Getpid(), instance, tickets.Add(1))}
	// A ticket struck from the line, as every ticket is when the file is
	// removed, is put back at its end on the next look.
	join := func(line []string) []string {
		if slices.Contains(line, at.ticket) {
			return line
		}
		return append(line, at.ticket)
	}
	for pause := time.Millisecond; ; pause = min(2*pause, longestLook) {
		line, err := editLine(path, join)
		if err != nil {
			leaveLine(at)
			return place{}, err
		}
		if line[0] == at.ticket {
			return at, nil
		}
		select {
		case <-waiting.Done():
			leaveLine(at)
			return place{}, errWaitedOut
		case <-time.After(pause):
		}
	}
}

// leaveLine gives up a place in the line, which hands it to the next waiter.
func leaveLine(at place) {
	_, _ = editLine(at.path, func(line []string) []string {
		return slices.DeleteFunc(line, func(ticket string) bool { return ticket == at.ticket })
	})
}

// editLine applies edit to the tickets of running processes in the line at
// path, with the kernel's lock on the file held throughout, and returns the
// line as edit left it.
func editLine(path string, edit func([]string) []string) ([]string, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	// Closing the file gives the kernel's lock up.
	defer file.Close()
	was, line, err := readLine(file, unix.LOCK_EX)
	if err != nil {
		return nil, err
	}
	line = edit(line)
	now := ""
	if len(line) > 0 {
		now = strings.Join(line, "\n") + "\n"
	}
	if now == was {
		return line, nil
	}
	// The new line is written over the old before the file is cut to it, so
	// a writer ended between the two leaves no line without its tickets.
	if _, err := file.WriteAt([]byte(now), 0); err != nil {
		return nil, err
	}
	return line, file.Truncate(int64(len(now)))
}

// readLine takes the kernel's lock on file, shared or exclusive as how says,
// and returns the file's text and the tickets in it whose processes still
// run, each once, in the file's order.
func readLine(file *os.File, how int) (text string, line []string, err error) {
	for {
		err := unix.Flock(int(file.Fd()), how)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EINTR) {
			return "", nil, &os.PathError{Op: "flock", Path: file.Name(), Err: err}
		}
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return "", nil, err
	}
	// Every ticket of one process is its process's to answer for, which is
	// read once.
	runs := map[string]bool{}
	for ticket := range strings.Lines(string(raw)) {
		ticket = strings.TrimSuffix(ticket, "\n")
		fields := strings.Fields(ticket)
		if len(fields) != 3 || slices.Contains(line, ticket) {
			continue
		}
		process := fields[0] + " " + fields[1]
		isRunning, isRead := runs[process]
		if !isRead {
			isRunning = processRuns(fields[0], fields[1])
			runs[process] = isRunning
		}
		if isRunning {
			line = append(line, ticket)
		}
	}
	return string(raw), line, nil
}

// processRuns reports whether the process a ticket names still runs: the
// one with that ID that is that instance, exactly, which no step of the
// clock changes. A process that cannot be read counts as running, as a
// lock's holder does.
func processRuns(id, instance string) bool {
	pid, err := strconv.Atoi(id)
	if err != nil {
		return false
	}
	running, status := readInstance(pid)
	if status != statusAlive {
		return status == statusUnknown
	}
	return running == instance
}

// keepPlace records at as the place this process stands at while it holds
// the lock at key.
func keepPlace(key string, at place) {
	places.Lock()
	places.held[key] = at
	places.Unlock()
}

// leavePlace gives up the place this process stands at for the lock at key,
// if it holds one.
func leavePlace(key string) {
	places.Lock()
	at, isHeld := places.held[key]
	delete(places.held, key)
	places.Unlock()
	if isHeld {
		leaveLine(at)
	}
}

// Show takes the sign at path for this process and returns how to take it
// down: the head of a line of waiters, as a lock's is. Showing it makes no
// new file once the file is there, and a look at it reads past it once the
// process has ended. A sign another process holds for longer than wait is
// that process's, and is an error here.
func Show(path string, wait time.Duration) (hide func(), err error) {
	waiting, cancel := awake.WithTimeout(context.Background(), wait)
	defer cancel()
	at, err := lineUp(path, waiting)
	if err != nil {
		return nil, err
	}
	return func() { leaveLine(at) }, nil
}

// Shown reports whether a process holds the sign at path. It writes
// nothing: a ticket of an ended process stays in the file until the next
// waiter strikes it.
func Shown(path string) bool {
	file, err := fsx.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	_, line, err := readLine(file, unix.LOCK_SH)
	return err == nil && len(line) > 0
}
