// Package axi provides thin subprocess integrations for AXI tools.
package axi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Tasks invokes tasks-axi without interpreting task bodies.
type Tasks struct {
	Commands execx.Runner
}

// ShowFull returns the complete task body exactly as tasks-axi writes it.
func (t Tasks) ShowFull(ctx context.Context, id string) (string, error) {
	result, err := command(ctx, t.Commands, "tasks-axi show "+id+" --full", "tasks-axi", "show", id, "--full")
	if err != nil {
		return "", err
	}
	return string(result.Stdout), nil
}

// Quota invokes quota-axi without interpreting its JSON output.
type Quota struct {
	Commands execx.Runner
}

// CommandError identifies an AXI command failure and preserves its stderr.
type CommandError struct {
	Operation string
	Stderr    string
	ExitCode  int
	Err       error
}

func (e *CommandError) Error() string {
	if e.Err != nil {
		if e.Stderr == "" {
			return fmt.Sprintf("axi: %s: %v", e.Operation, e.Err)
		}
		return fmt.Sprintf("axi: %s: %s: %v", e.Operation, e.Stderr, e.Err)
	}
	if e.Stderr == "" {
		return fmt.Sprintf("axi: %s exited with code %d", e.Operation, e.ExitCode)
	}
	return fmt.Sprintf("axi: %s exited with code %d: %s", e.Operation, e.ExitCode, e.Stderr)
}

// Unwrap returns the subprocess runner error, when present.
func (e *CommandError) Unwrap() error {
	return e.Err
}

// JSON returns quota-axi's raw JSON bytes.
func (q Quota) JSON(ctx context.Context) ([]byte, error) {
	result, err := command(ctx, q.Commands, "quota-axi --json", "quota-axi", "--json")
	if err != nil {
		return nil, err
	}
	return result.Stdout, nil
}

func command(ctx context.Context, commands execx.Runner, operation, name string, args ...string) (execx.Result, error) {
	if commands == nil {
		return execx.Result{}, errors.New("axi: command runner is required")
	}
	result, err := commands.Run(ctx, execx.Request{Name: name, Args: args})
	if err != nil {
		return execx.Result{}, commandError(operation, result, err)
	}
	if result.ExitCode != 0 {
		return execx.Result{}, commandError(operation, result, nil)
	}
	return result, nil
}

func commandError(operation string, result execx.Result, cause error) error {
	return &CommandError{
		Operation: operation,
		Stderr:    strings.TrimSpace(string(result.Stderr)),
		ExitCode:  result.ExitCode,
		Err:       cause,
	}
}
