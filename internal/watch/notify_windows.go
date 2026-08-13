package watch

import (
	"syscall"
	"time"
)

// dirWaiterFailMax is Task 9's EventCapFailMax: the number of CONSECUTIVE
// Wait failures that permanently degrades a waiter to timer-only waiting
// for the rest of its life. Fixed for this task rather than read from
// CFO_EVENT_CAP_FAIL_MAX; NewDirWaiter's signature carries no override
// parameter, so wiring the env knob is left for whichever later task needs
// it configurable.
const dirWaiterFailMax = 3

var (
	modKernel32      = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW = modKernel32.NewProc("CreateEventW")
	procResetEvent   = modKernel32.NewProc("ResetEvent")
)

// DirWaiter waits for a change notification on a directory via
// ReadDirectoryChangesW, so watch.Run can wake as soon as a status file
// changes instead of sleeping out the full poll interval. At most one read
// is ever in flight: Wait issues one, waits on the completion event bounded
// by its timeout, and always resolves that same read - consumed on success,
// canceled and drained on timeout - before returning, so the OVERLAPPED and
// the 4KB buffer behind it are safe to reuse on the next call. A run of
// dirWaiterFailMax consecutive API failures trips a one-way breaker: every
// later Wait call, including after Close, degrades to time.Sleep(timeout)
// without touching the API again, which is how this waiter survives AV
// filter drivers and network paths that silently kill directory watches.
// All exported methods are meant to be called from the single goroutine
// that runs watch.Run's loop; nothing here is safe for concurrent use.
type DirWaiter struct {
	dirHandle   syscall.Handle
	eventHandle syscall.Handle
	ov          syscall.Overlapped
	buf         [4096]byte

	pending    bool // a ReadDirectoryChangesW is outstanding on ov/buf
	closed     bool
	degraded   bool
	failStreak int
}

// NewDirWaiter opens dir for FILE_LIST_DIRECTORY overlapped access and
// creates the manual-reset event Wait waits on for ReadDirectoryChangesW
// completions.
func NewDirWaiter(dir string) (*DirWaiter, error) {
	pathPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return nil, err
	}
	dirHandle, err := syscall.CreateFile(
		pathPtr,
		syscall.FILE_LIST_DIRECTORY,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, err
	}

	eventHandle, err := createEvent()
	if err != nil {
		syscall.CloseHandle(dirHandle)
		return nil, err
	}

	return &DirWaiter{dirHandle: dirHandle, eventHandle: eventHandle}, nil
}

// Wait blocks for up to timeout for a directory change and reports whether
// one arrived. Its bool return has two halves, both load-bearing to
// watch.Run's contract on Config.WaitEvent: true is returned ONLY when
// ReadDirectoryChangesW actually completed with a signaled event within
// timeout, never fabricated by any other path. Every other path - a closed
// or degraded waiter, an API failure, or an ordinary timeout - returns
// false, and Run's own Poll-floor enforcement covers the rest.
func (w *DirWaiter) Wait(timeout time.Duration) bool {
	// Degraded is checked before closed: once tripped it stays true for the
	// waiter's remaining lifetime, including after Close, so every later
	// call sleeps out the timeout exactly like pure timer mode rather than
	// falling through to the closed-waiter strike path below.
	if w.degraded {
		time.Sleep(timeout)
		return false
	}
	if w.closed {
		// A closed waiter has nothing to wait on: both handles are already
		// InvalidHandle. This still scores a strike, so calling Wait after
		// Close still trips the breaker after dirWaiterFailMax calls
		// instead of returning false forever without ever degrading.
		w.recordFailure()
		return false
	}

	if err := resetEvent(w.eventHandle); err != nil {
		w.recordFailure()
		return false
	}
	w.ov = syscall.Overlapped{HEvent: w.eventHandle}
	var bytesReturned uint32
	if err := syscall.ReadDirectoryChanges(
		w.dirHandle, &w.buf[0], uint32(len(w.buf)), false,
		syscall.FILE_NOTIFY_CHANGE_FILE_NAME|syscall.FILE_NOTIFY_CHANGE_LAST_WRITE|syscall.FILE_NOTIFY_CHANGE_SIZE,
		&bytesReturned, &w.ov, 0,
	); err != nil {
		w.recordFailure()
		return false
	}
	w.pending = true

	event, err := syscall.WaitForSingleObject(w.eventHandle, uint32(timeout/time.Millisecond))
	if err != nil {
		w.recordFailure()
		w.cancelAndDrain()
		return false
	}
	if event == syscall.WAIT_OBJECT_0 {
		w.pending = false
		w.failStreak = 0
		return true
	}

	// WAIT_TIMEOUT (or any other non-error code funneled the same way):
	// give up waiting and reclaim the outstanding read before returning.
	// This is the normal quiet path, not a failure - scoring a strike here
	// would trip the breaker after three idle poll cycles and drop every
	// home to timer polling within a minute of intermittent quiet.
	w.cancelAndDrain()
	return false
}

// cancelAndDrain cancels the outstanding read (CancelIoEx, not CancelIo:
// CancelIo only cancels I/O issued by the calling thread, and Go goroutines
// migrate threads across syscalls, so it would be a silent no-op here) and
// then waits on the completion event so the kernel is done writing into ov
// and buf before this call returns. It is a no-op when nothing is pending,
// which is what keeps Close safe on a waiter whose Wait was never called:
// without the guard, canceling I/O that was never issued and then waiting
// on an event nothing will ever signal would hang forever.
func (w *DirWaiter) cancelAndDrain() {
	if !w.pending {
		return
	}
	syscall.CancelIoEx(w.dirHandle, &w.ov)
	syscall.WaitForSingleObject(w.eventHandle, syscall.INFINITE)
	w.pending = false
}

func (w *DirWaiter) recordFailure() {
	w.failStreak++
	if w.failStreak >= dirWaiterFailMax {
		w.degraded = true
	}
}

// Degraded reports whether the breaker has tripped. Once true it never
// resets: a fresh success can only reset the CONSECUTIVE-failure counter
// that feeds it, never the degraded state itself.
func (w *DirWaiter) Degraded() bool {
	return w.degraded
}

// Close cancels any outstanding read, closes both handles, and marks the
// waiter closed. It is idempotent (a second Close is a no-op) and safe on a
// waiter that never had Wait called on it, because cancelAndDrain's pending
// guard skips both the cancel and the drain wait in that case.
func (w *DirWaiter) Close() {
	if w.closed {
		return
	}
	w.cancelAndDrain()
	syscall.CloseHandle(w.dirHandle)
	syscall.CloseHandle(w.eventHandle)
	w.dirHandle = syscall.InvalidHandle
	w.eventHandle = syscall.InvalidHandle
	w.closed = true
}

// createEvent and resetEvent go through NewLazyDLL because the stdlib
// syscall package exports no Windows event APIs (CreateEvent, ResetEvent),
// and golang.org/x/sys, which does, is banned for this project.
func createEvent() (syscall.Handle, error) {
	r1, _, e1 := procCreateEventW.Call(0, 1, 0, 0) // manual-reset, initially non-signaled, unnamed
	if r1 == 0 {
		return 0, e1
	}
	return syscall.Handle(r1), nil
}

func resetEvent(h syscall.Handle) error {
	r1, _, e1 := procResetEvent.Call(uintptr(h))
	if r1 == 0 {
		return e1
	}
	return nil
}
