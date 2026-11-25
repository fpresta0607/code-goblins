package execx

import (
	"context"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// KillsOnCloseForTest reports whether the tree's job still ends what it holds
// when its last handle closes.
func (t *Tree) KillsOnCloseForTest() (bool, error) {
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := proc.QueryJobInformation(t.job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		return false, err
	}
	return limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE != 0, nil
}

// EndJobAgainForTest calls slow before each end of a job's processes after
// the first, until the test ends, as a loaded machine's reads of its
// processes, which each such end makes, can take seconds.
func EndJobAgainForTest(t testing.TB, slow func()) {
	original := endJob
	t.Cleanup(func() { endJob = original })
	hasEnded := false
	endJob = func(ctx context.Context, job windows.Handle) (ended, spared []proc.Entry, err error) {
		if hasEnded {
			slow()
		}
		hasEnded = true
		return original(ctx, job)
	}
}

// CountUnlistedForTest makes the next reads of a job's members, until the
// test ends, count one process beyond the list, as Windows does while a
// process exits, and calls counted before each such read returns.
func CountUnlistedForTest(t testing.TB, reads int, counted func()) {
	original := jobProcessIDs
	t.Cleanup(func() { jobProcessIDs = original })
	jobProcessIDs = func(job syscall.Handle) ([]uint32, uint32, error) {
		ids, unlisted, err := original(job)
		if reads > 0 {
			reads--
			unlisted++
			counted()
		}
		return ids, unlisted, err
	}
}
