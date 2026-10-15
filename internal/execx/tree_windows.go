package execx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// Tree is a process started in a job of its own (StartTree), with
// everything it starts.
type Tree struct {
	job windows.Handle
	cmd *exec.Cmd

	mu      sync.Mutex
	isDone  bool
	ended   []EndedProcess
	doneErr error
	// peak is the highest peak commitment read of the job, under which no
	// later read may fall.
	peak uint64
}

// StartTree starts cmd, made with Command or CommandContext, in a job of its
// own. Every process it starts joins the job, however it was started and
// whatever becomes of its parent, and none can leave: the job allows no
// breakaway, and a process that breaks away from a job of its own inside
// it, as a terminal's is, stays in this one. So End ends all of it, a helper
// whose parent already exited included, which ending a process with what
// its parents still reach never could. The job ends what it holds when its
// last handle closes, so the tree also ends with this process, should
// whoever started this process end it first, as a gate step's time limit
// does. The process is created suspended and runs its first instruction in
// the job. A cmd made with CommandContext is ended as a tree when its
// context ends. Once cmd has been waited for, the caller ends the tree or
// releases it.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("execx: create a job: %w", err)
	}
	return startTreeIn(cmd, job)
}

// startTreeIn starts cmd in job, which it owns from here on.
func startTreeIn(cmd *exec.Cmd, job windows.Handle) (*Tree, error) {
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("execx: limit a job: %w", err)
	}
	tree := &Tree{job: job, cmd: cmd}
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			_, err := tree.End()
			return err
		}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	if err := tree.join(); err != nil {
		_, endErr := tree.End()
		_ = cmd.Wait()
		return nil, errors.Join(err, endErr)
	}
	return tree, nil
}

// join puts the started process in the tree's job and lets it run, unless
// the tree was ended first, as its context's end ends it.
func (t *Tree) join() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	pid := t.cmd.Process.Pid
	if t.isDone {
		return fmt.Errorf("execx: process %d was ended before it joined a job of its own", pid)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("execx: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(t.job, process); err != nil {
		return fmt.Errorf("execx: put process %d in a job of its own: %w", pid, err)
	}
	return proc.Resume(pid)
}

// End ends every process still in the tree but a machine service and what
// runs under it (proc.EndJob), and returns what it ended, sorted by pid. A
// tree ended before its process joined it ends that process, which never
// ran. Once the tree is ended or released, End does nothing more and
// returns what that did.
func (t *Tree) End() ([]EndedProcess, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return t.ended, t.doneErr
	}
	t.isDone = true
	entries, spared, err := proc.EndJob(context.Background(), t.job)
	// What the end spared outlives the job's handle, so a machine service is
	// never ended with it, even by an end that failed beside it.
	if len(spared) > 0 {
		err = errors.Join(err, proc.KeepJobOnClose(t.job))
	}
	for _, entry := range entries {
		t.ended = append(t.ended, EndedProcess{Name: entry.ExeBase, PID: entry.PID})
	}
	// The job's end has ended the process, or will, unless the end came
	// before it joined. Ending it again says only that it has ended.
	_ = t.cmd.Process.Kill()
	t.doneErr = errors.Join(err, windows.CloseHandle(t.job))
	return t.ended, t.doneErr
}

// Release lets go of a tree whose process ended by itself: what the process
// left running runs on, as it did before its process was started as a
// tree, and no longer ends with this process. Once the tree is ended or
// released, Release does nothing more.
func (t *Tree) Release() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isDone {
		return t.doneErr
	}
	t.isDone = true
	t.doneErr = errors.Join(proc.KeepJobOnClose(t.job), windows.CloseHandle(t.job))
	return t.doneErr
}
