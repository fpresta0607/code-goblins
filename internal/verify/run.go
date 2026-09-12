package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/project"
	"os"
	"path/filepath"
	"time"
)

type Result struct {
	Command         []string  `json:"command"`
	Start           time.Time `json:"start"`
	DurationSeconds float64   `json:"duration_seconds"`
	ExitCode        int       `json:"exit_code"`
	Scope           string    `json:"scope"`
	Task            string    `json:"task,omitempty"`
	Commit          string    `json:"commit,omitempty"`
	TimedOut        bool      `json:"timed_out,omitempty"`
	Output          string    `json:"output,omitempty"`
}
type Runner struct{ Commands execx.Runner }

func (r Runner) Run(ctx context.Context, commands []project.Command, scope, task, commit, dir string, timeout time.Duration) ([]Result, error) {
	var out []Result
	for _, c := range commands {
		if len(c) == 0 {
			continue
		}
		cctx := ctx
		var cancel context.CancelFunc
		if timeout > 0 {
			cctx, cancel = context.WithTimeout(ctx, timeout)
		}
		start := time.Now()
		res, err := r.Commands.Run(cctx, execx.Request{Name: c[0], Args: c[1:], Dir: dir})
		if cancel != nil {
			cancel()
		}
		rr := Result{Command: append([]string(nil), c...), Start: start, DurationSeconds: time.Since(start).Seconds(), Scope: scope, Task: task, Commit: commit}
		if err != nil {
			rr.ExitCode = -1
			rr.Output = err.Error()
			if cctx.Err() == context.DeadlineExceeded {
				rr.TimedOut = true
			}
			out = append(out, rr)
			return out, fmt.Errorf("verify %s: %w", c[0], err)
		}
		rr.ExitCode = res.ExitCode
		rr.Output = string(res.Stdout)
		out = append(out, rr)
		if res.ExitCode != 0 {
			return out, fmt.Errorf("verify %s exited %d", c[0], res.ExitCode)
		}
	}
	return out, nil
}
func Save(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
