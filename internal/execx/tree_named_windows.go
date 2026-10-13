package execx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	createJobObject = usageKernel.NewProc("CreateJobObjectW")
	openJobObject   = usageKernel.NewProc("OpenJobObjectW")
)

// Job object access rights that golang.org/x/sys does not name.
const (
	jobObjectQuery     = 0x0004
	jobObjectTerminate = 0x0008
)

// StartNamedTree is StartTree in a job created under name, so a later
// process can open that job by its name and prove it empty after this one
// has gone. A name that already exists is refused before anything starts,
// and no child can inherit the job's handle.
func StartNamedTree(cmd *exec.Cmd, name string) (*Tree, error) {
	job, err := createExclusiveJob(name)
	if err != nil {
		return nil, err
	}
	return startTreeIn(cmd, job)
}

// createExclusiveJob creates a job named name. CreateJobObjectW hands back
// the existing job for a name that is taken and says so only in the last
// error, which golang.org/x/sys's wrapper drops, so it is called directly:
// the call clears the last error before it runs.
func createExclusiveJob(name string) (windows.Handle, error) {
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, fmt.Errorf("execx: name a job %q: %w", name, err)
	}
	job, _, callError := createJobObject.Call(0, uintptr(unsafe.Pointer(pointer)))
	if job == 0 {
		return 0, fmt.Errorf("execx: create job %s: %w", name, callError)
	}
	if errors.Is(callError, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(windows.Handle(job))
		return 0, fmt.Errorf("execx: job %s already exists", name)
	}
	return windows.Handle(job), nil
}

// EndNamedJob ends whatever still runs in the job named name and returns
// once Windows counts it empty, within five seconds or the context's limit.
// isFound is false when no job has that name: a job no handle holds and no
// process runs in is gone, so nothing runs in it. Any error, such as a job
// this process may not open, proves nothing about what runs in it.
func EndNamedJob(ctx context.Context, name string) (usage TreeUsage, isFound bool, err error) {
	pointer, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return TreeUsage{}, false, fmt.Errorf("execx: name a job %q: %w", name, err)
	}
	job, _, callError := openJobObject.Call(jobObjectQuery|jobObjectTerminate, 0, uintptr(unsafe.Pointer(pointer)))
	if job == 0 {
		if errors.Is(callError, windows.ERROR_FILE_NOT_FOUND) {
			return TreeUsage{}, false, nil
		}
		return TreeUsage{}, false, fmt.Errorf("execx: open job %s: %w", name, callError)
	}
	tree := &Tree{job: windows.Handle(job)}
	usage, err = tree.TerminateAndWait(ctx)
	if err != nil {
		tree.mu.Lock()
		defer tree.mu.Unlock()
		if !tree.isDone {
			tree.isDone = true
			err = errors.Join(err, windows.CloseHandle(tree.job))
		}
		return usage, true, err
	}
	return usage, true, nil
}
