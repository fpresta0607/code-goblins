package verify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Executor runs verification commands under the shared admission authority.
// The model or orchestrator holds no lease between individual commands.
type Executor struct {
	Admission Admission
	Monitor   Monitor
	Stdin     io.Reader
}

func (executor Executor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	var stdout, stderr bytes.Buffer
	exitCode, err := executor.Stream(ctx, request, &stdout, &stderr)
	return execx.Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCode}, err
}

func (executor Executor) Stream(ctx context.Context, request execx.Request, stdout, stderr io.Writer) (int, error) {
	if request.Dir == "" {
		var err error
		request.Dir, err = os.Getwd()
		if err != nil {
			return -1, err
		}
	}
	execution, err := BeginExecution(request.Dir, append([]string{request.Name}, request.Args...))
	if err != nil {
		return -1, err
	}
	refuse := func(failure error) (int, error) { return -1, errors.Join(failure, execution.Refuse(failure)) }
	monitor := executor.Monitor
	if monitor.Headroom == nil {
		return refuse(errors.New("verify: machine headroom reader is required"))
	}
	admission := executor.Admission
	admission.Floor = max(admission.Floor, monitor.Reserve.PhysicalAvailable, monitor.Reserve.CommitAvailable)
	admission.Available = func() (uint64, error) {
		memory, err := monitor.Headroom()
		return min(memory.PhysicalAvailable, memory.CommitAvailable), err
	}
	turn, err := admission.Wait(ctx)
	if err != nil {
		return refuse(err)
	}
	defer turn.Release()
	_, floor, err := AdmissionLimits(turn.dir)
	if err != nil {
		return refuse(err)
	}
	monitor.Reserve.PhysicalAvailable = max(monitor.Reserve.PhysicalAvailable, floor)
	monitor.Reserve.CommitAvailable = max(monitor.Reserve.CommitAvailable, floor)
	if err := execution.Admit(turn.Waited); err != nil {
		return refuse(err)
	}
	guard, err := turn.Protect(execution.ID())
	if err != nil {
		return refuse(err)
	}
	if err := execution.Start(); err != nil {
		return refuse(errors.Join(err, guard.Complete()))
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
			failure = errors.Join(failure, guard.Complete())
		}
		failure = errors.Join(failure, execution.Observe(measurement))
		return -1, errors.Join(failure, execution.Finish(-1, failure))
	}
	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	monitor.Cancel = func() error {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(command.Process.Pid))
	}
	observe := monitor.Observe
	monitor.Observe = func(measurement Measurement) error {
		if err := execution.Observe(measurement); err != nil {
			return err
		}
		if observe != nil {
			return observe(measurement)
		}
		return nil
	}
	measurement, failure := monitor.Watch(ctx, tree)
	if measurement.CleanupConfirmed {
		failure = errors.Join(failure, guard.Complete())
	}
	var waitError error
	select {
	case waitError = <-waited:
	case <-time.After(5 * time.Second):
		failure = errors.Join(failure, errors.New("verify: command wait did not finish after bounded cleanup"))
		return -1, errors.Join(failure, execution.Finish(-1, failure))
	}
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	var exitError *exec.ExitError
	if waitError != nil && !errors.As(waitError, &exitError) {
		failure = errors.Join(failure, waitError)
	}
	if err := execution.Finish(exitCode, failure); err != nil {
		failure = errors.Join(failure, err)
	}
	if failure != nil {
		return exitCode, fmt.Errorf("verify: execution %s failed (%s): %w", execution.ID(), measurement.State, failure)
	}
	return exitCode, nil
}
