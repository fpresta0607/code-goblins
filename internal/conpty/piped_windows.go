package conpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// PipedSpec is one process to run on pipes rather than in a pseudo console.
type PipedSpec struct {
	// Args is the command and its arguments; Args[0] is an absolute path to an
	// executable, as for Spec.
	Args []string
	Dir  string
	// Env is the whole environment, one KEY=value each.
	Env []string
	// Stderr takes what the process writes to its standard error.
	Stderr io.Writer
	// Processors is the mask of the processor threads the process and
	// everything it starts may run on, as for Spec; 0 leaves them where this
	// process may run.
	Processors uintptr
}

// Piped is one process run on pipes, for a program that speaks in lines
// rather than drawing a screen, as Claude Code does with stream-json. It runs
// in the same kind of job as a Console's process, scheduled the same way, so
// Close ends it and everything it starts, except a process that breaks away
// (a goblin host the CFO launches) or a machine service, and its hidden
// console keeps what it starts from opening windows.
type Piped struct {
	cmd        *exec.Cmd
	in         *os.File
	out        *os.File
	job        windows.Handle
	scheduling *jobScheduling
	done       chan struct{}
	code       uint32
}

// StartPiped runs spec.Args with its standard input and output on pipes. The
// process is created suspended and runs its first instruction in its job,
// on its processor threads.
func StartPiped(spec PipedSpec) (*Piped, error) {
	if len(spec.Args) == 0 {
		return nil, errors.New("conpty: a command is required")
	}
	if err := interactiveScheduling(windows.CurrentProcess()); err != nil {
		return nil, fmt.Errorf("conpty: host scheduling: %w", err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("conpty: create job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK
	if err := proc.SetJobInformation(job, windows.JobObjectExtendedLimitInformation, unsafe.Pointer(&limits), unsafe.Sizeof(limits)); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("conpty: limit job: %w", err)
	}
	// Pipes of its own rather than exec's, whose Wait closes the output as
	// the process exits, before all of it is read.
	inRead, in, err := os.Pipe()
	if err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("conpty: input pipe: %w", err)
	}
	out, outWrite, err := os.Pipe()
	if err != nil {
		windows.CloseHandle(job)
		inRead.Close()
		in.Close()
		return nil, fmt.Errorf("conpty: output pipe: %w", err)
	}
	cmd := execx.Command(spec.Args[0], spec.Args[1:]...)
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = spec.Dir, spec.Env, inRead, outWrite, spec.Stderr
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	err = cmd.Start()
	// The process holds its own copies of these ends.
	inRead.Close()
	outWrite.Close()
	if err != nil {
		windows.CloseHandle(job)
		in.Close()
		out.Close()
		return nil, fmt.Errorf("conpty: start process: %w", err)
	}
	p := &Piped{cmd: cmd, in: in, out: out, job: job, done: make(chan struct{})}
	if err := p.join(spec.Processors); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if p.scheduling != nil {
			p.scheduling.close()
		}
		windows.CloseHandle(job)
		in.Close()
		out.Close()
		return nil, err
	}
	go p.wait()
	return p, nil
}

// join keeps the suspended process to its processor threads, puts it in the
// job, schedules it as a Console's process is, and lets it run.
func (p *Piped) join(processors uintptr) error {
	pid := p.cmd.Process.Pid
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SET_INFORMATION|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("conpty: open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(process)
	if processors != 0 {
		if result, _, err := setProcessAffinityMask.Call(uintptr(process), processors); result == 0 {
			return fmt.Errorf("conpty: keep the process to its processor threads: %w", err)
		}
	}
	if err := windows.AssignProcessToJobObject(p.job, process); err != nil {
		return fmt.Errorf("conpty: assign job: %w", err)
	}
	if err := interactiveScheduling(process); err != nil {
		return fmt.Errorf("conpty: child scheduling: %w", err)
	}
	if p.scheduling, err = monitorJobScheduling(p.job); err != nil {
		return err
	}
	if err := proc.Resume(pid); err != nil {
		return fmt.Errorf("conpty: resume process: %w", err)
	}
	return nil
}

func (p *Piped) wait() {
	_ = p.cmd.Wait()
	p.code = uint32(p.cmd.ProcessState.ExitCode())
	close(p.done)
}

// PID is the process that was started.
func (p *Piped) PID() int {
	return p.cmd.Process.Pid
}

// Read reads what the process wrote to its standard output. It returns io.EOF
// once the process has exited and its output is read.
func (p *Piped) Read(b []byte) (int, error) {
	return p.out.Read(b)
}

// Write writes to the process's standard input, and keeps the processes of
// its job scheduled as interactive, as typing into a Console does.
func (p *Piped) Write(b []byte) (int, error) {
	if err := p.scheduling.reconcile(); err != nil {
		return 0, err
	}
	return p.in.Write(b)
}

// CloseInput closes the process's standard input, which a program that reads
// lines takes as its end.
func (p *Piped) CloseInput() error {
	return p.in.Close()
}

// Done closes once the process has exited.
func (p *Piped) Done() <-chan struct{} {
	return p.done
}

// ExitCode is the process's exit code, once Done is closed.
func (p *Piped) ExitCode() uint32 {
	<-p.done
	return p.code
}

// Close ends the process and everything it started but a machine service
// (proc.EndJob); the owner calls it once.
func (p *Piped) Close() error {
	_, spared, err := proc.EndJob(context.Background(), p.job)
	// What the end spared outlives the job's handle, so a machine service is
	// never ended with it, even by an end that failed beside it.
	if len(spared) > 0 {
		err = errors.Join(err, proc.KeepJobOnClose(p.job))
	}
	<-p.done
	p.in.Close()
	p.out.Close()
	p.scheduling.close()
	windows.CloseHandle(p.job)
	if err != nil {
		return fmt.Errorf("conpty: end the process tree: %w", err)
	}
	return nil
}
