package main

import (
	"bytes"
	"context"
	"errors"
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

// Native no-mistakes prints a failed axi run or axi respond as the error
// field of the document it writes on stdout, and exits 1.
const (
	healthMissed    = "error: \"start daemon: read response: daemon health did not reply within 250ms (run 'no-mistakes daemon start' to recover)\"\n"
	startFreshLost  = "error: \"start fresh run: read response: daemon start_fresh_run did not reply within 30s\"\n"
	gateContextLost = "error: \"respond: read response: daemon gate_context did not reply within 30s\"\n"
	pendingBranchID = "01M75EC3X25Z07CEQP6R93VJVR"
)

// busyDaemonRunner is a task's gate whose native command answers with
// outcomes in order, the last repeating, while the gate's database shows
// liveRuns runs on the daemon, or cannot be read when liveRuns is -1, and
// branchRun is the latest run of the task's branch.
type busyDaemonRunner struct {
	execx.Runner
	outcomes  []execx.Result
	liveRuns  int
	branchRun string
	native    []execx.Request
}

func (r *busyDaemonRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	if q.Name == "sqlite3" {
		sql := q.Args[len(q.Args)-1]
		switch {
		case strings.Contains(sql, "NOT IN ('completed','failed','cancelled','ci_monitor_interrupted')"):
			if r.liveRuns < 0 {
				return execx.Result{}, errors.New("database is locked")
			}
			runs := make([]string, r.liveRuns)
			for i := range runs {
				runs[i] = fmt.Sprintf(`{"id":"01OTHER%d","branch":"fix/%d","status":"running","repo":"C:/%d"}`, i, i, i)
			}
			return execx.Result{Stdout: []byte("[" + strings.Join(runs, ",") + "]")}, nil
		case strings.Contains(sql, "AS worktree FROM runs JOIN repos"):
			if r.branchRun == "" {
				return execx.Result{Stdout: []byte(`[]`)}, nil
			}
			return execx.Result{Stdout: []byte(r.branchRun)}, nil
		}
	}
	result, err := r.Runner.Run(ctx, q)
	if q.Name != "no-mistakes" || q.Args[0] == "help" {
		return result, err
	}
	r.native = append(r.native, q)
	return r.outcomes[min(len(r.native), len(r.outcomes))-1], nil
}

// Issue 2158: a gate daemon busy serving other runs on a loaded machine
// misses its 250 ms health reply, or the reply to a start or an answer, while
// it is alive. cfo pipeline run used to stop on the first miss with native's
// hint to start the daemon, so goblins looped on retries of their own and
// could not tell whether a start had left a run behind.
func TestPipelineReadsABusyDaemonsMissedReplyByItsProcess(t *testing.T) {
	alive := fmt.Sprintf(`{"pid":%d,"started_at":"2026-11-11T12:57:48Z"}`, os.Getpid())
	pendingRun := `[{"id":"` + pendingBranchID + `","repo_id":"repo1","branch":"feat/policy","status":"pending","head":"0123456789abcdef0123456789abcdef01234567"}]`
	finishedRun := strings.Replace(pendingRun, `"pending"`, `"completed"`, 1)
	for _, scenario := range []struct {
		name         string
		command      string
		pidFile      string
		liveRuns     int
		outcomes     []execx.Result
		branchRun    string
		wantLaunches int
		wantSleeps   int
		wantErr      string
		wantSaid     string
	}{
		{
			name:         "a missed health reply waits its turn and starts",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}, {}},
			wantLaunches: 2,
			wantSleeps:   1,
			wantSaid:     "is alive and serving 2 runs, and missed its short health reply before this run reached it, so it is busy, not stopped; waiting 30s for its turn (try 2 of 4)",
		},
		{
			name:         "a missed health reply on stderr is read the same",
			command:      "run",
			pidFile:      alive,
			liveRuns:     1,
			outcomes:     []execx.Result{{ExitCode: 1, Stderr: []byte(healthMissed)}, {}},
			wantLaunches: 2,
			wantSleeps:   1,
			wantSaid:     "is alive and serving 1 run, and missed",
		},
		{
			name:         "a live daemon whose database cannot be read is still waited on",
			command:      "run",
			pidFile:      alive,
			liveRuns:     -1,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}, {}},
			wantLaunches: 2,
			wantSleeps:   1,
			wantSaid:     "is alive and serving runs its database could not count, and missed",
		},
		{
			name:         "health missed on every turn names no run left behind",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}},
			wantLaunches: 4,
			wantSleeps:   3,
			wantErr:      "is alive and serving 2 runs and missed its reply, so it is busy, not stopped: whatever native's hint above says, never start or restart it. No run was created on feat/policy, so repeating this command in a few minutes starts it",
		},
		{
			name:         "health missed on every turn after a finished run names no run left behind",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}},
			branchRun:    finishedRun,
			wantLaunches: 4,
			wantSleeps:   3,
			wantErr:      "No run was created on feat/policy",
		},
		{
			name:         "a lost start reply names the run it left on the branch",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(startFreshLost)}},
			branchRun:    pendingRun,
			wantLaunches: 1,
			wantErr:      "busy, not stopped: whatever native's hint above says, never start or restart it. Run " + pendingBranchID + " already holds feat/policy, so repeating this command in a few minutes reattaches to it and starts no second run",
		},
		{
			name:         "a lost start reply over an unreadable run claims no run either way",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(startFreshLost)}},
			branchRun:    strings.Replace(pendingRun, "0123456789abcdef0123456789abcdef01234567", "not-a-commit", 1),
			wantLaunches: 1,
			wantErr:      "busy, not stopped: whatever native's hint above says, never start or restart it. The run on feat/policy could not be read (gate identity could not be verified), so check it with no-mistakes axi status before you act again",
		},
		{
			name:         "a health miss after the run was created is not repeated",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte("error: \"drive run: reconcile run " + pendingBranchID + ": get_run timed out and daemon health probe failed: read response: daemon health did not reply within 250ms\"\n")}},
			branchRun:    pendingRun,
			wantLaunches: 1,
			wantErr:      "Run " + pendingBranchID + " already holds feat/policy, so repeating this command in a few minutes reattaches to it and starts no second run",
		},
		{
			name:         "a successful run that quotes the health miss in its document is not repeated",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{Stdout: []byte("findings[1]:\n  - description: \"" + strings.TrimSpace(healthMissed) + "\"\n")}},
			wantLaunches: 1,
		},
		{
			name:         "a failed run that quotes a missed reply in a finding keeps native's own failure",
			command:      "run",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte("error: \"review found blocking findings\"\nfindings[1]:\n  - description: \"" + strings.TrimSpace(healthMissed) + "\"\n")}},
			wantLaunches: 1,
			wantErr:      "pipeline: native command exited 1",
		},
		{
			name:         "a daemon with no process is not waited on or restarted",
			command:      "run",
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}},
			wantLaunches: 1,
			wantErr:      "the gate daemon missed its reply and its process reads stopped. It serves every goblin, so never start or restart it: ask the CEO with cfo notify task --blocked",
		},
		{
			name:         "a pid file that names no process is a question, not a wait",
			command:      "run",
			pidFile:      `{"pid":"daemon"}`,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}},
			wantLaunches: 1,
			wantErr:      "the gate daemon missed its reply and its process reads unknown: pipeline: the gate daemon's pid file names no process. It serves every goblin, so never start or restart it: ask the CEO with cfo notify task --blocked",
		},
		{
			name:         "a respond turned away at its health reply waits its turn and answers",
			command:      "respond",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}, {}},
			wantLaunches: 2,
			wantSleeps:   1,
			wantSaid:     "missed its short health reply before this respond reached it, so it is busy, not stopped; waiting 30s for its turn (try 2 of 4)",
		},
		{
			name:         "a respond whose reply was lost names the run it may have reached",
			command:      "respond",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(gateContextLost)}},
			branchRun:    pendingRun,
			wantLaunches: 1,
			wantErr:      "busy, not stopped: whatever native's hint above says, never start or restart it. Your answer may have reached run " + pendingBranchID + ", so read it with no-mistakes axi status --run " + pendingBranchID + " before you answer again",
		},
		{
			name:         "a respond whose reply was lost on a finished run says the branch holds none",
			command:      "respond",
			pidFile:      alive,
			liveRuns:     2,
			outcomes:     []execx.Result{{ExitCode: 1, Stdout: []byte(gateContextLost)}},
			branchRun:    finishedRun,
			wantLaunches: 1,
			wantErr:      "busy, not stopped: whatever native's hint above says, never start or restart it. feat/policy holds no live run now, so read it with no-mistakes axi status before you act again",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			h, nm, worktree := busyDaemonTask(t)
			if scenario.pidFile != "" {
				if err := os.WriteFile(filepath.Join(nm, "daemon.pid"), []byte(scenario.pidFile), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			gate := execx.Runner(&pipelineStartRunner{worktree: worktree})
			args := []string{"run", "task", "--intent", "ship safely"}
			if scenario.command == "respond" {
				gate = &pipelineRunner{worktree: worktree, gate: pipeline.Gate{RunID: pendingBranchID, StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"style","action":"no-op"}]}`}}
				args = []string{"respond", "task", "--action", "approve"}
			}
			runner := &busyDaemonRunner{Runner: gate, outcomes: scenario.outcomes, liveRuns: scenario.liveRuns, branchRun: scenario.branchRun}
			var waits []time.Duration
			previousSleep := daemonTurnSleep
			daemonTurnSleep = func(wait time.Duration) { waits = append(waits, wait) }
			t.Cleanup(func() { daemonTurnSleep = previousSleep })
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, args, &out)

			// Assert
			if len(runner.native) != scenario.wantLaunches || len(waits) != scenario.wantSleeps {
				t.Fatalf("native launches=%d waits=%v, want %d launches and %d waits; said:\n%s", len(runner.native), waits, scenario.wantLaunches, scenario.wantSleeps, out.String())
			}
			for _, launch := range runner.native[1:] {
				if !slices.Equal(launch.Args, runner.native[0].Args) {
					t.Fatalf("a repeated %s changed its request: %q then %q", scenario.command, runner.native[0].Args, launch.Args)
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
