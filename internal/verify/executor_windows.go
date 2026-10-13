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
	// blocked on an arbitrary reader after the process exits.
	Stdin *os.File

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
	isGuardComplete   bool
	isTurnReleased    bool
	isEvidencePending bool
	isDone            bool
	exitCode          int
}

func (executor *Executor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Dir == "" {
		var err error
		request.Dir, err = os.Getwd()
		if err != nil {
			return execx.Result{ExitCode: -1}, err
		}
	}
	execution, err := BeginExecution(request.Dir, append([]string{request.Name}, request.Args...))
	if err != nil {
		return execx.Result{ExitCode: -1}, err
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
	stdout, err := os.OpenFile(execution.record.StdoutLog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return execx.Result{ExitCode: -1}, errors.Join(err, execution.Refuse(err, 0))
	}
	defer stdout.Close()
	stderr, err := os.OpenFile(execution.record.StderrLog, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return execx.Result{ExitCode: -1}, errors.Join(err, execution.Refuse(err, 0))
	}
	defer stderr.Close()
	exitCode, failure := executor.execute(ctx, request, pending, stdout, stderr)
	pending.exitCode = exitCode
	failure = errors.Join(failure, stdout.Sync(), stderr.Sync())
	output, outputError := outputTail(execution.record.StdoutLog)
	errorsOutput, errorsOutputError := outputTail(execution.record.StderrLog)
	failure = errors.Join(failure, outputError, errorsOutputError)
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
	return execx.Result{Stdout: output, Stderr: errorsOutput, ExitCode: exitCode}, failure
}

func (executor *Executor) execute(ctx context.Context, request execx.Request, pending *pendingCleanup, stdout, stderr *os.File) (exitCode int, failure error) {
	execution := pending.execution
	var waited time.Duration
	refuse := func(failure error) (int, error) { return -1, errors.Join(failure, execution.Refuse(failure, waited)) }
	monitor := executor.Monitor
	if monitor.Headroom == nil {
		return refuse(errors.New("verify: machine headroom reader is required"))
	}
	admission := executor.Admission
	admission.Available = nil
	admission.Headroom = monitor.Headroom
	admission.Reserve.PhysicalAvailable = max(admission.Reserve.PhysicalAvailable, monitor.Reserve.PhysicalAvailable)
	admission.Reserve.CommitAvailable = max(admission.Reserve.CommitAvailable, monitor.Reserve.CommitAvailable)
	turn, err := admission.Wait(ctx)
	waited = turn.Waited
	if err != nil {
		return refuse(err)
	}
	pending.turn = turn
	pending.isTurnReleased = false
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
	_, floor, err := AdmissionLimits(turn.dir)
	if err != nil {
		return refuse(err)
	}
	monitor.Reserve.PhysicalAvailable = max(admission.Reserve.PhysicalAvailable, admission.Floor, floor)
	monitor.Reserve.CommitAvailable = max(admission.Reserve.CommitAvailable, admission.Floor, floor)
	if err := execution.Admit(turn.Waited); err != nil {
		return refuse(err)
	}
	guard, err := turn.Protect(execution.ID())
	if err != nil {
		return refuse(err)
	}
	pending.guard = guard
	pending.isGuardComplete = false
	if err := execution.Start(); err != nil {
		return refuse(err)
	}
	command := execx.Command(request.Name, request.Args...)
	command.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	command.Dir = request.Dir
	command.Stdin, command.Stdout, command.Stderr = executor.Stdin, stdout, stderr
	environment := request.Env
	if environment == nil {
		environment = os.Environ()
	}
	command.Env = append(append([]string(nil), environment...), TurnVariable+"="+turn.Token())
	tree, err := execx.StartTree(command)
	if err != nil {
		failure := err
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
	processWait := make(chan error, 1)
	go func() { processWait <- command.Wait() }()
	monitor.Cancel = func() error {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid))
	}
	observe := monitor.Observe
	monitor.Observe = func(measurement Measurement) error {
		if measurement.State == Running {
			if err := execution.Observe(measurement); err != nil {
				return err
			}
		}
		if observe != nil {
			return observe(measurement)
		}
		return nil
	}
	measurement, failure := monitor.Watch(ctx, tree)
	if measurement.CleanupConfirmed {
		pending.usage = &measurement.Usage
	}
	var waitError error
	select {
	case waitError = <-processWait:
	case <-time.After(5 * time.Second):
		failure = errors.Join(failure, errors.New("verify: command wait did not finish after bounded cleanup"))
		if err := execution.Observe(measurement); err != nil {
			pending.isEvidencePending = true
			failure = errors.Join(failure, err)
		}
		return -1, failure
	}
	if err := ctx.Err(); err != nil {
		failure = errors.Join(failure, err)
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
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !pending.mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
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
		usage, err := pending.tree.TerminateAndWait(ctx)
		if err != nil {
			return err
		}
		if usage.ActiveProcesses != 0 {
			return errors.New("verify: retry returned active workers")
		}
		pending.usage = &usage
	}
	if !pending.isGuardComplete {
		if err := pending.guard.Complete(); err != nil {
			return err
		}
		pending.isGuardComplete = true
	}
	if !pending.isTurnReleased {
		if err := pending.turn.Release(); err != nil {
			return err
		}
		pending.isTurnReleased = true
	}
	if err := pending.execution.recoveredCleanup(*pending.usage, pending.exitCode); err != nil {
		return err
	}
	pending.isDone = true
	return nil
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
