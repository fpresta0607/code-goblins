package execx

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"
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

// DelayEndForTest makes EndAndWait's end of a job's members, until the test
// ends, wait delay longer for them to exit, as a loaded machine can, and fail
// as the real wait does once its caller's deadline runs out within it.
func DelayEndForTest(t testing.TB, delay time.Duration) {
	original := endJob
	t.Cleanup(func() { endJob = original })
	endJob = func(ctx context.Context, job windows.Handle) ([]proc.Entry, []proc.Entry, error) {
		ended, spared, err := original(ctx, job)
		if err != nil {
			return ended, spared, err
		}
		select {
		case <-ctx.Done():
			return nil, nil, fmt.Errorf("execx: the job's processes outlived its end: %w", ctx.Err())
		case <-time.After(delay):
		}
		return ended, spared, nil
	}
}
