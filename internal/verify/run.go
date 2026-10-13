package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/project"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	Command         []string  `json:"command"`
	Start           time.Time `json:"start,omitzero"`
	DurationSeconds float64   `json:"duration_seconds"`
	ExitCode        int       `json:"exit_code"`
	Scope           string    `json:"scope"`
	Task            string    `json:"task,omitempty"`
	Commit          string    `json:"commit,omitempty"`
	Status          string    `json:"status,omitempty"`
	TimedOut        bool      `json:"timed_out,omitempty"`
	Output          string    `json:"output,omitempty"`
	// Error is why the command's run failed beyond its exit code, as a
	// limit that ended it or a turn it never got.
	Error string `json:"error,omitempty"`
	// Execution is the path of the command's measured execution record,
	// for a command run as one, Leftovers what the command left running
	// past its linger, which the run ended, and Spared the machine services
	// it started, which the run left running.
	Execution string     `json:"execution,omitempty"`
	Leftovers []Leftover `json:"leftovers,omitempty"`
	Spared    []Leftover `json:"spared,omitempty"`
	// Packages are what became of each package a test command ran.
	Packages []PackageResult `json:"packages,omitempty"`
}

// PackageResult is what became of one package's tests: passed, failed,
// build_failed when it did not compile, no_tests when it has no test files,
// or unfinished when its tests were still running as the run ended. Tests is
// how many top-level tests started, Failed names the tests that failed and
// Unfinished the ones that started and never ended, as a test that hangs.
type PackageResult struct {
	Package    string   `json:"package"`
	Status     string   `json:"status"`
	Seconds    float64  `json:"seconds"`
	Tests      int      `json:"tests,omitempty"`
	Failed     []string `json:"failed,omitempty"`
	Unfinished []string `json:"unfinished,omitempty"`
}

type Runner struct{ Commands execx.Runner }

// measuredRunner runs each command as a measured execution whose limit
// starts with the command rather than with its wait for a turn.
type measuredRunner interface {
	RunWithin(ctx context.Context, request execx.Request, limit time.Duration) (execx.Result, string, error)
}

// Run runs a tier's commands in order until one fails. A tier that names no
// command, or names an empty one, is refused rather than passed.
func (r Runner) Run(ctx context.Context, commands []project.Command, scope, task, commit, dir string, timeout time.Duration) ([]Result, error) {
	if len(commands) == 0 {
		return nil, fmt.Errorf("verify: the %s tier names no command", scope)
	}
	// A tier with an empty command runs none of its commands, so its result
	// never reads as a pass of the ones before it.
	for _, c := range commands {
		if len(c) == 0 || c[0] == "" {
			return nil, fmt.Errorf("verify: the %s tier names an empty command", scope)
		}
	}
	var out []Result
	for _, c := range commands {
		start := time.Now()
		// A tier's command starts the checks it runs, so a run cut off ends
		// them with it: ending the command alone leaves them running with
		// nothing left to end them.
		request := execx.Request{Name: c[0], Args: c[1:], Dir: dir, KillTree: true}
		var res execx.Result
		var record string
		var err error
		var isTimedOut bool
		if measured, isMeasured := r.Commands.(measuredRunner); isMeasured {
			res, record, err = measured.RunWithin(ctx, request, timeout)
			isTimedOut = errors.Is(err, ErrLimit)
		} else {
			cctx := ctx
			var cancel context.CancelFunc
			if timeout > 0 {
				cctx, cancel = context.WithTimeout(ctx, timeout)
			}
			res, err = r.Commands.Run(cctx, request)
			if cancel != nil {
				cancel()
			}
			isTimedOut = cctx.Err() == context.DeadlineExceeded
			if err != nil {
				// Only a measured run's result holds an exit code beside
				// its error.
				res.ExitCode = -1
			}
		}
		rr := Result{Command: append([]string(nil), c...), Start: start, DurationSeconds: time.Since(start).Seconds(), Scope: scope, Task: task, Commit: commit, Execution: record}
		if err != nil {
			rr.ExitCode = res.ExitCode
			rr.Output = string(res.Stdout)
			rr.Error = err.Error()
			rr.TimedOut = isTimedOut
			rr.Status = "failed"
			if isTimedOut {
				rr.Status = "timed_out"
			}
			out = append(out, rr)
			return out, fmt.Errorf("verify %s: %w", c[0], err)
		}
		rr.ExitCode = res.ExitCode
		rr.Output = string(res.Stdout)
		rr.Status = "passed"
		if res.ExitCode != 0 {
			rr.Status = "failed"
		}
		out = append(out, rr)
		if res.ExitCode != 0 {
			return out, fmt.Errorf("verify %s exited %d", c[0], res.ExitCode)
		}
	}
	return out, nil
}
func Save(path string, v any) error {
	return save(context.Background(), path, v)
}

func save(ctx context.Context, path string, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return fsx.AtomicWriteFileContext(ctx, path, append(b, '\n'))
}
