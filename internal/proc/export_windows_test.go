package proc

import (
	"slices"
	"syscall"
	"testing"
	"unsafe"
)

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
