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

// OSRunner executes processes through the operating system.
type OSRunner struct{}

// Run starts and waits for the requested process. A normal non-zero exit is a
// result, not an execution error, so callers can distinguish tool refusals
// from failures to start or wait for the process.
func (OSRunner) Run(ctx context.Context, req Request) (Result, error) {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	if req.Env != nil {
		cmd.Env = req.Env
	}

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
