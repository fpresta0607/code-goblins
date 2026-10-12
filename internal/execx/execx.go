// Package execx provides the small subprocess seam used by CFO integrations.
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

// Request describes one child process invocation.
type Request struct {
	Dir  string
	Env  []string
	Name string
	Args []string
	// KillTree starts the process as a tree (StartTree), so a run its
	// context ends is ended with everything the process started: the real
	// child of a shim such as an npm .cmd, which ending the shim alone leaves
	// running, and a helper whose parent already exited, which nothing that
	// follows parents can reach. The run's error then names what was ended
	// (Ended).
	KillTree bool
}

// Result contains the complete, separately captured child output.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes one child process.
type Runner interface {
	Run(ctx context.Context, req Request) (Result, error)
}

// Starter starts a child process without waiting for it to exit.
//
// It is intentionally separate from Runner because most callers need a fully
// captured result, while a server launcher only needs a process start attempt.
type Starter interface {
	Start(ctx context.Context, req Request) error
}

// OSRunner executes processes through the operating system.
type OSRunner struct{}

// Run starts and waits for the requested process. A normal non-zero exit is a
// result, not an execution error, so callers can distinguish tool refusals
// from failures to start or wait for the process. A run its context ended
// returns the context's error with what the process wrote before it ended,
// since a tool that answered and then hung is a different fault from one
// that never answered; it never exited, so its exit code is left zero.
func (OSRunner) Run(ctx context.Context, req Request) (Result, error) {
	cmd := command(ctx, req)
	cmd.WaitDelay = 2 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	tree, err := start(cmd, req.KillTree)
	if err != nil {
		return Result{}, err
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, cut(tree, err)
	}
	result := Result{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err := release(tree); err != nil {
		return result, err
	}
	if waitErr == nil {
		return result, nil
	}

	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		return result, nil
	}
	return Result{}, waitErr
}

// Stream starts the requested process with what it writes going to stdout
// and stderr as it writes it, waits for it and returns its exit code: a run
// read while it lasts, as a test run is. A normal non-zero exit is a result,
// as it is for Run. A run its context ended returns the context's error with
// the process ended, and with KillTree everything it started.
func (OSRunner) Stream(ctx context.Context, req Request, stdout, stderr io.Writer) (int, error) {
	cmd := command(ctx, req)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout, cmd.Stderr = stdout, stderr
	tree, err := start(cmd, req.KillTree)
	if err != nil {
		return -1, err
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return -1, cut(tree, err)
	}
	if err := release(tree); err != nil {
		return cmd.ProcessState.ExitCode(), err
	}
	var exitErr *exec.ExitError
	if waitErr == nil || errors.As(waitErr, &exitErr) && cmd.ProcessState.Exited() {
		return cmd.ProcessState.ExitCode(), nil
	}
	return -1, waitErr
}

// Start starts a child process and arranges for it to be reaped after exit.
// Startup callers intentionally receive no exit status because their own
// protocol-level health check is the authoritative readiness signal.
func (OSRunner) Start(ctx context.Context, req Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cmd := startCommand(req)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
	}()
	return nil
}

// Command is exec.Command for every process a fleet program starts: the
// process opens no console window (see hide).
func Command(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	hide(cmd)
	return cmd
}

// CommandContext is exec.CommandContext for every process a fleet program
// starts: the process opens no console window (see hide).
func CommandContext(ctx context.Context, name string, arg ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, arg...)
	hide(cmd)
	return cmd
}

func command(ctx context.Context, req Request) *exec.Cmd {
	cmd := CommandContext(ctx, req.Name, req.Args...)
	configure(cmd, req)
	return cmd
}

// start starts cmd, as a tree when isTree says so (Request.KillTree).
func start(cmd *exec.Cmd, isTree bool) (*Tree, error) {
	if !isTree {
		return nil, cmd.Start()
	}
	return StartTree(cmd)
}

// cut is the error of a run its context ended with err: a run started as a
// tree is ended with all of it, and the error names what that ended.
func cut(tree *Tree, err error) error {
	if tree == nil {
		return err
	}
	ended, endErr := tree.End()
	if endErr != nil {
		return errors.Join(&Ended{Err: err, Processes: ended}, fmt.Errorf("execx: what the run started may not all have ended: %w", endErr))
	}
	return &Ended{Err: err, Processes: ended}
}

// release lets go of the tree of a run whose process ended by itself.
func release(tree *Tree) error {
	if tree == nil {
		return nil
	}
	return tree.Release()
}

func startCommand(req Request) *exec.Cmd {
	cmd := Command(req.Name, req.Args...)
	configure(cmd, req)
	return cmd
}

func configure(cmd *exec.Cmd, req Request) {
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	if req.Env != nil {
		cmd.Env = req.Env
	}
}
