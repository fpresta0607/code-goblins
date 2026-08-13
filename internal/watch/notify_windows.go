package watch

import (
	"encoding/binary"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/fpresta0607/code-goblins/internal/claudehook"
)

const (
	// dirWaiterFailMaxDefault is EventCapFailMax's default: the number of
	// CONSECUTIVE Wait failures that permanently degrades a waiter to
	// timer-only waiting for the rest of its life. CFO_EVENT_CAP_FAIL_MAX
	// overrides it, clamped to [1, 10].
	dirWaiterFailMaxDefault = 3

	// drainTimeoutMillis bounds cancelAndDrain's wait for a canceled read's
	// completion. See cancelAndDrain's doc for what happens when a filter
	// driver stalls the cancel past this bound.
	drainTimeoutMillis uint32 = 5000

	// maxWaitMillis is one below syscall.INFINITE (0xFFFFFFFF), so a
	// requested wait whose millisecond count would otherwise reach or wrap
	// past that sentinel is clamped instead of silently becoming unbounded.
	maxWaitMillis = 1<<32 - 2
)

var (
	modKernel32      = syscall.NewLazyDLL("kernel32.dll")
	procCreateEventW = modKernel32.NewProc("CreateEventW")
	procResetEvent   = modKernel32.NewProc("ResetEvent")

	leakedWaitersMu sync.Mutex
	// leakedWaiters pins every DirWaiter whose cancelAndDrain could not
	// confirm a canceled read's completion within drainTimeoutMillis. The
	// kernel may still write into that waiter's OVERLAPPED and 4KB buffer at
	// any time afterward, so the waiter must never become unreachable to the
	// garbage collector: freeing or reusing that memory while the kernel
	// still holds a pointer into it is a use-after-free. This is a
	// deliberate, bounded leak of one *DirWaiter per process that ever hits
	// this path, and it is strictly safer than closing handles the kernel
	// might still write through, or wedging state/.watch.lock forever.
	leakedWaiters []*DirWaiter
)

// DirWaiter waits for a change notification on a directory via
// ReadDirectoryChangesW, so watch.Run can wake as soon as a status file
// changes instead of sleeping out the full poll interval. At most one read
// is ever in flight: Wait issues one, waits on the completion event bounded
// by its timeout, and always resolves that same read - consumed, filtered,
// or canceled-and-drained - before returning (the one exception is the
// permanent-leak path documented on cancelAndDrain), so the OVERLAPPED and
// the 4KB buffer behind it are safe to reuse on the next call. A run of
// failMax consecutive API failures trips a one-way breaker: every later Wait
// call, including after Close, degrades to time.Sleep(timeout) without
// touching the API again, which is how this waiter survives AV filter
// drivers and network paths that silently kill directory watches. All
// exported methods are meant to be called from the single goroutine that
// runs watch.Run's loop; nothing here is safe for concurrent use.
type DirWaiter struct {
	dirHandle   syscall.Handle
	eventHandle syscall.Handle
	ov          syscall.Overlapped
	buf         [4096]byte

	failMax int

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

	return &DirWaiter{
		dirHandle:   dirHandle,
		eventHandle: eventHandle,
		failMax:     claudehook.Int("CFO_EVENT_CAP_FAIL_MAX", dirWaiterFailMaxDefault, 1, 10),
	}, nil
}

// Wait blocks for up to timeout for a directory change and reports whether
// one arrived. Its bool return has two halves, both load-bearing to
// watch.Run's contract on Config.WaitEvent: true is returned ONLY when a
// completed ReadDirectoryChangesW named at least one *.status or
// *.turn-ended file, or lost its content to a buffer overflow (in which case
// a real change is assumed since there is no way to say otherwise). Every
// other path - a closed or degraded waiter, an API failure, an ordinary
// timeout, or a completed read that named only bookkeeping files - returns
// false, but the bookkeeping-only case reissues against the SAME deadline
// instead of returning immediately: see the loop below.
//
// The bookkeeping-only case is not hypothetical. watch.Run touches
// state/.last-watcher-beat at the top of every iteration, on this exact
// directory, under this exact mask; the kernel buffers that touch on the
// handle and hands it back on the very next read, often before any real
// status write has happened. Returning true for that self-inflicted
// notification would make Run's own bookkeeping wake Run immediately, every
// iteration, forever: a free-running loop burning a full CPU core for as
// long as the host process runs. Filtering to the two suffixes ScanSignals
// actually acts on (an allowlist, not a denylist of today's known
// bookkeeping files) is what keeps this waiter from waking on its own noise
// without rotting the moment a later task adds a new bookkeeping filename.
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
		// Close still trips the breaker after failMax calls instead of
		// returning false forever without ever degrading.
		w.recordFailure()
		return false
	}

	// deadline is computed once, here, for the whole call. A completed read
	// that names only bookkeeping files is reissued against what remains of
	// THIS budget rather than returning false, so a real status write later
	// in the same interval is still caught promptly instead of costing up
	// to a full Poll of latency.
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}

		if err := resetEvent(w.eventHandle); err != nil {
			w.recordFailure()
			return false
		}
		w.ov = syscall.Overlapped{HEvent: w.eventHandle}
		var unused uint32
		if err := syscall.ReadDirectoryChanges(
			w.dirHandle, &w.buf[0], uint32(len(w.buf)), false,
			syscall.FILE_NOTIFY_CHANGE_FILE_NAME|syscall.FILE_NOTIFY_CHANGE_LAST_WRITE|syscall.FILE_NOTIFY_CHANGE_SIZE,
			&unused, &w.ov, 0,
		); err != nil {
			// A failed call means nothing was queued: the kernel holds no
			// reference to w.ov or w.buf, so there is nothing for
			// w.pending to track and no cancel/drain is needed here.
			w.recordFailure()
			return false
		}
		w.pending = true

		event, err := syscall.WaitForSingleObject(w.eventHandle, clampWaitMillis(remaining))
		if err != nil {
			w.recordFailure()
			w.cancelAndDrain()
			return false
		}
		if event != syscall.WAIT_OBJECT_0 {
			// Ordinary timeout: not a failure. Reclaim the outstanding read;
			// the deadline check at the top of the next pass returns false
			// once the budget is actually exhausted.
			w.cancelAndDrain()
			if w.degraded {
				// cancelAndDrain could not confirm the cancel landed within
				// its own bound and has permanently degraded this waiter;
				// touching the API again would race the kernel over ov/buf.
				return false
			}
			continue
		}

		w.pending = false
		w.failStreak = 0
		if w.matchesStatusFile() {
			return true
		}
		// A real change completed the read, but every record named a
		// bookkeeping file (most commonly this process's own beat touch):
		// loop again for whatever budget remains instead of returning
		// false.
	}
}

// matchesStatusFile reports whether the read that just completed into w.ov
// and w.buf should wake watch.Run: either its content was lost (a
// non-success completion, or a reported byte count outside [1, len(buf)],
// both treated as "a change definitely occurred but we cannot say what"), or
// at least one FILE_NOTIFY_INFORMATION record in the buffer names a
// *.status or *.turn-ended file.
//
// The byte count comes from w.ov.InternalHigh, not ReadDirectoryChanges's
// own out-parameter: for an overlapped call the kernel never fills that
// out-parameter, only the OVERLAPPED structure's own Internal (completion
// status) and InternalHigh (bytes transferred) fields, which is exactly what
// GetOverlappedResult reads internally. GetOverlappedResult itself is not
// exported by stdlib syscall, so this reads those two fields directly; that
// is a well-established use of OVERLAPPED once the completion event has
// actually signaled.
func (w *DirWaiter) matchesStatusFile() bool {
	if w.ov.Internal != 0 {
		// A non-success completion, most notably STATUS_NOTIFY_ENUM_DIR
		// (the change buffer overflowed; GetOverlappedResult would report
		// this as ERROR_NOTIFY_ENUM_DIR). Content is lost, so assume a
		// change occurred.
		return true
	}
	n := int(w.ov.InternalHigh)
	if n <= 0 || n > len(w.buf) {
		// Zero-byte completion, or a reported count this waiter cannot
		// trust the offsets of: content lost, assume a change occurred
		// rather than read out of bounds.
		return true
	}
	return bufferNamesStatusFile(w.buf[:n])
}

// bufferNamesStatusFile walks the FILE_NOTIFY_INFORMATION records packed
// into data (NextEntryOffset uint32, Action uint32, FileNameLength uint32 in
// BYTES over UTF-16, then the name itself, with NextEntryOffset 0 marking
// the last record) and reports whether any of them names a file ending in
// .status or .turn-ended, matched case-insensitively: exactly the two
// suffixes ScanSignals acts on.
func bufferNamesStatusFile(data []byte) bool {
	for offset := 0; offset+12 <= len(data); {
		nextEntry := binary.LittleEndian.Uint32(data[offset:])
		nameLen := int(binary.LittleEndian.Uint32(data[offset+8:]))
		nameStart := offset + 12
		nameEnd := nameStart + nameLen
		if nameEnd > len(data) {
			// A malformed or truncated record: cannot trust the rest of
			// this buffer either, so stop scanning it.
			break
		}
		if hasStatusSuffix(decodeUTF16LE(data[nameStart:nameEnd])) {
			return true
		}
		if nextEntry == 0 {
			break
		}
		offset += int(nextEntry)
	}
	return false
}

func hasStatusSuffix(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".status") || strings.HasSuffix(lower, ".turn-ended")
}

func decodeUTF16LE(b []byte) string {
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(u16))
}

// cancelAndDrain cancels the outstanding read (CancelIoEx, not CancelIo:
// CancelIo only cancels I/O issued by the calling thread, and Go goroutines
// migrate threads across syscalls, so it would be a silent no-op here) and
// waits up to drainTimeoutMillis for the completion to land, so the kernel
// is confirmed done writing into ov and buf before this call returns
// normally.
//
// It is a no-op when nothing is pending, which is what keeps Close safe on
// a waiter whose Wait was never called: without the guard, canceling I/O
// that was never issued and then waiting on an event nothing will ever
// signal would hang forever.
//
// If the drain cannot confirm completion within the bound, this is no
// longer an ordinary timeout: a filter driver stalling the cancel is the
// exact scenario this waiter's breaker exists to survive (Windows directory
// watches silently die under AV filter drivers), and CancelIoEx only queues
// the cancel request with no completion bound of its own, so the kernel may
// still hold a pointer into ov/buf indefinitely. This method therefore
// permanently degrades the waiter, deliberately LEAVES pending true so no
// later call reassigns ov or reissues a read against memory the kernel might
// still touch, never closes the handles, and pins w in leakedWaiters so the
// GC cannot free that memory out from under a write that never lands.
func (w *DirWaiter) cancelAndDrain() {
	if !w.pending {
		return
	}
	syscall.CancelIoEx(w.dirHandle, &w.ov)
	event, err := syscall.WaitForSingleObject(w.eventHandle, drainTimeoutMillis)
	if err != nil || event != syscall.WAIT_OBJECT_0 {
		w.degraded = true
		leakedWaitersMu.Lock()
		leakedWaiters = append(leakedWaiters, w)
		leakedWaitersMu.Unlock()
		return
	}
	w.pending = false
}

func (w *DirWaiter) recordFailure() {
	w.failStreak++
	if w.failStreak >= w.failMax {
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
//
// If cancelAndDrain could not confirm the cancel landed (see its doc),
// pending is still true when it returns here: Close marks itself closed
// anyway, so a second call is still a no-op, but deliberately leaves the
// handles open rather than closing them out from under a kernel write that
// was never confirmed finished.
func (w *DirWaiter) Close() {
	if w.closed {
		return
	}
	w.cancelAndDrain()
	if w.pending {
		w.closed = true
		return
	}
	syscall.CloseHandle(w.dirHandle)
	syscall.CloseHandle(w.eventHandle)
	w.dirHandle = syscall.InvalidHandle
	w.eventHandle = syscall.InvalidHandle
	w.closed = true
}

// clampWaitMillis converts d to the millisecond count WaitForSingleObject
// expects, clamped to [1, maxWaitMillis]: a sub-millisecond positive
// duration would otherwise truncate to a busy 0ms poll, and a duration whose
// millisecond count meets or exceeds 2^32-1 would either overflow uint32 or
// land exactly on syscall.INFINITE (0xFFFFFFFF), turning a bounded wait into
// an unbounded one.
func clampWaitMillis(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	ms := d.Milliseconds()
	if ms < 1 {
		return 1
	}
	if ms >= maxWaitMillis {
		return maxWaitMillis
	}
	return uint32(ms)
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
