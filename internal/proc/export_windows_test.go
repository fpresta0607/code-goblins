package proc

import (
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// EndJobFor is how long EndJob keeps ending a job's processes one by one.
const EndJobFor = endJobFor

// LoadForTest makes an end beside a machine service, until the test ends,
// meet a machine whose cores other work fills: each process the end ends
// finishes exiting exit after the end asked, and each read of a job's list
// once it has asked takes read, though the list is the one Windows gave as
// the read began, as a read starved of a processor is.
func LoadForTest(t testing.TB, exit, read time.Duration) {
	terminate, list := terminateMember, readJobList
	t.Cleanup(func() { terminateMember, readJobList = terminate, list })
	var isEnding atomic.Bool
	terminateMember = func(process windows.Handle) error {
		isEnding.Store(true)
		var late windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), process, windows.CurrentProcess(), &late, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return err
		}
		go func() {
			time.Sleep(exit)
			_ = terminate(late)
			windows.CloseHandle(late)
		}()
		return nil
	}
	readJobList = func(job syscall.Handle, buffer []uintptr) error {
		err := list(job, buffer)
		if isEnding.Load() {
			time.Sleep(read)
		}
		return err
	}
}

// LeaveOutForTest makes every read of a job's list, until the test ends,
// neither list nor count process pid, as Windows does from the moment a job
// nested in the one read begins to end a process.
func LeaveOutForTest(t testing.TB, pid int) {
	original := readJobList
	t.Cleanup(func() { readJobList = original })
	readJobList = func(job syscall.Handle, buffer []uintptr) error {
		if err := original(job, buffer); err != nil {
			return err
		}
		const headerWords = int(8 / unsafe.Sizeof(uintptr(0)))
		counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
		ids := buffer[headerWords : headerWords+int(counts[1])]
		if index := slices.Index(ids, uintptr(pid)); index >= 0 {
			copy(ids[index:], ids[index+1:])
			counts[0]--
			counts[1]--
		}
		return nil
	}
}
