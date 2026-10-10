package lock

import (
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// waitersSuffix names the file beside a lock that its waiters line up at.
const waitersSuffix = ".waiters"

// errWaitedOut reports that a waiter's place in the line did not come up
// within its wait.
var errWaitedOut = errors.New("lock: the wait ended in the line of waiters")

// places holds, by lock, the place in the line this process stands at while
// it holds that lock: ReleaseNamed gives it up after the lock's record.
var places = struct {
	sync.Mutex
	held map[string]windows.Handle
}{held: map[string]windows.Handle{}}

// lineUp waits until deadline for this process's place at the head of the
// line of waiters at path, and returns it held.
//
// The line is a lock Windows itself keeps on the file's first byte. It hands
// that lock to those asking in the order they asked, the moment its holder
// lets go, and takes it back from a holder that dies. A waiter that only
// looked for the lock's record again every so often was passed over by every
// process that asked between two of its looks: on 2026-10-09 an
// acknowledgement gave up after its five seconds beside a process that filed
// one notify after another, though the lock was free between every two.
func lineUp(path string, deadline time.Time) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// The file is opened for overlapped use so the wait can end at deadline.
	place, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return 0, &os.PathError{Op: "open", Path: path, Err: err}
	}
	granted, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(place)
		return 0, err
	}
	defer windows.CloseHandle(granted)
	request := newRequest(granted)
	defer runtime.KeepAlive(request)
	err = windows.LockFileEx(place, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, request)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		wait := max(time.Until(deadline), 0)
		if state, _ := windows.WaitForSingleObject(granted, uint32(min(wait.Milliseconds(), int64(windows.INFINITE-1)))); state != windows.WAIT_OBJECT_0 {
			// The request ends cancelled, or granted if the place came up
			// just as the wait ended.
			_ = windows.CancelIoEx(place, request)
		}
		var transferred uint32
		err = windows.GetOverlappedResult(place, request, &transferred, true)
		if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
			err = errWaitedOut
		}
	}
	if err != nil {
		windows.CloseHandle(place)
		return 0, err
	}
	return place, nil
}

// newRequest returns a request Windows completes after the call that makes
// it has returned, and writes to until then. It must not be on a goroutine's
// stack, which moves as it grows, and a function that is never inlined
// returns only what is on the heap.
//
//go:noinline
func newRequest(granted windows.Handle) *windows.Overlapped {
	return &windows.Overlapped{HEvent: granted}
}

// leaveLine gives up a place in the line, which hands it to the next waiter.
func leaveLine(place windows.Handle) {
	_ = windows.UnlockFileEx(place, 0, 1, 0, &windows.Overlapped{})
	_ = windows.CloseHandle(place)
}

// keepPlace records place as the one this process stands at while it holds
// the lock at key.
func keepPlace(key string, place windows.Handle) {
	places.Lock()
	places.held[key] = place
	places.Unlock()
}

// leavePlace gives up the place this process stands at for the lock at key,
// if it holds one.
func leavePlace(key string) {
	places.Lock()
	place, isHeld := places.held[key]
	delete(places.held, key)
	places.Unlock()
	if isHeld {
		leaveLine(place)
	}
}

// Show takes the sign at path for this process and returns how to take it
// down: the lock Windows keeps on the file's first byte, as a line of waiters
// is. Showing it makes no new file once the file is there, so it costs no
// wait for a virus scanner, and Windows takes it down when the process ends.
// A sign another process holds for longer than wait is that process's, and
// is an error here.
func Show(path string, wait time.Duration) (hide func(), err error) {
	place, err := lineUp(path, time.Now().Add(wait))
	if err != nil {
		return nil, err
	}
	return func() { leaveLine(place) }, nil
}

// Shown reports whether a process holds the sign at path.
func Shown(path string) bool {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	file, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(file)
	// A shared lock is refused only while a process holds the sign, and a
	// process asking for the sign waits out the moment this one is held.
	looked := &windows.Overlapped{}
	if err := windows.LockFileEx(file, windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, looked); err != nil {
		return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
	}
	_ = windows.UnlockFileEx(file, 0, 1, 0, looked)
	return false
}
