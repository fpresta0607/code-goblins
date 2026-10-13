package verify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Executor owns commands and any cleanup still pending after Run returns.
// Its owner must retain it and call RetryCleanup until that succeeds.
// The model or orchestrator holds no lease between individual commands.
type Executor struct {
	Admission Admission
	Monitor   Monitor
	// A file is inherited directly, without a Go I/O pump that could remain
	// blocked on an arbitrary reader after the process exits. Without one the
	// command reads an empty input.
	Stdin *os.File
	// Stdout and Stderr, when both are set, are the command's output, as a
	// caller's own console or pipe is, so the command sees what its caller
	// does and the caller keeps the output: the execution keeps no logs and
	// the result holds no output. Otherwise the command writes to the
	// execution's logs.
	Stdout, Stderr *os.File
	// OutputLimit, 1 GiB unless set, is how much the command may write to
	// its logs together before its run is interrupted.
	OutputLimit int64
	// IsConsoleShared starts the command in this process's console group
	// rather than in a group of its own, so a Ctrl+C at the console reaches
	// it as it reaches this process and it answers for itself: the context
	// then ends only the wait for the turn, and a command that started runs
	// until it ends. Pressure ends its job with no gentler step first, since
	// a Ctrl+Break reaches only a whole group, this process's own included.
	IsConsoleShared bool
	// Took, when set, is told how long the run waited for its turn, once it
	// has taken it and before its command starts.
	Took func(waited time.Duration)

	mu      sync.Mutex
	pending map[string]*pendingCleanup
}

type pendingCleanup struct {
	mu                sync.Mutex
	tree              MeasuredJob
	turn              Turn
	guard             Custody
	execution         *Execution
	isFinished        bool
	usage             *execx.TreeUsage
	spared            []Leftover
	isGuardComplete   bool
	isTurnReleased    bool
	isEvidencePending bool
	isDone            bool
	exitCode          int
}

func (executor *Executor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	result, _, err := executor.RunWithin(ctx, request, 0)
	return result, err
}

// RunWithin is Run with limit, when positive, bounding the command from the
// moment it starts, so the wait for its turn never counts against it. It
// also returns the path of the execution's record, or nothing when no record
// could be made. The limit stops once the command's own process exits, so
// what it left running is judged by the linger alone. A command its limit
// ends fails with ErrLimit.
func (executor *Executor) RunWithin(ctx context.Context, request execx.Request, limit time.Duration) (execx.Result, string, error) {
	if request.Dir == "" {
		var err error
		request.Dir, err = os.Getwd()
		if err != nil {
			return execx.Result{ExitCode: -1}, "", err
		}
	}
	execution, err := BeginExecution(request.Dir, append([]string{request.Name}, request.Args...))
	if err != nil {
		return execx.Result{ExitCode: -1}, "", err
	}
	pending := &pendingCleanup{execution: execution, isGuardComplete: true, isTurnReleased: true, usage: &execx.TreeUsage{}, exitCode: -1}
	executor.mu.Lock()
	if executor.pending == nil {
		executor.pending = make(map[string]*pendingCleanup)
	}
	executor.pending[execution.ID()] = pending
	executor.mu.Unlock()
	defer func() {
		executor.mu.Lock()
		defer executor.mu.Unlock()
		if pending := executor.pending[execution.ID()]; pending != nil {
			pending.isFinished = true
			if pending.usage != nil && pending.isGuardComplete && pending.isTurnReleased && !pending.isEvidencePending && execution.record.Status != "pending" {
				delete(executor.pending, execution.ID())
			}
		}
	}()
	var exitCode int
	var failure error
	var output, errorsOutput []byte
	if executor.Stdout != nil && executor.Stderr != nil {
		if err := execution.keepNoLogs(); err != nil {
			err = fmt.Errorf("%w: %w", ErrNoTurn, err)
			return execx.Result{ExitCode: -1}, execution.Path(), errors.Join(err, execution.Refuse(err, 0))
		}
		exitCode, failure = executor.execute(ctx, request, limit, pending, executor.Stdout, executor.Stderr)
		pending.exitCode = exitCode
	} else {
		stdout, err := os.OpenFile(execution.record.StdoutLog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrNoTurn, err)
			return execx.Result{ExitCode: -1}, execution.Path(), errors.Join(err, execution.Refuse(err, 0))
		}
		defer stdout.Close()
		stderr, err := os.OpenFile(execution.record.StderrLog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrNoTurn, err)
			return execx.Result{ExitCode: -1}, execution.Path(), errors.Join(err, execution.Refuse(err, 0))
		}
		defer stderr.Close()
		exitCode, failure = executor.execute(ctx, request, limit, pending, stdout, stderr)
		pending.exitCode = exitCode
		failure = errors.Join(failure, stdout.Sync(), stderr.Sync())
		var outputError, errorsOutputError error
		output, outputError = outputTail(execution.record.StdoutLog)
		errorsOutput, errorsOutputError = outputTail(execution.record.StderrLog)
		failure = errors.Join(failure, outputError, errorsOutputError)
	}
	if execution.record.Status == "pending" {
		if err := ctx.Err(); err != nil {
			failure = errors.Join(failure, err, execution.interrupt())
		}
		if err := execution.Finish(exitCode, failure); err != nil {
			pending.isEvidencePending = true
			failure = errors.Join(failure, err)
		}
	}
	if failure != nil {
		failure = fmt.Errorf("verify: execution %s failed (%s): %w", execution.ID(), execution.record.Measurement.State, failure)
	}
	return execx.Result{Stdout: output, Stderr: errorsOutput, ExitCode: exitCode}, execution.Path(), failure
}

func (executor *Executor) execute(ctx context.Context, request execx.Request, limit time.Duration, pending *pendingCleanup, stdout, stderr *os.File) (exitCode int, failure error) {
	execution := pending.execution
	var waited time.Duration
	refuse := func(failure error) (int, error) { return -1, errors.Join(failure, execution.Refuse(failure, waited)) }
	// What fails once the turn is taken, before the command starts, gives the
	// turn back with the command never started.
	refuseStart := func(failure error) (int, error) { return refuse(fmt.Errorf("%w: %w", ErrNotStarted, failure)) }
	monitor := executor.Monitor
	if monitor.Headroom == nil {
		return refuse(fmt.Errorf("%w: verify: machine headroom reader is required", ErrNoTurn))
	}
	admission := executor.Admission
	admission.Available = nil
	admission.Headroom = monitor.Headroom
	admission.Reserve.PhysicalAvailable = max(admission.Reserve.PhysicalAvailable, monitor.Reserve.PhysicalAvailable)
	admission.Reserve.CommitAvailable = max(admission.Reserve.CommitAvailable, monitor.Reserve.CommitAvailable)
	turn, err := admission.Wait(ctx)
	waited = turn.Waited
	pending.turn = turn
	pending.isTurnReleased = turn.release == nil
	defer func() {
		if pending.usage != nil && !pending.isGuardComplete {
			if err := pending.guard.Complete(); err != nil {
				failure = errors.Join(failure, err)
			} else {
				pending.isGuardComplete = true
			}
		}
		if err := turn.Release(); err != nil {
			failure = errors.Join(failure, err)
		} else {
			pending.isTurnReleased = true
		}
	}()
	if err != nil {
		return refuse(fmt.Errorf("%w: %w", ErrNoTurn, err))
	}
	if executor.Took != nil {
		executor.Took(turn.Waited)
	}
	// The store's desktop reserve is the least a run leaves the desktop: a
	// caller may raise it, never lower it.
	reserve, err := DesktopReserve(turn.dir)
	if err != nil {
		return refuseStart(err)
	}
	monitor.Reserve.PhysicalAvailable = max(monitor.Reserve.PhysicalAvailable, reserve.PhysicalAvailable)
	monitor.Reserve.CommitAvailable = max(monitor.Reserve.CommitAvailable, reserve.CommitAvailable)
	if err := execution.Admit(turn.Waited); err != nil {
		return refuseStart(err)
	}
	guard, err := turn.Protect(execution.ID(), execution.Path())
	if guard.RunID != "" {
		pending.guard = guard
		pending.isGuardComplete = false
	}
	if err != nil {
		return refuseStart(err)
	}
	if err := execution.Start(); err != nil {
		return refuseStart(err)
	}
	command := execx.Command(request.Name, request.Args...)
	if !executor.IsConsoleShared {
		command.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	}
	command.Dir = request.Dir
	command.Stdout, command.Stderr = stdout, stderr
	// A nil file in the interface would hand the command a duplicate of this
	// process's own handle as its input, not an empty one.
	if executor.Stdin != nil {
		command.Stdin = executor.Stdin
	}
	environment := request.Env
	if environment == nil {
		environment = os.Environ()
	}
	command.Env = append(append([]string(nil), environment...), TurnVariable+"="+turn.Token())
	tree, err := execx.StartNamedTree(command, guard.Job)
	if err != nil {
		failure := fmt.Errorf("%w: %w", ErrNotStarted, err)
		measurement := Measurement{State: CleanupUncertain}
		if command.Process == nil {
			measurement = Measurement{State: Interrupted, CleanupConfirmed: true}
		} else {
			pending.usage = nil
		}
		return -1, errors.Join(failure, execution.Observe(measurement))
	}
	pending.tree = tree
	pending.usage = nil
	exited := make(chan struct{})
	var waitError error
	go func() {
		waitError = command.Wait()
		close(exited)
	}()
	monitor.Exited = exited
	monitor.Cancel = func() error {
		select {
		case <-exited:
			// The group's first process has exited, and its pid may name
			// another process now. The job's end reaches what it left.
			return nil
		default:
		}
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid))
	}
	if executor.IsConsoleShared {
		monitor.Cancel = nil
	}
	isLogged := executor.Stdout == nil || executor.Stderr == nil
	outputLimit := executor.OutputLimit
	if outputLimit <= 0 {
		outputLimit = 1 << 30
	}
	var persisted time.Time
	observe := monitor.Observe
	monitor.Observe = func(measurement Measurement) error {
		if measurement.State == Running {
			if isLogged {
				written, err := writtenTo(stdout, stderr)
				if err != nil {
					return err
				}
				if written > outputLimit {
					return fmt.Errorf("verify: the command wrote %d bytes of output, past its limit of %d", written, outputLimit)
				}
			}
			// A sample is kept every ten seconds, and the final measurement
			// always, so a long run is not a file write every poll.
			if time.Since(persisted) >= 10*time.Second {
				if err := execution.Observe(measurement); err != nil {
					return err
				}
				persisted = time.Now()
			}
		}
		if observe != nil {
			return observe(measurement)
		}
		return nil
	}
	// A command that answers an interrupt itself runs until it ends.
	parent := ctx
	if executor.IsConsoleShared {
		parent = context.WithoutCancel(ctx)
	}
	runContext, stopRun := context.WithCancelCause(parent)
	defer stopRun(nil)
	if limit > 0 {
		timer := time.AfterFunc(limit, func() { stopRun(ErrLimit) })
		defer timer.Stop()
		go func() {
			select {
			case <-exited:
				timer.Stop()
			case <-runContext.Done():
			}
		}()
	}
	measurement, failure := monitor.Watch(runContext, tree)
	if measurement.CleanupConfirmed {
		pending.usage = &measurement.Usage
		pending.spared = measurement.Spared
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		failure = errors.Join(failure, errors.New("verify: command wait did not finish after bounded cleanup"))
		if err := execution.Observe(measurement); err != nil {
			pending.isEvidencePending = true
			failure = errors.Join(failure, err)
		}
		return -1, failure
	}
	if runContext.Err() != nil {
		failure = errors.Join(failure, context.Cause(runContext))
		if measurement.State == Completed {
			measurement.State = Interrupted
		}
	}
	if err := execution.Observe(measurement); err != nil {
		pending.isEvidencePending = true
		failure = errors.Join(failure, err)
	}
	exitCode = -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	var exitError *exec.ExitError
	if waitError != nil && !errors.As(waitError, &exitError) {
		failure = errors.Join(failure, waitError)
	}
	return exitCode, failure
}

// RetryCleanup retries retained jobs under the caller's deadline. A later
// cleanup proof can release custody but can never turn a failed run green.
func (executor *Executor) RetryCleanup(ctx context.Context) error {
	executor.mu.Lock()
	ready := make(map[string]*pendingCleanup)
	var failure error
	for id, pending := range executor.pending {
		if !pending.isFinished {
			failure = errors.Join(failure, fmt.Errorf("verify: execution %s is still finalizing", id))
			continue
		}
		ready[id] = pending
	}
	executor.mu.Unlock()
	for id, pending := range ready {
		if err := pending.retry(ctx); err != nil {
			failure = errors.Join(failure, fmt.Errorf("verify: execution %s recovery remains incomplete: %w", id, err))
			continue
		}
		executor.mu.Lock()
		delete(executor.pending, id)
		executor.mu.Unlock()
	}
	return failure
}

func (pending *pendingCleanup) retry(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := lockBefore(ctx, &pending.mu); err != nil {
		return err
	}
	defer pending.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if pending.isDone {
		return nil
	}
	if pending.usage == nil {
		if pending.tree == nil {
			return errors.New("verify: failed process setup requires external cleanup proof")
		}
		usage, spared, err := pending.tree.EndAndWait(ctx)
		if err != nil {
			return err
		}
		if len(spared) == 0 && usage.ActiveProcesses != 0 {
			return errors.New("verify: retry returned active workers")
		}
		pending.usage = &usage
		for _, process := range spared {
			pending.spared = append(pending.spared, Leftover{Name: process.Name, PID: process.PID})
		}
	}
	if !pending.isGuardComplete {
		if err := pending.guard.CompleteContext(ctx); err != nil {
			return err
		}
		pending.isGuardComplete = true
	}
	if !pending.isTurnReleased {
		if err := pending.turn.ReleaseContext(ctx); err != nil {
			return err
		}
		pending.isTurnReleased = true
	}
	if err := pending.execution.recoveredCleanup(ctx, *pending.usage, pending.spared, pending.exitCode); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	pending.isDone = true
	return nil
}

// writtenTo is how much has been written to files together.
func writtenTo(files ...*os.File) (int64, error) {
	var written int64
	for _, file := range files {
		info, err := file.Stat()
		if err != nil {
			return 0, fmt.Errorf("verify: measure the command's output: %w", err)
		}
		written += info.Size()
	}
	return written, nil
}

// Keep complete logs on disk without allocating an unbounded command result.
func outputTail(path string) ([]byte, error) {
	file, err := fsx.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const limit = 4 << 20
	if _, err := file.Seek(max(0, info.Size()-limit), io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, limit))
}
