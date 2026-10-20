package browserlogin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/priority"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

const (
	// devToolsWait bounds a Chrome's start up to its DevTools answering. One
	// took over 30 s on a loaded CI runner on 2026-10-14, and a fetch's own
	// bound of two minutes leaves room for twice that.
	devToolsWait = time.Minute
	// closeWait is how long a Chrome asked to close has before it is ended.
	// A headless Chrome took 6 to 10 seconds to exit on this machine.
	closeWait = 15 * time.Second
	// teardownWait is how long the processes of an ended job have to be
	// gone. Windows finished tearing every process of one job down at the
	// same moment, 3 to 14 seconds after it ended on an idle machine, and
	// past 30 seconds while a test run loaded it. A process holds its files
	// until it is gone.
	teardownWait = time.Minute
)

// Process is a Chrome started on a login's profile: its pid, proven by when
// it started, its DevTools address on this machine, and the job that holds
// it and every helper it starts. The job ends them all when its last handle
// closes, so a Chrome never outlives the process that started it.
type Process struct {
	PID     int
	Started time.Time
	URL     string
	job     *job
}

// job is a Chrome's job object, closed once however many copies of its
// Process end it.
type job struct {
	handle     windows.Handle
	unassigned *os.Process
	waited     <-chan struct{}
	once       sync.Once
	err        error
	transport  *devToolsBridge
	// members are the processes the job held when it was ended, which Gone
	// checks again, and isEnded says the job was ended: a Chrome that closed
	// when asked leaves it holding none.
	members []proc.Entry
	isEnded bool
}

// Start starts launch's Chrome and returns once its DevTools answer. It is
// started suspended and put in a job of its own before it runs, so nothing
// it starts escapes the job. A failed start closes its Chrome. If cleanup
// cannot be confirmed, it returns the owned Process alongside the error,
// and the caller must retain that ownership and refuse profile reuse.
func Start(ctx context.Context, launch Launch) (Process, error) {
	return start(ctx, launch, func(ctx context.Context, pipe *devToolsPipe) error {
		return pipe.call(ctx, "Browser.getVersion", nil, nil)
	}, Process.close)
}

func start(ctx context.Context, launch Launch, await func(context.Context, *devToolsPipe) error, cleanup func(Process) error) (process Process, failure error) {
	if err := ctx.Err(); err != nil {
		return Process{}, err
	}
	if err := ClearPort(launch.Profile); err != nil {
		return Process{}, err
	}
	held, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return Process{}, fmt.Errorf("create chrome's job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if err := proc.SetJobInformation(held, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		windows.CloseHandle(held)
		return Process{}, fmt.Errorf("limit chrome's job: %w", err)
	}
	cmd := execx.Command(launch.Executable, launch.Arguments()...)
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	transport, closeChildren, err := debugHandles(cmd)
	if err != nil {
		windows.CloseHandle(held)
		return Process{}, err
	}
	defer closeChildren()
	if err := ctx.Err(); err != nil {
		transport.close()
		windows.CloseHandle(held)
		return Process{}, err
	}
	if err := cmd.Start(); err != nil {
		transport.close()
		windows.CloseHandle(held)
		return Process{}, fmt.Errorf("start chrome: %w", err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	closeChildren()
	process = Process{PID: cmd.Process.Pid, job: &job{handle: held, unassigned: cmd.Process, waited: waited, transport: transport}}
	defer func() {
		if failure == nil {
			return
		}
		if err := cleanup(process); err != nil {
			failure = errors.Join(failure, err)
		} else {
			process = Process{}
		}
	}()
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID))
	if err == nil {
		err = windows.AssignProcessToJobObject(held, handle)
		windows.CloseHandle(handle)
	}
	if err != nil {
		return process, fmt.Errorf("put chrome in its job: %w", err)
	}
	process.job.unassigned = nil
	started, ok := proc.StartTime(process.PID)
	if !ok {
		return process, fmt.Errorf("chrome %d's start time could not be read", process.PID)
	}
	process.Started = started
	if err := ctx.Err(); err != nil {
		return process, err
	}
	if err := proc.Resume(process.PID); err != nil {
		return process, err
	}
	ready, cancel := context.WithTimeout(ctx, devToolsWait)
	defer cancel()
	if err := await(ready, transport.pipe); err != nil {
		return process, err
	}
	if err := ctx.Err(); err != nil {
		return process, err
	}
	if err := transport.listen(); err != nil {
		return process, err
	}
	process.URL = transport.address
	if err := ctx.Err(); err != nil {
		return process, err
	}
	return process, nil
}

// End closes process: it asks the browser to close over its private pipe,
// which writes its session to the profile, gives it closeWait to exit, and
// then ends its job, which ends whatever of it is left, its helpers
// included. A browser that closes the pipe before it answers took the ask,
// and one that could not be asked is ended at once. It returns once nothing
// of the job runs, so the profile can be removed, and an error only when
// that could not be made so.
//
// Chrome does its saving on threads it runs at background priority, and
// each of its processes ends only once its background threads have run, so
// while other work filled the machine's cores, a close waited tens of
// seconds for them: past closeWait, when the job was ended before the
// browser had written its sign-in to the profile, and past teardownWait,
// when its end could not be confirmed (issue #896). The browser's processes
// are raised to normal priority before it is asked, since answering the ask
// waits on its threads too: beside a busy thread on a CI runner, a browser
// raised only once it had answered was ended 7 seconds into its close, as
// only a failed ask, such as one unanswered within its 5 seconds, ends it
// before closeWait. They are raised again each second it is waited
// for, which picks up a thread it lowers after the last raise, and once more
// when its job is ended.
func End(ctx context.Context, process Process) error {
	if Running(process) {
		process.job.raise()
		asked, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := process.job.transport.pipe.call(asked, "Browser.close", nil, nil)
		if err == nil || errors.Is(err, errPipeClosed) {
			for deadline, raised := time.Now().Add(closeWait), time.Now(); Running(process) && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
				if time.Since(raised) >= time.Second {
					process.job.raise()
					raised = time.Now()
				}
			}
		}
		cancel()
	}
	return process.close()
}

// raise runs the processes j holds now at normal priority at least
// (priority.Raise).
func (j *job) raise() {
	if ids, _, err := proc.JobProcessIDs(syscall.Handle(j.handle)); err == nil {
		priority.Raise(ids)
	}
}

// close ends process's job and everything in it, and waits until each of
// them has exited: a process ended through its job still holds its files
// until Windows has torn it down, which its handle says once it has.
func (p Process) close() error {
	p.job.once.Do(func() { p.job.err = p.job.end(p.PID) })
	return p.job.err
}

func (j *job) end(pid int) error {
	defer windows.CloseHandle(j.handle)
	if j.transport != nil {
		j.transport.close()
	}
	if j.unassigned != nil {
		if err := j.unassigned.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("end unassigned chrome %d: %w", pid, err)
		}
		select {
		case <-j.waited:
			return nil
		case <-time.After(teardownWait):
			return fmt.Errorf("unassigned chrome %d did not exit", pid)
		}
	}
	members, spared, err := proc.EndJob(context.Background(), j.handle)
	// What the end spared outlives the job's handle, so a machine service is
	// never ended with it, even by an end that failed beside it.
	if len(spared) > 0 {
		err = errors.Join(err, proc.KeepJobOnClose(j.handle))
	}
	if err != nil {
		return fmt.Errorf("end chrome %d's job: %w", pid, err)
	}
	j.members, j.isEnded = members, true
	// An ended process runs no code of its own, so none lowers a thread
	// again, and the job no longer lists the processes it ended.
	pids := make([]uint32, 0, len(members))
	for _, member := range members {
		pids = append(pids, uint32(member.PID))
	}
	priority.Raise(pids)
	deadline := time.Now().Add(teardownWait)
	for _, member := range members {
		if err := awaitGone(member, deadline); err != nil {
			return fmt.Errorf("chrome %d's processes were not gone %s after its job ended: %w", pid, teardownWait, err)
		}
	}
	return nil
}

// Gone reports whether everything process's ended job held has exited, for
// an end that could not confirm it in time: a helper took over a minute to
// finish exiting on a loaded machine. A job that was never ended, because
// its end failed first, reads as not gone, and one ended after its Chrome
// closed when asked held nothing and is gone.
func Gone(process Process) bool {
	j := process.job
	if j.unassigned != nil {
		select {
		case <-j.waited:
			return true
		default:
			return false
		}
	}
	if !j.isEnded {
		return false
	}
	for _, member := range j.members {
		if awaitGone(member, time.Now()) != nil {
			return false
		}
	}
	return true
}

// awaitGone waits until member, a process an ended job held, has been torn
// down, or deadline passes. A missing PID or one that started at another
// time is gone. An unreadable process leaves cleanup unconfirmed.
func awaitGone(member proc.Entry, deadline time.Time) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(member.PID))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return err
	}
	if difference := time.Unix(0, creation.Nanoseconds()).Sub(member.Start); difference <= -time.Second || difference >= time.Second {
		return nil
	}
	event, err := windows.WaitForSingleObject(handle, uint32(max(time.Until(deadline), 0).Milliseconds()))
	if err != nil {
		return err
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("process %d is still there", member.PID)
	}
	return nil
}

// stillActive is GetExitCodeProcess's answer for a process that has not
// exited.
const stillActive = 259

// Running reports whether process is still the running process it was
// started as.
func Running(process Process) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(process.PID))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return false
	}
	if difference := time.Unix(0, creation.Nanoseconds()).Sub(process.Started); difference <= -time.Second || difference >= time.Second {
		return false
	}
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}
