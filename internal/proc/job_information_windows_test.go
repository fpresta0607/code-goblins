package proc

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/stacktest"
)

// newJob creates an empty job, closed with the test.
func newJob(t *testing.T) windows.Handle {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(job) })
	return job
}

// setLimitsAt sets the job's limits from the memory at information, passed
// as a bare uintptr, as windows.SetInformationJobObject takes it.
//
//go:noinline
func setLimitsAt(job windows.Handle, information uintptr) error {
	_, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, information, uint32(unsafe.Sizeof(windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{})))
	return err
}

// Each set of a job's limits lands, wherever the setting goroutine's stack
// moves on the way to the system call: the buffer handed to Windows is still
// where the buffer is when the set returns, and the job reads back what was
// set. A buffer on the stack, passed to Windows as a bare uintptr, is left
// behind by the move, and Windows reads whatever is where it was (issue 1110).
func TestSetJobInformationLandsWhereverItsStackMoves(t *testing.T) {
	// Arrange
	job := newJob(t)
	// The sweep must move the stack inside a set: a stack buffer passed as a
	// bare uintptr is somewhere else when the set returns at some depth.
	moved := stacktest.FailingDepths(func() bool {
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		handed := uintptr(unsafe.Pointer(&limits))
		err := setLimitsAt(job, handed)
		return err == nil && uintptr(unsafe.Pointer(&limits)) == handed
	})
	if len(moved) == 0 {
		t.Fatal("a stack buffer passed as a bare uintptr stayed where it was handed at every depth, so the sweep never moved the stack inside a set and proves nothing")
	}
	var processLimit uint32

	// Act
	lost := stacktest.FailingDepths(func() bool {
		processLimit++
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		limits.BasicLimitInformation.ActiveProcessLimit = processLimit
		handed := uintptr(unsafe.Pointer(&limits))
		if err := SetJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil || uintptr(unsafe.Pointer(&limits)) != handed {
			return false
		}
		var set windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		err := QueryJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&set), unsafe.Sizeof(set))
		return err == nil && set.BasicLimitInformation.ActiveProcessLimit == processLimit
	})

	// Assert
	if len(lost) > 0 {
		t.Errorf("at depths %v a set's buffer was no longer where it was handed to Windows, or the job did not read back what was set", lost)
	}
}

// KeepJobOnClose stops the job ending what it holds when its last handle
// closes, and keeps its other limits, wherever the stack moves inside it. Its
// read of the limits into a stack buffer read nothing when the stack moved,
// so it found no kill on close to stop and left the job to end, as it
// closes, the machine services a run's end spares (issue 1110).
func TestKeepJobOnCloseStopsKillOnCloseAloneWhereverItsStackMoves(t *testing.T) {
	// Arrange
	job := newJob(t)
	armed := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	armed.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	arm := func() error {
		return SetJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&armed), unsafe.Sizeof(armed))
	}
	if err := arm(); err != nil {
		t.Fatal(err)
	}
	movedInside := 0
	var left uint32

	// Act
	failing := stacktest.FailingDepths(func() bool {
		// KeepJobOnClose comes first, so the stack moves inside it.
		var marker byte
		before := uintptr(unsafe.Pointer(&marker))
		err := KeepJobOnClose(job)
		if uintptr(unsafe.Pointer(&marker)) != before {
			movedInside++
		}
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		readErr := QueryJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits))
		// The next depth starts from a job that ends what it holds.
		armErr := arm()
		flags := limits.BasicLimitInformation.LimitFlags
		if flags != windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK {
			left = flags
		}
		return err == nil && readErr == nil && armErr == nil && flags == windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	})

	// Assert
	if movedInside == 0 {
		t.Fatal("the stack never moved inside KeepJobOnClose, so the sweep proves nothing")
	}
	if len(failing) > 0 {
		t.Errorf("at depths %v KeepJobOnClose failed or left the job's limit flags at %#x; want %#x, kill on close stopped and the rest kept", failing, left, windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK)
	}
}
