package execx

import (
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// KillsOnCloseForTest reports whether the tree's job still ends what it holds
// when its last handle closes.
func (t *Tree) KillsOnCloseForTest() (bool, error) {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := queryJob(t.job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		return false, err
	}
	return limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE != 0, nil
}

// CountUnlistedForTest makes the next reads of a job's members, until the
// test ends, count one process beyond the list, as Windows does while a
// process exits.
func CountUnlistedForTest(t testing.TB, reads int) {
	original := jobProcessIDs
	t.Cleanup(func() { jobProcessIDs = original })
	jobProcessIDs = func(job syscall.Handle) ([]uint32, uint32, error) {
		ids, unlisted, err := original(job)
		if reads > 0 {
			reads--
			unlisted++
		}
		return ids, unlisted, err
	}
}
