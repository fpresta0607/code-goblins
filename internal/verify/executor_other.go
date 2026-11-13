//go:build !windows

package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Executor would run commands as measured executions in the machine's line
// for heavy runs, which this system does not run yet: a measured execution
// holds its command's processes in a Windows job object. Its fields are the
// Windows Executor's.
type Executor struct {
	Admission       Admission
	Monitor         Monitor
	ExecutionLimit  time.Duration
	Stdin           *os.File
	Stdout, Stderr  *os.File
	OutputLimit     int64
	IsConsoleShared bool
	Took            func(waited time.Duration)
	Ready           func(ctx context.Context) error
	Source          func(ctx context.Context, dir string) (Source, error)
}

// errExecutorNotPorted is what every run returns on this system.
var errExecutorNotPorted = fmt.Errorf("verify: measured executions are not ported to this system yet: %w", errors.ErrUnsupported)

// Run refuses and starts nothing, since measured executions are not ported
// yet.
func (executor *Executor) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	return execx.Result{}, fmt.Errorf("%w: %w", ErrNotStarted, errExecutorNotPorted)
}

// RunWithin refuses, as Run does.
func (executor *Executor) RunWithin(ctx context.Context, request execx.Request, limit time.Duration) (execx.Result, string, error) {
	return execx.Result{}, "", fmt.Errorf("%w: %w", ErrNotStarted, errExecutorNotPorted)
}

// RetryCleanup has nothing to retry, since no run started.
func (executor *Executor) RetryCleanup(ctx context.Context) error { return nil }
