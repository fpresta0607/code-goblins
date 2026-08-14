// Package execx provides the small subprocess seam used by CFO integrations.
package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Request describes one child process invocation.
type Request struct {
	Dir  string
	Env  []string
	Name string
	Args []string
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
// from failures to start or wait for the process.
func (OSRunner) Run(ctx context.Context, req Request) (Result, error) {
	cmd := command(ctx, req)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	result := Result{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
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

func command(ctx context.Context, req Request) *exec.Cmd {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	configure(cmd, req)
	return cmd
}

func startCommand(req Request) *exec.Cmd {
	cmd := exec.Command(req.Name, req.Args...)
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
