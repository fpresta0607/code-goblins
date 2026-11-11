package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	healthMissed    = "error: \"start daemon: read response: daemon health did not reply within 250ms (run 'no-mistakes daemon start' to recover)\"\n"
	startFreshLost  = "error: \"start fresh run: read response: daemon start_fresh_run did not reply within 30s\"\n"
	pendingBranchID = "01M75EC3X25Z07CEQP6R93VJVR"
)

// busyDaemonRunner is a task start whose native command answers with
// outcomes in order, the last repeating, while the gate's database shows two
// runs live on the daemon and, when branchRun is set, the run the task's
// branch already holds.
type busyDaemonRunner struct {
	pipelineStartRunner
	outcomes  []execx.Result
	branchRun string
}

func (r *busyDaemonRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	if q.Name == "sqlite3" {
		sql := q.Args[len(q.Args)-1]
		switch {
		case strings.Contains(sql, "NOT IN ('completed','failed','cancelled','ci_monitor_interrupted')"):
			return execx.Result{Stdout: []byte(`[{"id":"01OTHERA","branch":"fix/a","status":"running","repo":"C:/a"},{"id":"01OTHERB","branch":"fix/b","status":"running","repo":"C:/b"}]`)}, nil
		case strings.Contains(sql, "runs.worktree_dir"):
			if r.branchRun == "" {
				return execx.Result{Stdout: []byte(`[]`)}, nil
			}
			return execx.Result{Stdout: []byte(r.branchRun)}, nil
		}
	}
	result, err := r.pipelineStartRunner.Run(ctx, q)
	if q.Name != "no-mistakes" || slices.Equal(q.Args, []string{"help", "axi", "run"}) {
		return result, err
	}
	return r.outcomes[min(len(r.native)-1, len(r.outcomes)-1)], nil
}

// Issue 2158: a gate daemon busy serving other runs on a loaded machine
// misses its 250 ms health reply, or the reply to a start, while it is alive.
// cfo pipeline run used to stop on the first miss with native's hint to start
// the daemon, so goblins looped on retries of their own and could not tell
// whether a start had left a run behind.
func TestPipelineRunReadsABusyDaemonsMissedReplyByItsProcess(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		isAlive      bool
		outcomes     []execx.Result
		branchRun    string
		wantLaunches int
		wantSleeps   int
		wantErr      string
		wantSaid     string
	}{
		{
			name:         "a missed health reply waits its turn and starts",
			isAlive:      true,
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte(healthMissed)}, {}},
			wantLaunches: 2,
			wantSleeps:   1,
			wantSaid:     "is alive and serving 2 runs, and missed its short health reply before this run reached it, so it is busy, not stopped; waiting 30s for its turn (try 2 of 4)",
		},
		{
			name:         "health missed on every turn names no run left behind",
			isAlive:      true,
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte(healthMissed)}},
			wantLaunches: 4,
			wantSleeps:   3,
			wantErr:      "is alive and serving 2 runs and missed its reply, so it is busy, not stopped: whatever native's hint above says, never start or restart it. No run was created on feat/policy, so repeating this command in a few minutes starts it",
		},
		{
			name:         "a lost start reply names the run it left on the branch",
			isAlive:      true,
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte(startFreshLost)}},
			branchRun:    `[{"id":"` + pendingBranchID + `","repo_id":"repo1","branch":"feat/policy","status":"pending","head":"0123456789abcdef0123456789abcdef01234567"}]`,
			wantLaunches: 1,
			wantErr:      "busy, not stopped: whatever native's hint above says, never start or restart it. Run " + pendingBranchID + " already holds feat/policy, so repeating this command in a few minutes reattaches to it and starts no second run",
		},
		{
			name:         "a health miss after the run was created is not repeated",
			isAlive:      true,
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte("error: \"drive run: reconcile run " + pendingBranchID + ": get_run timed out and daemon health probe failed: read response: daemon health did not reply within 250ms\"\n")}},
			branchRun:    `[{"id":"` + pendingBranchID + `","repo_id":"repo1","branch":"feat/policy","status":"pending","head":"0123456789abcdef0123456789abcdef01234567"}]`,
			wantLaunches: 1,
			wantErr:      "Run " + pendingBranchID + " already holds feat/policy, so repeating this command in a few minutes reattaches to it and starts no second run",
		},
		{
			name:         "a daemon with no process is not waited on or restarted",
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte(healthMissed)}},
			wantLaunches: 1,
			wantErr:      "the gate daemon missed its reply and its process reads stopped. It serves every goblin, so never start or restart it: ask the CEO with cfo notify task --blocked",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			h, nm, worktree := busyDaemonTask(t)
			if scenario.isAlive {
				record := fmt.Sprintf(`{"pid":%d,"started_at":"2026-11-11T12:57:48Z"}`, os.Getpid())
				if err := os.WriteFile(filepath.Join(nm, "daemon.pid"), []byte(record), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runner := &busyDaemonRunner{pipelineStartRunner: pipelineStartRunner{worktree: worktree}, outcomes: scenario.outcomes, branchRun: scenario.branchRun}
			var waits []time.Duration
			previousSleep := daemonTurnSleep
			daemonTurnSleep = func(wait time.Duration) { waits = append(waits, wait) }
			t.Cleanup(func() { daemonTurnSleep = previousSleep })
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out)

			// Assert
			if len(runner.native) != scenario.wantLaunches || len(waits) != scenario.wantSleeps {
				t.Fatalf("native launches=%d waits=%v, want %d launches and %d waits; said:\n%s", len(runner.native), waits, scenario.wantLaunches, scenario.wantSleeps, out.String())
			}
			for _, launch := range runner.native[1:] {
				if !slices.Equal(launch.Args, runner.native[0].Args) {
					t.Fatalf("a repeated start changed its request: %q then %q", runner.native[0].Args, launch.Args)
				}
			}
			if scenario.wantErr == "" {
				if err != nil {
					t.Fatalf("pipelineCommand: %v; said:\n%s", err, out.String())
				}
			} else if err == nil || !strings.Contains(err.Error(), scenario.wantErr) {
				t.Fatalf("error = %v, want it to say %q", err, scenario.wantErr)
			}
			if !strings.Contains(out.String(), scenario.wantSaid) {
				t.Fatalf("said:\n%s\nwant %q", out.String(), scenario.wantSaid)
			}
			if scenario.wantSleeps > 0 && strings.Count(out.String(), "no-mistakes daemon start") > 1 {
				t.Fatalf("each waited turn repeated native's hint to start a live daemon:\n%s", out.String())
			}
		})
	}
}

// busyDaemonTask is a no-mistakes task ready to start its gate, with the gate
// home it starts in and its worktree.
func busyDaemonTask(t *testing.T) (home.Home, string, string) {
	t.Helper()
	p, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	selection, err := p.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	worktree := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{nm, tmp, project, worktree} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: worktree, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return h, nm, worktree
}
