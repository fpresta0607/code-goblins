package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

func TestExecutorExecutionLimitCoversStartupAndFinalization(t *testing.T) {
	for _, stage := range []string{"startup", "launch", "finalization"} {
		t.Run(stage, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
			root := t.TempDir()
			standin.RemoveAtCleanup(t, root)
			program := filepath.Join(root, "workload.exe")
			standin.Put(t, program)
			ran := filepath.Join(root, "ran.txt")
			executor := Executor{Admission: admission(t), Monitor: Monitor{Poll: 10 * time.Millisecond, Headroom: plentyOfMemory}, ExecutionLimit: 3 * time.Second, IsConsoleShared: true}
			var deadline time.Time
			waitForLimit := func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(30 * time.Second):
					return errors.New("the execution deadline never arrived")
				}
			}
			executor.Ready = func(ctx context.Context) error {
				var isBounded bool
				deadline, isBounded = ctx.Deadline()
				if !isBounded {
					return errors.New("startup has no execution deadline")
				}
				if stage == "startup" {
					return waitForLimit(ctx)
				}
				return nil
			}
			reads := 0
			executor.Source = func(ctx context.Context, _ string) (Source, error) {
				reads++
				if readDeadline, _ := ctx.Deadline(); !readDeadline.Equal(deadline) {
					return Source{}, errors.New("finalization reset or lost the shared execution deadline")
				}
				if reads == 2 || stage == "launch" {
					return Source{}, waitForLimit(ctx)
				}
				return Source{Commit: "c1", Tree: "t1", IsComplete: true}, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			// Act
			result, path, err := executor.RunWithin(ctx, execx.Request{Dir: root, Name: program, Env: append(os.Environ(), standin.Env(ran, standin.Rule{Any: true, Exit: 0})...)}, 0)

			// Assert
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("result = %+v, %v; want an outer execution timeout", result, err)
			}
			record := readExecution(t, path)
			if !strings.Contains(record.Failure, ErrLimit.Error()) || !record.Measurement.CleanupConfirmed || record.Status == "passed" {
				t.Fatalf("receipt = %+v; want a timeout with cleanup confirmed", record)
			}
			if stage != "finalization" {
				if _, err := os.Stat(ran); !os.IsNotExist(err) || record.Status != "not_started" || record.ExitCode != nil {
					t.Fatalf("startup ran a command or invented an exit: %+v, %v", record, err)
				}
			} else if reads != 2 || result.ExitCode != 0 || record.Status != "failed" || record.Measurement.State != Interrupted {
				t.Fatalf("finalization = %d reads, %+v, %+v; want the zero-exit command's run to fail", reads, result, record)
			}
			if holders, waiters, err := Line(executor.Admission.Dir); err != nil || len(holders)+len(waiters) != 0 {
				t.Fatalf("custody leaked: %v, %v, %v", holders, waiters, err)
			}
		})
	}
}

// The limit has one timer, and a loaded machine can run it late. Startup ends
// on that timer and no other, or it ends before the context that names the
// limit does and reports a bare deadline.
func TestExecutorStartupNamesTheLimitWhenItsTimerRunsLate(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
	isFirstTimer := true
	limitTimer = func(parent context.Context, deadline time.Time, cause error) (context.Context, context.CancelFunc) {
		if isFirstTimer {
			isFirstTimer = false
			deadline = deadline.Add(2 * time.Second)
		}
		return context.WithDeadlineCause(parent, deadline, cause)
	}
	t.Cleanup(func() { limitTimer = context.WithDeadlineCause })
	executor := Executor{Admission: admission(t), Monitor: Monitor{Headroom: plentyOfMemory}, ExecutionLimit: 100 * time.Millisecond, IsConsoleShared: true}
	executor.Ready = func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return errors.New("the execution deadline never arrived")
		}
	}

	// Act
	result, path, err := executor.RunWithin(context.Background(), execx.Request{Dir: t.TempDir(), Name: "must-not-start"}, 0)

	// Assert
	if !errors.Is(err, ErrLimit) || !errors.Is(err, ErrNotStarted) || result.ExitCode != -1 {
		t.Fatalf("result = %+v, %v; want the limit named on a command that never started", result, err)
	}
	if record := readExecution(t, path); record.Status != "not_started" || record.ExitCode != nil || !strings.Contains(record.Failure, ErrLimit.Error()) {
		t.Fatalf("receipt = %+v; want a refusal that names the limit", record)
	}
}

func TestExecutorExecutionLimitKeepsStartupInterruptible(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executor := Executor{Admission: admission(t), Monitor: Monitor{Headroom: plentyOfMemory}, IsConsoleShared: true, ExecutionLimit: time.Minute}
	executor.Ready = func(startup context.Context) error {
		cancel()
		select {
		case <-startup.Done():
			return startup.Err()
		case <-time.After(30 * time.Second):
			return errors.New("startup ignored the interrupt")
		}
	}

	// Act
	result, path, err := executor.RunWithin(ctx, execx.Request{Dir: t.TempDir(), Name: "must-not-start"}, 0)

	// Assert
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrLimit) || result.ExitCode != -1 {
		t.Fatalf("startup interrupt = %+v, %v; want cancellation before launch", result, err)
	}
	if record := readExecution(t, path); !strings.Contains(record.Failure, context.Canceled.Error()) || strings.Contains(record.Failure, "ignored the interrupt") {
		t.Fatalf("receipt = %+v; startup did not keep the caller's interrupt", record)
	}
}
