package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/worktree"
	"gopkg.in/yaml.v3"
)

type pipelineRunner struct {
	gate        pipeline.Gate
	native      []execx.Request
	rounds      []byte
	roundsAfter []byte
	roundsReads int
	// scopeDecision is the scope decision file the scoped native response was
	// handed, with the run it named, read while the command was still running.
	scopeDecision []byte
	scopeRun      string
	worktree      string
	activeRuns    int
	nativeResult  *execx.Result
	nativeError   error
	// nativeOutcomes, when set, answers native commands in order, the last
	// repeating.
	nativeOutcomes []execx.Result
}

type pipelineStartRunner struct {
	worktree    string
	advance     bool
	remoteRead  int
	roleStarted bool
	native      []execx.Request
	// previous is the branch's latest run as CheckStart reads it: "[]" when
	// the branch has none, and a one-element array otherwise.
	previous []byte
	handback []byte
	// head is the commit the task branch holds.
	head string
	// repoConfig, when set, is the .no-mistakes.yaml the task branch holds.
	repoConfig string
}

func (r *pipelineStartRunner) Run(_ context.Context, q execx.Request) (execx.Result, error) {
	const trusted = "0123456789abcdef0123456789abcdef01234567"
	switch q.Name {
	case "sqlite3":
		sql := q.Args[len(q.Args)-1]
		if strings.Contains(sql, "JOIN ci_handbacks") {
			if len(r.handback) == 0 {
				return execx.Result{Stdout: []byte(`[]`)}, nil
			}
			return execx.Result{Stdout: r.handback}, nil
		}
		if strings.Contains(sql, "SELECT default_branch FROM repos") {
			return execx.Result{Stdout: []byte(`[{"default_branch":"main"}]`)}, nil
		}
		if strings.Contains(sql, "FROM runs JOIN repos") && strings.Contains(sql, "AS submitted_head") {
			if len(r.previous) == 0 {
				return execx.Result{Stdout: []byte(`[]`)}, nil
			}
			return execx.Result{Stdout: r.previous}, nil
		}
	case "git":
		switch strings.Join(q.Args, " ") {
		case "rev-parse HEAD":
			head := r.head
			if head == "" {
				head = trusted
			}
			return execx.Result{Stdout: []byte(head + "\n")}, nil
		case "rev-parse --show-toplevel":
			return execx.Result{Stdout: []byte(r.worktree + "\n")}, nil
		case "symbolic-ref --quiet --short HEAD":
			return execx.Result{Stdout: []byte("feat/policy\n")}, nil
		case "status --porcelain --untracked-files=all":
			return execx.Result{}, nil
		case "show HEAD:.no-mistakes.yaml":
			if r.repoConfig != "" {
				return execx.Result{Stdout: []byte(r.repoConfig)}, nil
			}
			return execx.Result{Stdout: []byte("auto_fix: {review: 0, rebase: 1, ci: 0}\n")}, nil
		case "ls-remote --symref origin HEAD":
			head := trusted
			if r.advance && r.remoteRead > 1 {
				head = "89abcdef0123456789abcdef0123456789abcdef"
			}
			r.remoteRead++
			return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n" + head + "\tHEAD\n")}, nil
		case "rev-parse --verify refs/remotes/origin/main":
			return execx.Result{Stdout: []byte(trusted + "\n")}, nil
		case "show refs/remotes/origin/main:.no-mistakes.yaml":
			return execx.Result{Stdout: []byte("auto_fix: {review: 0}\n")}, nil
		}
	case "no-mistakes":
		// This no-mistakes names no --intent-file in its help, as one before
		// 1.86.0, so a run it starts has its intent on the command line.
		if slices.Equal(q.Args, []string{"help", "axi", "run"}) {
			return execx.Result{}, nil
		}
		r.native = append(r.native, q)
		r.roleStarted = r.advance && r.remoteRead >= 2
		return execx.Result{}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected start command: %#v", q)
}

// pipelineSwitchRunner holds a switch at its worktree status read, inside
// its metadata lock, until the test releases it.
type pipelineSwitchRunner struct {
	statusReady   chan struct{}
	statusRelease chan struct{}
}

func (r *pipelineSwitchRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	if q.Name == "git" && len(q.Args) > 0 && q.Args[0] == "status" {
		close(r.statusReady)
		select {
		case <-r.statusRelease:
			return execx.Result{}, nil
		case <-ctx.Done():
			return execx.Result{}, ctx.Err()
		}
	}
	if q.Name == "git" {
		return execx.Result{}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected switch command: %#v", q)
}

type pipelineSwitchGit struct {
	worktree string
}

func (g pipelineSwitchGit) Acquire(context.Context, string, string, string) (string, error) {
	return "", errors.New("unexpected worktree acquisition")
}

func (pipelineSwitchGit) Landing(context.Context, string) (worktree.Landing, error) {
	return worktree.Landing{}, errors.New("unexpected landing check")
}

func (pipelineSwitchGit) ArchiveTag(context.Context, string, worktree.Landing, string) (string, error) {
	return "", errors.New("unexpected archive tag")
}

func (g pipelineSwitchGit) WorktreeTop(context.Context, string) (string, error) {
	return g.worktree, nil
}

func (pipelineSwitchGit) Return(context.Context, string, string) error {
	return errors.New("unexpected worktree return")
}

func (pipelineSwitchGit) EnsureSeeded(context.Context, string) (bool, error) {
	return false, errors.New("unexpected repository seeding")
}

type pipelineSwitchAdapter struct {
	kind harness.Kind
}

func (a pipelineSwitchAdapter) Kind() harness.Kind {
	return a.kind
}

func (pipelineSwitchAdapter) Validate(context.Context, execx.Runner) error {
	return nil
}

func (a pipelineSwitchAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	return harness.Launch{
		Args:       []string{"--test"},
		Env:        map[string]string{"GOTMPDIR": spec.Scratch, "TEMP": spec.Scratch, "TMP": spec.Scratch},
		PromptFile: spec.BriefPath,
		Executable: string(a.kind),
	}, nil
}

func (pipelineSwitchAdapter) Control() harness.Control {
	return harness.Control{}
}

func (r *pipelineRunner) Run(_ context.Context, q execx.Request) (execx.Result, error) {
	switch q.Name {
	case "git":
		if q.Args[0] == "rev-parse" {
			return execx.Result{Stdout: []byte(r.worktree)}, nil
		}
		if q.Args[0] == "symbolic-ref" {
			return execx.Result{Stdout: []byte("feat/policy")}, nil
		}
	case "sqlite3":
		if strings.Contains(q.Args[len(q.Args)-1], "FROM runs LEFT JOIN repos") {
			runs := make([]map[string]string, r.activeRuns)
			for i := range runs {
				runs[i] = map[string]string{"id": fmt.Sprintf("01RUN%d", i), "branch": "feat/other", "status": "running", "repo": "C:/work/other"}
			}
			data, err := json.Marshal(runs)
			return execx.Result{Stdout: data}, err
		}
		data, err := json.Marshal([]pipeline.Gate{r.gate})
		return execx.Result{Stdout: data}, err
	case "no-mistakes":
		if slices.Equal(q.Args, []string{"axi", "rounds", "--step", "review", "--run", r.gate.RunID, "--json"}) {
			r.roundsReads++
			if r.roundsAfter != nil && r.roundsReads > 1 {
				return execx.Result{Stdout: r.roundsAfter}, nil
			}
			if r.rounds != nil {
				return execx.Result{Stdout: r.rounds}, nil
			}
		}
		if at := slices.Index(q.Args, "--scope-decision-file"); at >= 0 && at+1 < len(q.Args) {
			data, err := os.ReadFile(q.Args[at+1])
			if err != nil {
				return execx.Result{}, err
			}
			r.scopeDecision = data
			if runAt := slices.Index(q.Args, "--run"); runAt >= 0 && runAt+1 < len(q.Args) {
				r.scopeRun = q.Args[runAt+1]
			}
		}
		r.native = append(r.native, q)
		if r.nativeOutcomes != nil {
			return r.nativeOutcomes[min(len(r.native), len(r.nativeOutcomes))-1], nil
		}
		if r.nativeResult != nil {
			return *r.nativeResult, r.nativeError
		}
		return execx.Result{Stdout: []byte("native decision output\n")}, nil
	}
	return execx.Result{}, errors.New("unexpected command")
}

func legacyPipelineSelection(t *testing.T, class string) pipeline.Selection {
	t.Helper()
	policy := pipeline.Policy{
		Version:  1,
		Reviewer: pipeline.Reviewer{Harness: "claude", Model: "opus", Effort: "high"},
		AutoFix:  pipeline.AutoFix{Review: 0, Test: 1, Lint: 1, Rebase: 1, CI: 1},
		Classes: pipeline.Classes{
			Ordinary:   pipeline.Class{ReviewCycles: 2},
			HighRisk:   pipeline.Class{ReviewCycles: 3},
			Mechanical: pipeline.Class{ReviewCycles: 2},
		},
	}
	selection, err := policy.Select(class)
	if err != nil {
		t.Fatal(err)
	}
	return selection
}

func TestPipelineMigrateRefusesATargetTheSharedConfigDoesNotRun(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	for _, path := range []string{filepath.Join(root, "config"), nm, tmp} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	// The machine still runs the task's old policy, so the current one is a
	// target nothing runs yet.
	config, _, err := pipeline.Render([]byte("{}"), old.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(tmp, "pipeline.json")
	if err := old.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	err = migratePipelinePolicy(h, nm, meta, old, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "apply current shared config before migrating tasks") {
		t.Fatalf("migration error=%v, want config drift refusal", err)
	}
	unchanged, err := pipeline.LoadSelection(snapshot)
	if err != nil || unchanged != old {
		t.Fatalf("refused migration changed snapshot: %+v %v", unchanged, err)
	}
	updated, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || updated.PipelineHash != old.Hash || updated.PipelineClass != old.Class {
		t.Fatalf("refused migration changed metadata: %+v %v", updated, err)
	}
	if _, err := os.Stat(filepath.Join(tmp, policyMigrationJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused migration left journal: %v", err)
	}
	if status, err := state.TailStatus(h.State, meta.ID, 1); err != nil || len(status) != 0 {
		t.Fatalf("refused migration wrote audit: %v %v", status, err)
	}
}

func TestPipelineMigrateReplacesOnlyFrozenPolicyAndAudits(t *testing.T) {
	// A migration changes only the task's own snapshot, so a run in flight
	// elsewhere on the machine never holds it up.
	for _, test := range []struct {
		name           string
		activeRuns     int
		isVersionTwo   bool
		isVersionThree bool
		isVersionSix   bool
	}{
		{name: "active-legacy", activeRuns: 1},
		{name: "idle"},
		{name: "active-v2", activeRuns: 1, isVersionTwo: true},
		{name: "idle-v2", isVersionTwo: true},
		{name: "active-v3", activeRuns: 2, isVersionThree: true},
		// A home that kept version 6 moves its tasks to the checked-in
		// version 7, whose gate repairs no Test, Lint or CI failure.
		{name: "active-v6", activeRuns: 1, isVersionSix: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			nm := filepath.Join(root, "nm")
			tmp := filepath.Join(h.State, "tasktmp", "task")
			project := filepath.Join(root, "project")
			wt := filepath.Join(project, ".worktrees", "gb-task")
			for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
				t.Fatal(err)
			}
			policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			old := legacyPipelineSelection(t, "high-risk")
			if test.isVersionTwo || test.isVersionThree {
				previous := old.Policy
				previous.Version = 2
				previous.Primary = pipeline.Reviewer{Harness: "codex", Model: "gpt-5.6-sol", Effort: "high"}
				if test.isVersionThree {
					previous.Version = 3
					previous.Primary = pipeline.Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
				}
				previous.Reviewer, previous.Fixer = previous.Primary, previous.Primary
				old, err = previous.Select(old.Class)
				if err != nil {
					t.Fatal(err)
				}
			}
			if test.isVersionSix {
				previous := old.Policy
				previous.Version, previous.Reviewer = 6, pipeline.Reviewer{}
				old, err = previous.Select(old.Class)
				if err != nil {
					t.Fatal(err)
				}
				if policy.Version != 7 || old.Policy.AutoFix == policy.AutoFix {
					t.Fatalf("the checked-in policy is %+v, want version 7 with other follow-ups than version 6's", policy)
				}
			}
			snapshot := filepath.Join(tmp, "pipeline.json")
			if err := old.Save(snapshot); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			config, _, err := pipeline.Render([]byte("{}"), policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
				t.Fatal(err)
			}
			runner := &pipelineRunner{worktree: wt, activeRuns: test.activeRuns}
			var out bytes.Buffer
			if err := pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &out); err != nil {
				t.Fatal(err)
			}
			migrated, err := pipeline.LoadSelection(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			if migrated.Policy != policy || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles || updated.PipelineHash != migrated.Hash || updated.PipelineClass != old.Class {
				t.Fatalf("snapshot=%+v meta=%+v", migrated, updated)
			}
			status, err := state.TailStatus(h.State, "task", 1)
			if err != nil || len(status) != 1 || !strings.Contains(status[0], "pipeline-policy-migrated:") || !strings.Contains(status[0], old.Hash) || !strings.Contains(status[0], migrated.Hash) {
				t.Fatalf("audit=%v err=%v", status, err)
			}
			if len(runner.native) != 0 {
				t.Fatalf("migration launched provider command: %+v", runner.native)
			}
		})
	}
}

// On 2026-10-23 cfo pipeline run and the cfo pipeline migrate it pointed to
// both refused a direct-PR task, as the supervisor starts every root issue,
// with a migration decision that migrate could not make (issue 1438). A task
// with no gate is refused by its mode and its own route, and changes nothing.
func TestPipelineRefusesATaskWithNoGateByItsModeAndRoute(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		command string
		want    []string
	}{
		{name: "run on a direct-PR task", mode: "direct-PR", command: "run", want: []string{`task task runs in mode "direct-PR"`, "cfo pr check", "gh pr ready"}},
		{name: "migrate a direct-PR task", mode: "direct-PR", command: "migrate", want: []string{`task task runs in mode "direct-PR"`, "cfo pr check", "gh pr ready"}},
		{name: "run on a local-only task", mode: "local-only", command: "run", want: []string{`task task runs in mode "local-only"`, "cfo merge-local task"}},
		{name: "migrate a local-only task", mode: "local-only", command: "migrate", want: []string{`task task runs in mode "local-only"`, "cfo merge-local task"}},
		{name: "run on a gated task with no snapshot", mode: "no-mistakes", command: "run", want: []string{"task lacks a spawn-time policy; existing tasks require an explicit migration decision"}},
		{name: "migrate a gated task with no snapshot", mode: "no-mistakes", command: "migrate", want: []string{"task lacks a spawn-time policy; existing tasks require an explicit migration decision"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			nm := filepath.Join(root, "nm")
			tmp := filepath.Join(h.State, "tasktmp", "task")
			project := filepath.Join(root, "project")
			wt := filepath.Join(project, ".worktrees", "gb-task")
			for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
				t.Fatal(err)
			}
			policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			config, _, err := pipeline.Render([]byte("{}"), policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "task", Mode: test.mode, Worktree: wt, Project: project, TaskTmp: tmp}); err != nil {
				t.Fatal(err)
			}
			record, err := state.ReadMeta(filepath.Join(h.State, "task.meta"))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"migrate", "task"}
			if test.command == "run" {
				intent := filepath.Join(root, "intent.md")
				if err := os.WriteFile(intent, []byte("Fix the root.\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"run", "task", "--intent-file", intent}
			}
			runner := &pipelineRunner{worktree: wt}
			var out bytes.Buffer

			// Act
			err = pipelineCommand(context.Background(), h, nm, runner, args, &out)

			// Assert
			if err == nil {
				t.Fatalf("pipeline %s on a %s task succeeded with %q", test.command, test.mode, out.String())
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to contain %q", err, want)
				}
			}
			if test.mode != "no-mistakes" && strings.Contains(err.Error(), "migration decision") {
				t.Fatalf("error = %q, which asks for a migration decision cfo pipeline migrate cannot make for a %s task", err, test.mode)
			}
			if len(runner.native) != 0 || out.Len() != 0 {
				t.Fatalf("refused command ran native %+v and printed %q", runner.native, out.String())
			}
			after, err := state.ReadMeta(filepath.Join(h.State, "task.meta"))
			if err != nil || !maps.Equal(after, record) {
				t.Fatalf("task record = %v, %v, want it unchanged as %v", after, err, record)
			}
			for _, name := range []string{"pipeline.json", policyMigrationJournalName} {
				if _, err := os.Stat(filepath.Join(tmp, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("refused command left %s: %v", name, err)
				}
			}
			if status, err := state.TailStatus(h.State, "task", 1); err != nil || len(status) != 0 {
				t.Fatalf("refused command wrote status %v: %v", status, err)
			}
			if shared, err := os.ReadFile(filepath.Join(nm, "config.yaml")); err != nil || !bytes.Equal(shared, config) {
				t.Fatalf("refused command changed the shared config: %v", err)
			}
		})
	}
}

// On 2026-10-08 the operator gave the gate a Claude fallback in the shared
// config, and a task frozen at v3 then refused with shared config drift. The
// policy catching up with the machine is all it takes: migrate moves the task
// while the daemon runs a gate, and nothing touches the shared config.
func TestPipelineMigrateCatchesUpWhileTheDaemonRunsAGate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("holds the native daemon lock")
	}
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	// Version 4 is the policy that names the machine's chain as it stood.
	chain := legacyPipelineSelection(t, "ordinary").Policy
	chain.Version, chain.Reviewer = 4, pipeline.Reviewer{}
	chain.Primary = pipeline.Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
	chain.Fallback = pipeline.Reviewer{Harness: "claude"}
	current, err := json.Marshal(chain)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	machine := []byte("# Prefer Codex at the user's request; retain Claude as an available fallback.\nagent:\n  - codex\n  - claude\nauto_fix:\n  rebase: 1\n  lint: 1\n  test: 1\n  review: 0\n  document: 10\n  ci: 1\nagent_args_override:\n  codex:\n    - -c\n    - service_tier=\"default\"\nagent_config:\n  codex:\n    effort: xhigh\n    model: gpt-6.1-sol\n")
	configPath := filepath.Join(nm, "config.yaml")
	if err := os.WriteFile(configPath, machine, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	frozen := legacyPipelineSelection(t, "high-risk").Policy
	frozen.Version = 3
	frozen.Primary = pipeline.Reviewer{Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
	frozen.Reviewer, frozen.Fixer = frozen.Primary, frozen.Primary
	old, err := frozen.Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(tmp, "pipeline.json")
	if err := old.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	runner := &pipelineRunner{worktree: wt}
	// The daemon holds its singleton lock for as long as it runs.
	daemon, err := pipeline.Reader{Root: nm, Commands: runner}.Idle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer daemon()
	runner.activeRuns = 1

	err = pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship it"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "shared config drift") {
		t.Fatalf("run before migrating=%v, want the frozen v3 policy to drift from the machine", err)
	}
	var out bytes.Buffer
	if err := pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &out); err != nil {
		t.Fatalf("migrate under a running daemon: %v", err)
	}
	migrated, err := pipeline.LoadSelection(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Policy != policy || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles {
		t.Fatalf("snapshot=%+v, want the current policy with the frozen class and cap", migrated)
	}
	drift, err := (pipeline.Config{Path: configPath, Policy: migrated.Policy}).Drift()
	if err != nil || len(drift) != 0 {
		t.Fatalf("migrated task still drifts: %v %v", drift, err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(after, machine) {
		t.Fatalf("migration changed the shared config: %s %v", after, err)
	}
	if len(runner.native) != 0 {
		t.Fatalf("migration launched a native command: %+v", runner.native)
	}
}

func TestPipelineMigrateRollsBackWhenAuditCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt, filepath.Join(h.State, "task.status")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	snapshot := filepath.Join(tmp, "pipeline.json")
	if err := old.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	runner := &pipelineRunner{worktree: wt}
	err = pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("migration succeeded without a writable audit log")
	}
	unchanged, loadErr := pipeline.LoadSelection(snapshot)
	if loadErr != nil || unchanged != old {
		t.Fatalf("snapshot rollback: %+v %v", unchanged, loadErr)
	}
	updated, readErr := state.ReadTaskMeta(h.State, "task")
	if readErr != nil || updated.PipelineHash != old.Hash || updated.PipelineClass != old.Class {
		t.Fatalf("metadata rollback: %+v %v", updated, readErr)
	}
	if len(runner.native) != 0 {
		t.Fatalf("migration launched provider command: %+v", runner.native)
	}
}

func TestEnsurePolicyMigrationAuditTrustsSuccessfulAppend(t *testing.T) {
	checks := 0
	err := ensurePolicyMigrationAuditWith(false, func() (bool, error) {
		checks++
		if checks == 1 {
			return false, nil
		}
		return false, errors.New("transient audit read failure")
	}, func() error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("audit reads=%d, want 1", checks)
	}
}

func TestEnsurePolicyMigrationAuditMarksFailedAppendWithUnreadableStateUncertain(t *testing.T) {
	checks := 0
	err := ensurePolicyMigrationAuditWith(false, func() (bool, error) {
		checks++
		if checks == 1 {
			return false, nil
		}
		return false, errors.New("transient audit read failure")
	}, func() error {
		return errors.New("ambiguous append failure")
	})
	if !errors.Is(err, errPolicyMigrationAuditUncertain) {
		t.Fatalf("error=%v, want uncertain audit state", err)
	}
}

func TestPolicyMigrationRetainsForwardStateWhenExistingAuditCannotBeRead(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	tmp := filepath.Join(h.State, "tasktmp", "task")
	if err := os.MkdirAll(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(h.State, "task.status"), 0700); err != nil {
		t.Fatal(err)
	}
	current, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	next, err := pipeline.MigrateSelection(old, current)
	if err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", TaskTmp: tmp, PipelineClass: next.Class, PipelineHash: next.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := next.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	journal := policyMigrationJournal{Version: 1, TaskID: meta.ID, Direction: "forward", Old: old, New: next, Audit: pipelineMigrationAudit(old, next)}
	journalPath := filepath.Join(tmp, policyMigrationJournalName)
	if err := writePolicyMigrationJournal(journalPath, journal); err != nil {
		t.Fatal(err)
	}
	if err := applyPolicyMigration(h, meta, journalPath, journal, true); !errors.Is(err, errPolicyMigrationAuditUncertain) {
		t.Fatalf("error=%v, want uncertain audit state", err)
	}
	selection, err := pipeline.LoadSelection(filepath.Join(tmp, "pipeline.json"))
	if err != nil || selection != next {
		t.Fatalf("snapshot=%+v err=%v", selection, err)
	}
	updated, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || updated.PipelineHash != next.Hash {
		t.Fatalf("metadata=%+v err=%v", updated, err)
	}
	loaded, err := loadPolicyMigrationJournal(journalPath)
	if err != nil || loaded.Direction != "forward" {
		t.Fatalf("journal=%+v err=%v", loaded, err)
	}
}

func TestPipelineMigrateResumesInterruptedTransaction(t *testing.T) {
	for _, stage := range []string{"snapshot", "metadata", "audit"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			nm := filepath.Join(root, "nm")
			tmp := filepath.Join(h.State, "tasktmp", "task")
			project := filepath.Join(root, "project")
			wt := filepath.Join(project, ".worktrees", "gb-task")
			for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
				t.Fatal(err)
			}
			policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
			if err != nil {
				t.Fatal(err)
			}
			old := legacyPipelineSelection(t, "high-risk")
			next, err := pipeline.MigrateSelection(old, policy)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := filepath.Join(tmp, "pipeline.json")
			if err := old.Save(snapshot); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			config, _, err := pipeline.Render([]byte("{}"), policy)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
				t.Fatal(err)
			}
			journal := policyMigrationJournal{Version: 1, TaskID: "task", Direction: "forward", Old: old, New: next, Audit: pipelineMigrationAudit(old, next)}
			journalPath := filepath.Join(tmp, policyMigrationJournalName)
			if err := writePolicyMigrationJournal(journalPath, journal); err != nil {
				t.Fatal(err)
			}
			if err := next.Save(snapshot); err != nil {
				t.Fatal(err)
			}
			if stage == "metadata" || stage == "audit" {
				values, err := state.ReadMeta(filepath.Join(h.State, "task.meta"))
				if err != nil {
					t.Fatal(err)
				}
				values["pipeline_hash"] = next.Hash
				if err := state.WriteMeta(filepath.Join(h.State, "task.meta"), values); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "audit" {
				if err := state.AppendStatus(h.State, "task", journal.Audit); err != nil {
					t.Fatal(err)
				}
			}

			runner := &pipelineRunner{worktree: wt}
			if err := pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			migrated, err := pipeline.LoadSelection(snapshot)
			if err != nil || migrated != next {
				t.Fatalf("snapshot=%+v err=%v", migrated, err)
			}
			updated, err := state.ReadTaskMeta(h.State, "task")
			if err != nil || updated.PipelineHash != next.Hash || updated.PipelineClass != next.Class {
				t.Fatalf("metadata=%+v err=%v", updated, err)
			}
			if _, err := os.Stat(journalPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal remains: %v", err)
			}
			status, err := state.TailStatus(h.State, "task", 10)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, line := range status {
				_, event := state.SplitStatus(line)
				if event == journal.Audit {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("migration audit count=%d, want 1: %v", count, status)
			}
		})
	}
}

func TestPipelineMigrationRecoveryRetainsJournalWhenNewPolicyDrifts(t *testing.T) {
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	for _, path := range []string{nm, tmp} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	next, err := pipeline.MigrateSelection(old, current)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	journal := policyMigrationJournal{Version: 1, TaskID: meta.ID, Direction: "forward", Old: old, New: next, Audit: pipelineMigrationAudit(old, next)}
	journalPath := filepath.Join(tmp, policyMigrationJournalName)
	if err := writePolicyMigrationJournal(journalPath, journal); err != nil {
		t.Fatal(err)
	}
	unsafeConfig, _, err := pipeline.Render([]byte("{}"), current)
	if err != nil {
		t.Fatal(err)
	}
	var configDocument yaml.Node
	if err := yaml.Unmarshal(unsafeConfig, &configDocument); err != nil {
		t.Fatal(err)
	}
	var agentPaths yaml.Node
	if err := agentPaths.Encode(map[string]string{"codex": "C:/tools/openrouter-codex.exe"}); err != nil {
		t.Fatal(err)
	}
	configDocument.Content[0].Content = append(configDocument.Content[0].Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "agent_path_override"},
		&agentPaths,
	)
	unsafeConfig, err = yaml.Marshal(&configDocument)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), unsafeConfig, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	err = resumePipelinePolicyMigration(h, nm, meta)
	if err == nil || !strings.Contains(err.Error(), "apply current shared config before migrating tasks") || !strings.Contains(err.Error(), "agent_path_override.codex") {
		t.Fatalf("recovery error=%v, want Codex executable drift refusal", err)
	}
	selection, err := pipeline.LoadSelection(filepath.Join(tmp, "pipeline.json"))
	if err != nil || selection != old {
		t.Fatalf("refused recovery changed snapshot: %+v %v", selection, err)
	}
	updated, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil || updated.PipelineHash != old.Hash {
		t.Fatalf("refused recovery changed metadata: %+v %v", updated, err)
	}
	if _, err := loadPolicyMigrationJournal(journalPath); err != nil {
		t.Fatalf("refused recovery removed journal: %v", err)
	}
}

func TestPipelineMigrationRacingPRCheckPreservesBothUpdates(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses a blocking Windows gh shim")
	}
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	bin := filepath.Join(root, "bin")
	for _, path := range []string{filepath.Join(root, "config"), nm, tmp, wt, bin} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	if err := old.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "gh-ready")
	release := filepath.Join(root, "gh-release")
	shim := "@echo off\r\n>\"%GH_READY%\" echo ready\r\n:wait\r\nif not exist \"%GH_RELEASE%\" (\r\n  ping 127.0.0.1 -n 2 >nul\r\n  goto wait\r\n)\r\necho abc123\r\n"
	if err := os.WriteFile(filepath.Join(bin, "gh.bat"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_READY", ready)
	t.Setenv("GH_RELEASE", release)
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() {
		done <- runPRCheck([]string{"task", "https://example.test/pull/1"}, &stdout, &stderr)
	}()
	deadline := time.Now().Add(time.Minute)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pr check did not reach the blocked head lookup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.WriteFile(release, nil, 0600)
	if err := pipelineCommand(context.Background(), h, nm, &pipelineRunner{worktree: wt}, []string{"migrate", "task"}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("pr check code=%d stderr=%s", code, &stderr)
	}
	updated, err := state.ReadMeta(filepath.Join(h.State, "task.meta"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := policy.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	if updated["pipeline_hash"] != want.Hash || updated["pipeline_class"] != want.Class || updated["pr"] != "https://example.test/pull/1" || updated["pr_head"] != "abc123" {
		t.Fatalf("metadata lost migration or PR update: %+v", updated)
	}
}

func TestPipelineMigrationRacingSwitchPreservesBothUpdates(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(name, filepath.Join(root, "cache"))
	}
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	nm := filepath.Join(root, "nm")
	tmp := filepath.Join(h.State, "tasktmp", "task")
	project := filepath.Join(root, "project")
	wt := filepath.Join(project, ".worktrees", "gb-task")
	brief := filepath.Join(tmp, "brief.md")
	for _, path := range []string{filepath.Join(root, "config"), h.Data, nm, tmp, wt} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	current, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), current, 0600); err != nil {
		t.Fatal(err)
	}
	policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("continue the task\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	snapshot := filepath.Join(tmp, "pipeline.json")
	if err := old.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{
		ID:            "task",
		Mode:          "no-mistakes",
		Worktree:      wt,
		Project:       project,
		TaskTmp:       tmp,
		Brief:         brief,
		PipelineClass: old.Class,
		PipelineHash:  old.Hash,
		Window:        "native",
		Backend:       "native",
		Harness:       string(harness.Claude),
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	// A switch starts the codex its validation found on PATH, so the one this
	// test's adapter stands for is put there. It never runs.
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "codex.cmd"), []byte("@exit /b 1\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stub+string(os.PathListSeparator)+os.Getenv("PATH"))
	switchRunner := &pipelineSwitchRunner{statusReady: make(chan struct{}), statusRelease: make(chan struct{})}
	// No host command is given, so the switch publishes the new harness and
	// then cannot start it; starting it is not what this test is about.
	switchService := spawn.Service{
		Worktrees: worktree.Service{Commands: switchRunner, Git: pipelineSwitchGit{worktree: wt}, DataDir: h.Data},
		Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
			harness.Claude: pipelineSwitchAdapter{kind: harness.Claude},
			harness.Codex:  pipelineSwitchAdapter{kind: harness.Codex},
		}},
		Commands:        switchRunner,
		StateDir:        h.State,
		UserEnvironment: func() ([]string, error) { return os.Environ(), nil },
	}
	switchDone := make(chan error, 1)
	go func() {
		_, err := switchService.Switch(context.Background(), spawn.SwitchRequest{ID: meta.ID, Harness: harness.Codex})
		switchDone <- err
	}()
	<-switchRunner.statusReady

	runner := &pipelineRunner{worktree: wt}
	err = pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", meta.ID}, &bytes.Buffer{})
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("migration racing switch error=%v, want metadata lock refusal", err)
	}
	unchanged, err := pipeline.LoadSelection(snapshot)
	if err != nil || unchanged != old {
		t.Fatalf("refused migration changed snapshot: %+v %v", unchanged, err)
	}
	if _, err := os.Stat(filepath.Join(tmp, policyMigrationJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused migration left journal: %v", err)
	}

	close(switchRunner.statusRelease)
	if err := <-switchDone; err == nil || !strings.Contains(err.Error(), "the command that runs a native terminal's host is required") {
		t.Fatalf("switch: %v, want it to publish the new harness and stop at its launch", err)
	}
	if err := pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", meta.ID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("retry migration: %v", err)
	}

	want, err := policy.Select(old.Class)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := pipeline.LoadSelection(snapshot)
	if err != nil || migrated != want {
		t.Fatalf("snapshot=%+v err=%v", migrated, err)
	}
	updated, err := state.ReadTaskMeta(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PipelineHash != want.Hash || updated.PipelineClass != want.Class || updated.Harness != string(harness.Codex) {
		t.Fatalf("metadata lost migration or switch update: %+v", updated)
	}
	if _, err := os.Stat(filepath.Join(tmp, policyMigrationJournalName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed migration left journal: %v", err)
	}
	status, err := state.TailStatus(h.State, meta.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	audit := pipelineMigrationAudit(old, want)
	count := 0
	for _, line := range status {
		_, event := state.SplitStatus(line)
		if event == audit {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("migration audit count=%d, want 1: %v", count, status)
	}
}

func TestPipelineRespondInvokesNativeOnlyForBudgetedExplicitDecision(t *testing.T) {
	p, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	selection, err := p.Select("ordinary")
	if err != nil {
		t.Fatal(err)
	}
	for _, round := range []int{1, 3} {
		t.Run(string(rune('0'+round)), func(t *testing.T) {
			root := t.TempDir()
			h := home.Home{Root: root, State: filepath.Join(root, "state")}
			nm := filepath.Join(root, "nm")
			tmp := filepath.Join(h.State, "tasktmp", "task")
			project := filepath.Join(root, "project")
			wt := filepath.Join(project, ".worktrees", "gb-task")
			for _, path := range []string{nm, tmp, wt} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
				t.Fatal(err)
			}
			meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			config, _, err := pipeline.Render([]byte("{}"), p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			runner := &pipelineRunner{worktree: wt, gate: pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: round, Findings: `{"findings":[{"id":"bug","action":"auto-fix"}]}`}}
			// A gate runs for hours, so the pipeline must not take the cleanup
			// lock: holding it that long makes an auth refresh report a live
			// task as being cleaned up and never deliver its credentials.
			if _, err := lock.AcquireExclusiveNamed(h.State, state.CleanupLockName("task")); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "fix", "--findings", "bug", "--instructions", "literal $() and ` text"}, &out)
			if round == 3 {
				if !errors.Is(err, pipeline.ErrUnresolved) || len(runner.native) != 0 {
					t.Fatalf("exhausted invoked native: %v %+v", err, runner.native)
				}
			} else {
				if err != nil || len(runner.native) != 1 {
					t.Fatalf("respond: %v %+v", err, runner.native)
				}
				request := runner.native[0]
				if request.Dir != wt || request.Args[len(request.Args)-1] != "literal $() and ` text" {
					t.Fatalf("unsafe argv transport: %+v", request)
				}
				// execx replaces rather than merges a non-nil Env, so the
				// native engine must still receive the inherited environment
				// it resolves its tools and home directory from.
				var hasHome, hasPath bool
				for _, entry := range request.Env {
					if entry == "NM_HOME="+nm {
						hasHome = true
					}
					if name, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "PATH") {
						hasPath = true
					}
				}
				if !hasHome || !hasPath {
					t.Fatalf("native environment: home=%v path=%v", hasHome, hasPath)
				}
				if out.String() != "native decision output\n" {
					t.Fatal(out.String())
				}
			}
		})
	}
}

const (
	scopeHead    = "0123456789abcdef0123456789abcdef01234567"
	scopeBase    = "89abcdef0123456789abcdef0123456789abcdef"
	scopeText    = "Repair exactly the retained Review requirement."
	scopeFinding = `{"id":"bug","severity":"error","category":"review","action":"ask-user","description":"Repair the retained requirement."}`
)

func scopeGate() pipeline.Gate {
	return pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"bug","action":"ask-user","description":"Repair the retained requirement."}]}`}
}

func scopeRounds() string {
	return `{"schema_version":1,"run_id":"run","step":"review","rounds":[{"id":"round","step_result_id":"step","reviewed_head_sha":"` + scopeHead + `","trusted_config_sha":"trusted-config","round":1,"findings":[` + scopeFinding + `]}]}`
}

// scopeRequest is the exact managed decision the production path builds and
// sends for the answering notify in scopeStage.
func scopeRequest() string {
	return `{"version":1,"decision_id":"notify-task-7","authority":"controller_assertion","run_id":"run","step_result_id":"step","round_id":"round","source_head_sha":"` + scopeHead + `","source_base_sha":"` + scopeBase + `","trusted_config_sha":"trusted-config","finding":` + scopeFinding + `,"accepted_scope":"` + scopeText + `"}`
}

func scopeRunInfo() string {
	return `{"id":"run","head_sha":"` + scopeHead + `","base_sha":"` + scopeBase + `","steps":[{"id":"step","step_name":"review","status":"awaiting_approval","latest_round_id":"round"}]}`
}

// scopeAccepted is what native's durable rounds evidence holds once it has
// persisted the decision this task's answered notify names.
func scopeAccepted() string {
	return `{"schema_version":1,"run_id":"run","step":"review","rounds":[],"scope_decisions":[{"version":1,"decision_id":"notify-task-7","authority":"controller_assertion","run_id":"run","step_result_id":"step","round_id":"round","source_head_sha":"` + scopeHead + `","source_base_sha":"` + scopeBase + `","trusted_config_sha":"trusted-config","finding":` + scopeFinding + `,"accepted_scope":"` + scopeText + `"}]}`
}

// scopeStage is a task whose goblin put decision 7 to the CFO, which answered
// it with the exact requirement correction, and whose Review gate is parked
// with native rounds evidence for it.
func scopeStage(t *testing.T) (home.Home, state.TaskMeta, string, *pipelineRunner) {
	t.Helper()
	h, nativeRoot, runner := registeredCFOStage(t, "cfo", scopeGate())
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	plantJSON(t, filepath.Join(h.State, ".supervisor.json"), map[string]any{"questions": []supervisor.Question{{
		ID: "notify-task-7", Task: "task", Generation: meta.SpawnGen, Seq: 7, Answer: scopeText, Status: "succeeded",
	}}})
	runner.rounds = []byte(scopeRounds())
	return h, meta, nativeRoot, runner
}

func scopeRespond(t *testing.T, h home.Home, nativeRoot string, runner *pipelineRunner) error {
	t.Helper()
	return scopeRespondRef(t, h, nativeRoot, runner, "7")
}

func scopeRespondRef(t *testing.T, h home.Home, nativeRoot string, runner *pipelineRunner, ref string) error {
	t.Helper()
	return pipelineCommand(t.Context(), h, nativeRoot, runner, []string{"respond", "task", "--action", "fix", "--findings", "bug", "--scope-decision", ref}, &bytes.Buffer{})
}

func scopeJournalPath(meta state.TaskMeta) string {
	return filepath.Join(meta.TaskTmp, "review-scope-decisions.jsonl")
}

func scopeStatusHas(h home.Home, prefix string) bool {
	lines, _ := state.TailStatus(h.State, "task", 20)
	return slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, prefix) })
}

func TestPipelineScopeDecisionUsesTheRegisteredCFOAndNativeEvidence(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	runner.roundsAfter = []byte(scopeAccepted())
	requests := serveScopeRun(t, nativeRoot, scopeRunInfo())

	if err := scopeRespond(t, h, nativeRoot, runner); err != nil {
		t.Fatalf("scope decision response: %v", err)
	}
	if len(runner.native) != 1 {
		t.Fatalf("native calls=%+v, want one scoped response", runner.native)
	}
	request := runner.native[0]
	if request.Dir != runner.worktree {
		t.Fatalf("native request=%+v, want the scope response sent from the task worktree", request)
	}
	if got := requests.all(); len(got) != 1 || got[0].Method != "get_run" || got[0].RunID != "run" {
		t.Fatalf("native evidence requests=%+v, want one get_run for the gated run", got)
	}
	var sent, want any
	if err := json.Unmarshal(runner.scopeDecision, &sent); err != nil {
		t.Fatalf("native scope decision file=%q, want the request's decoded decision: %v", runner.scopeDecision, err)
	}
	if err := json.Unmarshal([]byte(scopeRequest()), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent, want) || runner.scopeRun != scopeGate().RunID {
		t.Fatalf("native scope file=%s run=%q, want the exact decision bound to run %q", runner.scopeDecision, runner.scopeRun, scopeGate().RunID)
	}
	if runner.roundsReads != 2 {
		t.Fatalf("native rounds reads=%d, want the pre-response evidence and its durable acknowledgement", runner.roundsReads)
	}
	journal, err := os.ReadFile(scopeJournalPath(meta))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(journal, []byte(`"decision_id":"notify-task-7"`)) || !bytes.Contains(journal, []byte(`"accepted_scope":"Repair exactly the retained Review requirement."`)) || !bytes.Contains(journal, []byte(`"source_base_sha":"`+scopeBase+`"`)) {
		t.Fatalf("scope journal=%s, want the exact decision and native source binding", journal)
	}
	if !bytes.Contains(journal, []byte(`"scope_decision":`+scopeRequest())) {
		t.Fatalf("scope journal=%s, want the request's exact bytes kept", journal)
	}
	if !scopeStatusHas(h, "pipeline-review-scope-confirmed: decision=notify-task-7 run=run round=round") {
		lines, _ := state.TailStatus(h.State, "task", 20)
		t.Fatalf("status=%q, want the durable acknowledgement audited", lines)
	}
}

// A native response that fails, as an engine without the scope method does,
// leaves its exact outcome in the task's journal and starts no ordinary
// response, retry or new run.
func TestPipelineScopeDecisionRecordsANativeRefusalWithoutFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	runner.nativeResult = &execx.Result{ExitCode: 1, Stderr: []byte("daemon did not acknowledge the accepted scope decision")}
	serveScopeRun(t, nativeRoot, scopeRunInfo())

	err := scopeRespond(t, h, nativeRoot, runner)
	if err == nil || !strings.Contains(err.Error(), "recorded in review-scope-decisions.jsonl") {
		t.Fatalf("native refusal err=%v, want it refused as recorded evidence with no fallback", err)
	}
	if len(runner.native) != 1 || runner.roundsReads != 1 {
		t.Fatalf("native calls=%+v rounds reads=%d, want one scoped response and no acknowledgement read", runner.native, runner.roundsReads)
	}
	journal, readErr := os.ReadFile(scopeJournalPath(meta))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Contains(journal, []byte(`"exit_code":1`)) || scopeStatusHas(h, "pipeline-review-scope-confirmed") {
		t.Fatalf("scope journal=%s, want the refusal kept and no confirmed audit", journal)
	}
}

// A scope response a busy gate daemon turns away at its health reply is waited
// on as any response is. Turned away on every try, its refusal says the daemon
// is busy, not stopped, and that the answer never reached it, rather than that
// no retry was attempted after four tries (#2158). Turned away once and then
// refused by the gate, its refusal names that refusal as the recorded outcome
// rather than a busy daemon's.
func TestPipelineScopeDecisionTurnedAwayByABusyDaemonSaysItWasTriedAndNeverArrived(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	for _, scenario := range []struct {
		name      string
		outcomes  []execx.Result
		wantTries int
		wantSaid  []string
	}{
		{
			name:      "turned away on every try",
			outcomes:  []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}},
			wantTries: daemonTurns,
			wantSaid:  []string{"busy, not stopped", "Your answer never reached it"},
		},
		{
			name:      "turned away once and then refused by the gate",
			outcomes:  []execx.Result{{ExitCode: 1, Stdout: []byte(healthMissed)}, {ExitCode: 1, Stderr: []byte("daemon did not acknowledge the accepted scope decision")}},
			wantTries: 2,
			wantSaid:  []string{"after a busy gate daemon turned away every earlier try at its health reply, try 2 reached it and was refused; that refusal is the recorded outcome, and no ordinary response was attempted"},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			h, meta, nativeRoot, runner := scopeStage(t)
			runner.nativeOutcomes = scenario.outcomes
			serveScopeRun(t, nativeRoot, scopeRunInfo())
			if err := os.WriteFile(filepath.Join(nativeRoot, "daemon.pid"), []byte(fmt.Sprintf(`{"pid":%d,"started_at":"2026-11-11T12:57:48Z"}`, os.Getpid())), 0o600); err != nil {
				t.Fatal(err)
			}
			var waits []time.Duration
			previousSleep := daemonTurnSleep
			daemonTurnSleep = func(wait time.Duration) { waits = append(waits, wait) }
			t.Cleanup(func() { daemonTurnSleep = previousSleep })

			// Act
			err := scopeRespond(t, h, nativeRoot, runner)

			// Assert
			if len(runner.native) != scenario.wantTries || len(waits) != scenario.wantTries-1 {
				t.Fatalf("native calls=%d waits=%v, want %d tries and %d waits", len(runner.native), waits, scenario.wantTries, scenario.wantTries-1)
			}
			if err == nil || !strings.Contains(err.Error(), "recorded in review-scope-decisions.jsonl") {
				t.Fatalf("err=%v, want the journal named", err)
			}
			for _, said := range scenario.wantSaid {
				if !strings.Contains(err.Error(), said) {
					t.Fatalf("err=%v, want it to say %q", err, said)
				}
			}
			if strings.Contains(err.Error(), "no ordinary response or retry was attempted") {
				t.Fatalf("err=%v claims no retry after %d tries", err, len(runner.native))
			}
			if journal, readErr := os.ReadFile(scopeJournalPath(meta)); readErr != nil || !bytes.Contains(journal, []byte(`"exit_code":1`)) {
				t.Fatalf("scope journal=%s, %v; want the last try's outcome kept", journal, readErr)
			}
		})
	}
}

// A clean native exit with no durable record of this decision is an uncertain
// acknowledgement: the controller refuses instead of reporting delivery.
func TestPipelineScopeDecisionRefusesAnUnconfirmedAcknowledgement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	runner.roundsAfter = []byte(`{"schema_version":1,"run_id":"run","step":"review","rounds":[]}`)
	serveScopeRun(t, nativeRoot, scopeRunInfo())

	err := scopeRespond(t, h, nativeRoot, runner)
	if err == nil || !strings.Contains(err.Error(), "did not confirm the accepted Review scope") {
		t.Fatalf("unconfirmed acknowledgement err=%v, want it refused", err)
	}
	journal, readErr := os.ReadFile(scopeJournalPath(meta))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Contains(journal, []byte(`"exit_code":0`)) || scopeStatusHas(h, "pipeline-review-scope-confirmed") {
		t.Fatalf("scope journal=%s, want the native success kept and no confirmed audit", journal)
	}
}

// An identical replay is the one native makes idempotent, so the journal
// leaves it to native and records the second outcome beside the first.
func TestPipelineScopeDecisionAllowsAnIdenticalReplay(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	record := `{"at":"2026-11-07T00:00:00Z","request_sha256":"earlier","scope_decision":` + scopeRequest() + `,"stdout":"","stderr":"","exit_code":0}` + "\n"
	if err := os.WriteFile(scopeJournalPath(meta), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	runner.roundsAfter = []byte(scopeAccepted())
	requests := serveScopeRun(t, nativeRoot, scopeRunInfo())

	if err := scopeRespondRef(t, h, nativeRoot, runner, "notify-task-7"); err != nil {
		t.Fatalf("identical replay: %v", err)
	}
	if len(runner.native) != 1 {
		t.Fatalf("native calls=%+v, want the replay sent once more", runner.native)
	}
	if len(requests.all()) == 0 {
		t.Fatal("identical replay did not read native RunInfo")
	}
	journal, readErr := os.ReadFile(scopeJournalPath(meta))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if lines := strings.Count(string(journal), "\n"); lines != 2 {
		t.Fatalf("scope journal=%s, want the earlier decision kept beside the replay's outcome", journal)
	}
}

// The same round's earlier decision stands: a replay carrying different bytes
// is refused before native is asked anything.
func TestPipelineScopeDecisionRefusesAChangedReplay(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	record := `{"at":"2026-11-07T00:00:00Z","request_sha256":"earlier","scope_decision":{"version":1,"decision_id":"notify-task-7","authority":"controller_assertion","run_id":"run","step_result_id":"step","round_id":"round","source_head_sha":"` + scopeHead + `","source_base_sha":"` + scopeBase + `","trusted_config_sha":"trusted-config","finding":{"id":"bug"},"accepted_scope":"An earlier requirement correction."},"stdout":"","stderr":"","exit_code":0}` + "\n"
	if err := os.WriteFile(scopeJournalPath(meta), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	serveScopeRun(t, nativeRoot, scopeRunInfo())

	err := scopeRespond(t, h, nativeRoot, runner)
	if err == nil || !strings.Contains(err.Error(), "differs from its durable decision or round binding") {
		t.Fatalf("changed replay err=%v, want it refused", err)
	}
	if len(runner.native) != 0 {
		t.Fatalf("native calls=%+v, want nothing sent", runner.native)
	}
	journal, readErr := os.ReadFile(scopeJournalPath(meta))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(journal) != record {
		t.Fatalf("scope journal=%s, want the earlier decision left as it stood", journal)
	}
}

// A --scope-decision given with no sequence is a decision refused before
// anything is read or sent, never read as no decision that would send the
// ordinary fix and drop the accepted scope the primary CEO answered with.
func TestPipelineScopeDecisionRefusesABlankDecision(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	serveScopeRun(t, nativeRoot, scopeRunInfo())
	for _, given := range []string{"", "  "} {
		err := pipelineCommand(t.Context(), h, nativeRoot, runner, []string{"respond", "task", "--action", "fix", "--findings", "bug", "--scope-decision", given}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "--scope-decision is empty") {
			t.Fatalf("--scope-decision %q err=%v, want it refused as a blank decision", given, err)
		}
		if len(runner.native) != 0 || runner.roundsReads != 0 {
			t.Fatalf("--scope-decision %q native=%+v rounds reads=%d, want nothing read or sent", given, runner.native, runner.roundsReads)
		}
		if _, statErr := os.Stat(scopeJournalPath(meta)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("--scope-decision %q wrote scope evidence (%v), want none", given, statErr)
		}
	}
}

// A goblin that carries the primary's process tree is refused by its own
// terminal labels, and a gate agent the same way.
func TestPipelineScopeDecisionRefusesAWorkerOrGateAgent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	t.Run("goblin", func(t *testing.T) {
		h, _, nativeRoot, runner := scopeStage(t)
		inGoblinTerminal(t, h.State, "task")
		err := scopeRespond(t, h, nativeRoot, runner)
		if err == nil || !strings.Contains(err.Error(), "registered primary CFO") || len(runner.native) != 0 || runner.roundsReads != 0 {
			t.Fatalf("goblin scope decision err=%v native=%+v rounds reads=%d, want primary-only refusal before native access", err, runner.native, runner.roundsReads)
		}
	})
	t.Run("gate agent", func(t *testing.T) {
		h, _, nativeRoot, runner := scopeStage(t)
		outsideGoblinTerminal(t)
		t.Setenv(gateAgentVariable, "1")
		err := scopeRespond(t, h, nativeRoot, runner)
		if err == nil || !strings.Contains(err.Error(), "registered primary CFO") || !strings.Contains(err.Error(), "gate agent") || len(runner.native) != 0 || runner.roundsReads != 0 {
			t.Fatalf("gate agent scope decision err=%v native=%+v rounds reads=%d, want primary-only refusal before native access", err, runner.native, runner.roundsReads)
		}
	})
}

// Every part of the binding comes from native's typed evidence, so evidence
// that does not name this exact round, source and finding refuses before any
// scope file is written.
func TestPipelineScopeDecisionRefusesMismatchedNativeEvidence(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	const otherHead = "fedcba9876543210fedcba9876543210fedcba98"
	cases := []struct {
		Name    string
		Rounds  string
		RunInfo string
		Want    string
	}{
		{"reviewed head differs", strings.Replace(scopeRounds(), `"reviewed_head_sha":"`+scopeHead+`"`, `"reviewed_head_sha":"`+otherHead+`"`, 1), "", "exact source, configuration or identity"},
		{"round differs", strings.Replace(scopeRounds(), `"round":1`, `"round":2`, 1), "", "exact source, configuration or identity"},
		{"finding absent", strings.Replace(scopeRounds(), `"id":"bug"`, `"id":"other"`, 1), "", "absent from the native Review round"},
		{"trusted configuration absent", strings.Replace(scopeRounds(), `"trusted_config_sha":"trusted-config"`, `"trusted_config_sha":""`, 1), "", "exact source, configuration or identity"},
		{"foreign step result", strings.Replace(scopeRounds(), `"step_result_id":"step"`, `"step_result_id":"other"`, 1), "", "exact source, configuration or identity"},
		{"base source absent", "", strings.Replace(scopeRunInfo(), `"base_sha":"`+scopeBase+`"`, `"base_sha":""`, 1), "lacks the exact source binding"},
		{"step status differs", "", strings.Replace(scopeRunInfo(), `"status":"awaiting_approval"`, `"status":"running"`, 1), "does not confirm the parked Review step"},
	}
	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			h, _, nativeRoot, runner := scopeStage(t)
			if testCase.Rounds != "" {
				runner.rounds = []byte(testCase.Rounds)
			}
			runInfo := scopeRunInfo()
			if testCase.RunInfo != "" {
				runInfo = testCase.RunInfo
			}
			serveScopeRun(t, nativeRoot, runInfo)
			err := scopeRespond(t, h, nativeRoot, runner)
			if err == nil || !strings.Contains(err.Error(), testCase.Want) || len(runner.native) != 0 {
				t.Fatalf("mismatched evidence err=%v native=%+v, want it refused before writing a scope file", err, runner.native)
			}
		})
	}
}

// A durable decision that keeps the identity but not the accepted correction
// is a wrong acknowledgement, refused like a missing one.
func TestPipelineScopeDecisionRefusesADifferingAcknowledgement(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	runner.roundsAfter = []byte(strings.Replace(scopeAccepted(), `"accepted_scope":"`+scopeText+`"`, `"accepted_scope":"A different requirement correction."`, 1))
	serveScopeRun(t, nativeRoot, scopeRunInfo())

	err := scopeRespond(t, h, nativeRoot, runner)
	if err == nil || !strings.Contains(err.Error(), "differs from the accepted request") {
		t.Fatalf("differing acknowledgement err=%v, want it refused", err)
	}
	if len(runner.native) != 1 || runner.roundsReads != 2 {
		t.Fatalf("native calls=%+v rounds reads=%d, want one scoped response and its acknowledgement read", runner.native, runner.roundsReads)
	}
	journal, readErr := os.ReadFile(scopeJournalPath(meta))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Contains(journal, []byte(`"exit_code":0`)) || scopeStatusHas(h, "pipeline-review-scope-confirmed") {
		t.Fatalf("scope journal=%s, want the native success kept and no confirmed audit", journal)
	}
}

// A --scope-decision naming another task's notify is not this task's decision.
func TestPipelineScopeDecisionRefusesAnotherTasksNotify(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	h, meta, nativeRoot, runner := scopeStage(t)
	serveScopeRun(t, nativeRoot, scopeRunInfo())
	err := scopeRespondRef(t, h, nativeRoot, runner, "notify-other-7")
	if err == nil || !strings.Contains(err.Error(), "is not the question of task task's decision 7") {
		t.Fatalf("another task's notify err=%v, want it refused", err)
	}
	if len(runner.native) != 0 || runner.roundsReads != 0 {
		t.Fatalf("native=%+v rounds reads=%d, want nothing read or sent", runner.native, runner.roundsReads)
	}
	if _, statErr := os.Stat(scopeJournalPath(meta)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("another task's notify wrote scope evidence (%v), want none", statErr)
	}
}

type scopeRunRequest struct {
	Method string
	RunID  string
}

// scopeRunLog is the RunInfo requests the stand-in native engine answered. It
// records a request before replying to it, so a driver that finished its read
// cannot have done so before the request it made is on the log: no clock is
// needed to tell whether the managed driver asked native anything.
type scopeRunLog struct {
	mu       sync.Mutex
	requests []scopeRunRequest
}

func (log *scopeRunLog) all() []scopeRunRequest {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.requests)
}

func serveScopeRun(t *testing.T, nativeRoot, run string) *scopeRunLog {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const token = "scope-run-token"
	if err := os.WriteFile(filepath.Join(nativeRoot, "socket"), []byte(listener.Addr().String()+"\n"+token+"\n"+strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := &scopeRunLog{}
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		gotToken, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(gotToken) != token {
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
			Params struct {
				RunID string `json:"run_id"`
			} `json:"params"`
		}
		if err := json.NewDecoder(reader).Decode(&request); err != nil {
			return
		}
		requests.mu.Lock()
		requests.requests = append(requests.requests, scopeRunRequest{Method: request.Method, RunID: request.Params.RunID})
		requests.mu.Unlock()
		_ = json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]json.RawMessage{"run": json.RawMessage(run)}, "id": request.ID})
	}()
	return requests
}

func TestPipelineRunLaunchesNativeAfterCheckStart(t *testing.T) {
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
	wt := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{nm, tmp, project, wt} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
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
	runner := &pipelineStartRunner{worktree: wt}
	if err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("pipelineCommand: %v", err)
	}
	if len(runner.native) != 1 {
		t.Fatalf("native launches=%+v, want one", runner.native)
	}
	request := runner.native[0]
	if request.Name != "no-mistakes" || !strings.HasPrefix(strings.Join(request.Args, " "), "axi run --intent ship safely --launch-nonce cfo-") {
		t.Fatalf("native launch argv: %+v", request)
	}
	if request.Dir != wt {
		t.Fatalf("native launch directory=%q, want the task worktree %q", request.Dir, wt)
	}
	// execx replaces rather than merges a non-nil Env, so the native engine
	// must still receive the inherited environment beside the NM_HOME
	// override it resolves its tools and home directory from.
	var hasHome, hasPath bool
	for _, entry := range request.Env {
		if entry == "NM_HOME="+nm {
			hasHome = true
		}
		if name, _, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "PATH") {
			hasPath = true
		}
	}
	if !hasHome || !hasPath {
		t.Fatalf("native environment: home=%v path=%v", hasHome, hasPath)
	}
}

func TestPipelineRunRefusesAMissingRequiredEnvironmentBeforeStartingTheGate(t *testing.T) {
	// Arrange
	policy, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	selection, err := policy.Select("high-risk")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	nativeRoot, temporary, checkout := filepath.Join(root, "native"), filepath.Join(h.State, "tasktmp", "task"), filepath.Join(root, "project")
	worktree := filepath.Join(checkout, ".worktrees", "gb-task")
	for _, directory := range []string{nativeRoot, temporary, worktree, filepath.Join(h.Data, "projects", "project")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(temporary, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Project: checkout, Worktree: worktree, TaskTmp: temporary, PipelineClass: selection.Class, PipelineHash: selection.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativeRoot, "config.yaml"), config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "projects", "project", "project.json"), []byte(`{"project":"project","test_environment":{"python_version":"3.12.10"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &pipelineStartRunner{worktree: worktree}

	// Act
	err = pipelineCommand(context.Background(), h, nativeRoot, runner, []string{"run", "task", "--intent", "reuse the verified environment"}, &bytes.Buffer{})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "required test environment") || len(runner.native) != 0 {
		t.Fatalf("error=%v native launches=%v; want refusal before native launch", err, runner.native)
	}
}

// taskWorktreesRunner answers git in each directory of branches as the top
// level of a checkout with that branch checked out, the native database's run
// lookup with runBranch and its custody record with recovery; every other
// command goes to the runner it wraps.
type taskWorktreesRunner struct {
	execx.Runner
	branches  map[string]string
	runBranch string
	recovery  string
}

func (r taskWorktreesRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	command := strings.Join(q.Args, " ")
	switch {
	case q.Name == "git" && command == "rev-parse --show-toplevel":
		return execx.Result{Stdout: []byte(q.Dir + "\n")}, nil
	case q.Name == "git" && command == "symbolic-ref --quiet --short HEAD":
		branch, ok := r.branches[q.Dir]
		if !ok {
			return execx.Result{ExitCode: 1}, nil
		}
		return execx.Result{Stdout: []byte(branch + "\n")}, nil
	case q.Name == "sqlite3" && strings.Contains(command, "SELECT runs.branch FROM runs"):
		if r.runBranch == "" {
			return execx.Result{Stdout: []byte("[]")}, nil
		}
		return execx.Result{Stdout: []byte(`[{"branch":"` + r.runBranch + `"}]`)}, nil
	case q.Name == "sqlite3" && strings.Contains(command, "AS recorded_head"):
		return execx.Result{Stdout: []byte(r.recovery)}, nil
	}
	return r.Runner.Run(ctx, q)
}

// extraWorktreeTask is a gated task shaped as cg-board-polish-4 was on
// 2026-10-03: its own worktree on feat/panel-row and an extra worktree, which
// cfo worktree add recorded, on fix/connections, where PR 308's run waited on
// a decision the CFO could not give. A stray worktree of the project that the
// task never recorded has fix/stray checked out. It returns the home, the
// native root and the branches checked out by directory.
func extraWorktreeTask(t *testing.T) (home.Home, string, map[string]string) {
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
	own := filepath.Join(root, "worktrees", "project", "task")
	extra := filepath.Join(root, "worktrees", "project", "task-connections")
	stray := filepath.Join(root, "worktrees", "project", "stray")
	for _, path := range []string{nm, tmp, project, own, extra, stray} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: own, Extras: []string{extra}, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash, Harness: "codex", Model: "gpt-6.1-sol", Effort: "xhigh"}
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
	return h, nm, map[string]string{own: "feat/panel-row", extra: "fix/connections", stray: "fix/stray"}
}

// A goblin that gates several pull requests from extra worktrees still gets
// the CFO's decision on each: respond, named the branch or the run, answers
// the gate from the worktree that has that branch checked out, since the
// native command answers the branch checked out where it runs.
func TestPipelineRespondAnswersTheGateOfABranchInAnExtraWorktree(t *testing.T) {
	for _, selector := range [][]string{{"--branch", "fix/connections"}, {"--run", "01M3ZNA9"}} {
		t.Run(selector[0], func(t *testing.T) {
			// Arrange
			h, nm, branches := extraWorktreeTask(t)
			gate := &pipelineRunner{gate: pipeline.Gate{RunID: "01M3ZNA9", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"style","action":"no-op"}]}`}}
			runner := taskWorktreesRunner{Runner: gate, branches: branches, runBranch: "fix/connections"}
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}

			// Act
			err = pipelineCommand(context.Background(), h, nm, runner, append([]string{"respond", "task", "--action", "approve"}, selector...), &bytes.Buffer{})

			// Assert
			if err != nil {
				t.Fatalf("respond %s: %v", strings.Join(selector, " "), err)
			}
			if len(gate.native) != 1 || gate.native[0].Dir != meta.Extras[0] || strings.Join(gate.native[0].Args, " ") != "axi respond --step review --action approve" {
				t.Fatalf("native calls = %+v, want one approve in the extra worktree %s", gate.native, meta.Extras[0])
			}
		})
	}
}

// What respond is named must be the task's: a branch none of its recorded
// worktrees has checked out, a stray worktree's included, and a run that is
// not the latest of such a branch are refused before anything reaches the
// gate.
func TestPipelineRespondRefusesABranchOrRunThatIsNotTheTasks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selector  []string
		runBranch string
		want      string
	}{
		{"a branch checked out nowhere", []string{"--branch", "feat/elsewhere"}, "", "checked out in none of task task's worktrees"},
		{"a stray worktree's branch", []string{"--branch", "fix/stray"}, "", "checked out in none of task task's worktrees"},
		{"a run that is no branch's latest", []string{"--run", "01OLDRUN"}, "", "is not the latest run of a branch"},
		{"a run of a stray worktree's branch", []string{"--run", "01STRAY"}, "fix/stray", "checked out in none of task task's worktrees"},
		{"a run its branch moved past", []string{"--run", "01OLDRUN"}, "fix/connections", "no longer the latest run of fix/connections"},
		{"a branch and a run", []string{"--branch", "fix/connections", "--run", "01M3ZNA9"}, "fix/connections", "name a branch or a run, not both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h, nm, branches := extraWorktreeTask(t)
			gate := &pipelineRunner{gate: pipeline.Gate{RunID: "01M3ZNA9", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[]}`}}
			runner := taskWorktreesRunner{Runner: gate, branches: branches, runBranch: tc.runBranch}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"respond", "task", "--action", "approve"}, tc.selector...), &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("respond %s: %v, want a refusal saying %q", strings.Join(tc.selector, " "), err, tc.want)
			}
			if len(gate.native) != 0 {
				t.Fatalf("a refused respond reached the gate: %+v", gate.native)
			}
		})
	}
}

// run and recover act on a named branch the way respond does: in the task's
// worktree that has it checked out.
func TestPipelineRunAndRecoverActInTheWorktreeOfTheNamedBranch(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		// Arrange
		h, nm, branches := extraWorktreeTask(t)
		start := &pipelineStartRunner{}
		runner := taskWorktreesRunner{Runner: start, branches: branches}

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--branch", "fix/connections", "--intent", "ship safely"}, &bytes.Buffer{})

		// Assert
		if err != nil {
			t.Fatalf("run --branch: %v", err)
		}
		if len(start.native) != 1 || !strings.HasSuffix(start.native[0].Dir, "task-connections") || !strings.HasPrefix(strings.Join(start.native[0].Args, " "), "axi run --intent ship safely --launch-nonce cfo-") {
			t.Fatalf("native launches = %+v, want one run in the extra worktree", start.native)
		}
	})
	t.Run("recover", func(t *testing.T) {
		// Arrange
		h, nm, branches := extraWorktreeTask(t)
		gate := &pipelineRunner{}
		runner := taskWorktreesRunner{Runner: gate, branches: branches, runBranch: "fix/connections",
			recovery: `[{"run_id":"01M3ZNA9","repo_id":"repo","branch":"fix/connections","status":"failed","recorded_head":"aaa","submitted_head":"bbb"}]`}

		// Act: the fake's worktree reads no clean status, so custody stays where it is.
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"recover", "task", "--run", "01M3ZNA9"}, &bytes.Buffer{})

		// Assert
		if err == nil || !strings.Contains(err.Error(), "custody recovery requires a clean task worktree") {
			t.Fatalf("recover --run: %v, want it to reach the extra worktree's custody check", err)
		}
		if len(gate.native) != 1 || !strings.HasSuffix(gate.native[0].Dir, "task-connections") || strings.Join(gate.native[0].Args, " ") != "axi sync --check" {
			t.Fatalf("native calls = %+v, want the custody check in the extra worktree", gate.native)
		}
	})
}

func TestPipelineConfigDriftDoesNotWriteOrExposeValues(t *testing.T) {
	root := t.TempDir()
	nm := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "pipeline.json"), policy, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nm, "config.yaml")
	before := "agent: [pi]\nprivate_token: secret-sentinel\n"
	if err := os.WriteFile(path, []byte(before), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = pipelineCommand(context.Background(), home.Home{Root: root}, nm, nil, []string{"config-drift"}, &out)
	if err != nil || !strings.Contains(out.String(), "auto_fix.review") || strings.Contains(out.String(), "secret-sentinel") {
		t.Fatalf("drift: %s %v", &out, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != before {
		t.Fatalf("drift wrote config: %s %v", after, err)
	}
}

// registeredCFOStage stages a gated task whose primary registration names the
// caller: this test process as the CFO, or for a goblin a separate live
// process this one does not run under. It returns the home, the native root
// and a runner whose gate is the one given.
func registeredCFOStage(t *testing.T, caller string, gate pipeline.Gate) (home.Home, string, *pipelineRunner) {
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
	wt := filepath.Join(project, ".worktrees", "gb-task")
	for _, path := range []string{nm, tmp, wt} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := selection.Save(filepath.Join(tmp, "pipeline.json")); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	config, _, err := pipeline.Render([]byte("{}"), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "config.yaml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	registeredPrimary(t, h, caller)
	return h, nm, &pipelineRunner{worktree: wt, gate: gate}
}

// registeredPrimary registers the home's primary CFO as this test process, or
// for the goblin a separate live process this one does not run under.
func registeredPrimary(t *testing.T, h home.Home, caller string) {
	t.Helper()
	var registered *lock.Info
	var err error
	if caller == "cfo" {
		registered, err = lock.Acquire(h.State)
		t.Cleanup(func() { _ = lock.Release(h.State) })
	} else {
		other := exec.Command("ping", "-n", "30", "127.0.0.1")
		if err := other.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
		registered, err = lock.AcquireOwner(h.State, other.Process.Pid, "other")
	}
	if err != nil {
		t.Fatal(err)
	}
	// The registration names native terminal cfo, whose host record names the
	// registered process as the terminal's program.
	data, err := json.Marshal(struct {
		Host    string    `json:"host"`
		Agent   string    `json:"agent"`
		Process lock.Info `json:"process"`
	}{"cfo", "codex", *registered})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(host.Record{ID: "cfo", Pipe: `\\.\pipe\code-goblins-host-test-` + strconv.Itoa(os.Getpid()), Token: "token", Version: host.Version, HostPID: os.Getpid(), ChildPID: registered.PID, Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "hosts", "cfo.json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Taking a gate's open findings as they stand is the CFO's decision, and a
// goblin's only on its own run's spent review. A process that is neither the
// registered CFO nor the task's own goblin, as this one is outside a goblin's
// terminal, is refused before anything is recorded or sent, even while the
// registered CFO is live; the CFO's acceptance reaches the task's status log
// before approve reaches the gate, and wakes nobody.
func TestPipelineAcceptIsHonouredOnlyFromTheRegisteredCFO(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	gate := pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"bug","action":"auto-fix"},{"id":"note","action":"no-op"}]}`}
	for _, caller := range []string{"goblin", "cfo"} {
		t.Run(caller, func(t *testing.T) {
			h, nm, runner := registeredCFOStage(t, caller, gate)
			outsideGoblinTerminal(t)
			var out bytes.Buffer
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "bug,ask"}, &out)
			lines, _ := state.TailStatus(h.State, "task", 10)
			pending, _ := wake.Pending(h.State)
			if caller == "goblin" {
				if err == nil || !strings.Contains(err.Error(), "does not run under the registered CFO") || len(runner.native) != 0 || len(lines) != 0 || len(pending) != 0 {
					t.Fatalf("a process outside any goblin's terminal accepting: %v, %d native calls, status %q, wakes %+v; want it refused with nothing recorded or sent", err, len(runner.native), lines, pending)
				}
				return
			}
			if err != nil || len(runner.native) != 1 || strings.Join(runner.native[0].Args, " ") != "axi respond --step review --action approve" {
				t.Fatalf("the CFO accepting: %v %+v, want one approve", err, runner.native)
			}
			if len(lines) != 1 || !strings.HasSuffix(lines[0], "pipeline-findings-accepted: step=review run=run round=3 findings=bug,ask by=cfo") {
				t.Fatalf("status log %q, want the acceptance audited", lines)
			}
			if len(pending) != 0 {
				t.Fatalf("wakes %+v; want the CFO's own accept to wake nobody", pending)
			}
		})
	}
}

// outsideGoblinTerminal runs the rest of the test as a process in no
// goblin's terminal, wherever the test itself runs.
func outsideGoblinTerminal(t *testing.T) {
	t.Helper()
	for _, variable := range []string{harness.RoleVariable, host.IDVariable, host.ProofVariable, gateAgentVariable} {
		t.Setenv(variable, "")
	}
}

// inGoblinTerminal runs the rest of the test as a process in goblin task's
// own terminal: its role, its terminal and a proof that terminal was given.
func inGoblinTerminal(t *testing.T, stateDir, task string) {
	t.Helper()
	proof := "proof-of-" + task
	line := host.ProofSum(proof) + " " + time.Now().UTC().Format(time.RFC3339Nano) + "\n"
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", task+".proofs"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideGoblinTerminal(t)
	t.Setenv(harness.RoleVariable, harness.RoleGoblin)
	t.Setenv(host.IDVariable, task)
	t.Setenv(host.ProofVariable, proof)
}

// Once its run has spent its review repair cycles, the goblin that owns the
// task accepts that run's open review findings itself, from its own
// terminal, by the exact-ids rule the CFO's accept keeps: the accept is
// audited with the task as who accepted, and reaches the CFO as a notify
// nothing waits on, before approve reaches the gate, and an accept the CFO
// cannot be told of is never sent (#2049).
func TestPipelineGoblinAcceptsItsOwnSpentReviewFindings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	spent := pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"bug","action":"auto-fix"},{"id":"note","action":"no-op"}]}`}
	t.Run("every open finding named", func(t *testing.T) {
		// Arrange
		h, nm, runner := registeredCFOStage(t, "goblin", spent)
		inGoblinTerminal(t, h.State, "task")

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "bug,ask"}, &bytes.Buffer{})

		// Assert
		if err != nil || len(runner.native) != 1 || strings.Join(runner.native[0].Args, " ") != "axi respond --step review --action approve" {
			t.Fatalf("the goblin accepting its spent review: %v %+v, want one approve", err, runner.native)
		}
		const audit = "pipeline-findings-accepted: step=review run=run round=3 findings=bug,ask by=task"
		if lines, _ := state.TailStatus(h.State, "task", 10); len(lines) != 1 || !strings.HasSuffix(lines[0], audit) {
			t.Fatalf("status log %q, want the accept audited with the task as who accepted", lines)
		}
		pending, err := wake.Pending(h.State)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 || pending[0].Kind != "notify" || pending[0].Key != "task" || !strings.HasPrefix(pending[0].Detail, audit) {
			t.Fatalf("wakes %+v, want one notify to the CFO naming the run, round and findings", pending)
		}
		if verb, isBlocking := wake.BlockingNotify(pending[0]); isBlocking {
			t.Fatalf("the notify is %s, a decision the CFO owes; want one nothing waits on", verb)
		}
	})
	t.Run("not every open finding named", func(t *testing.T) {
		// Arrange
		h, nm, runner := registeredCFOStage(t, "goblin", spent)
		inGoblinTerminal(t, h.State, "task")

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "bug"}, &bytes.Buffer{})

		// Assert
		lines, _ := state.TailStatus(h.State, "task", 10)
		pending, _ := wake.Pending(h.State)
		if err == nil || !strings.Contains(err.Error(), "--accept must name exactly the open ask-user and auto-fix findings: ask,bug") || len(runner.native) != 0 || len(lines) != 0 || len(pending) != 0 {
			t.Fatalf("the goblin naming some of the open findings: %v, %d native calls, status %q, wakes %+v; want the CFO's exact-ids refusal with nothing recorded or sent", err, len(runner.native), lines, pending)
		}
	})
	t.Run("the CFO cannot be told", func(t *testing.T) {
		// Arrange
		h, nm, runner := registeredCFOStage(t, "goblin", spent)
		inGoblinTerminal(t, h.State, "task")
		if err := os.WriteFile(filepath.Join(h.State, ".wake-queue"), []byte("not a wake\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "bug,ask"}, &bytes.Buffer{})

		// Assert
		if err == nil || !strings.Contains(err.Error(), "the CFO cannot be told of this accept, so approve was not sent") || len(runner.native) != 0 {
			t.Fatalf("the goblin accepting with no notify queueable: %v, %d native calls; want it refused with no approve sent", err, len(runner.native))
		}
	})
}

// A spent review still refuses a fix and a bare approve with exit 3, and that
// refusal names the accept its own goblin may make, so a goblin that reads it
// does not go back to wait on the CFO (#2049). A review with cycles left has no
// such accept to name, and an accept that reached this refusal was already
// made, so neither of those refusals names one.
func TestPipelineSpentReviewRefusalNamesItsGoblinsAccept(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	spent := pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"bug","action":"auto-fix"}]}`}
	early := spent
	early.Round = 2
	blank := spent
	blank.Findings = `{"findings":[{"id":"bug","action":"auto-fix"},{"id":"plain"}]}`
	cases := []struct {
		name         string
		gate         pipeline.Gate
		args         []string
		isHintWanted bool
	}{
		{"a fix of a spent review", spent, []string{"--action", "fix", "--findings", "bug"}, true},
		{"a bare approve of a spent review", spent, []string{"--action", "approve"}, true},
		{"a bare approve with cycles left", early, []string{"--action", "approve"}, false},
		{"an accept a finding with no action refuses", blank, []string{"--action", "approve", "--accept", "bug"}, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := registeredCFOStage(t, "goblin", testCase.gate)
			inGoblinTerminal(t, h.State, "task")

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"respond", "task"}, testCase.args...), &bytes.Buffer{})

			// Assert
			if !errors.Is(err, pipeline.ErrUnresolved) || len(runner.native) != 0 {
				t.Fatalf("%v, %d native calls; want the exit 3 refusal with nothing sent", err, len(runner.native))
			}
			if isHintGiven := strings.Contains(err.Error(), "--accept <ids>"); isHintGiven != testCase.isHintWanted {
				t.Fatalf("%v: naming the goblin's accept is %t, want %t", err, isHintGiven, testCase.isHintWanted)
			}
		})
	}
}

// A goblin's accept is refused, with why, before anything is recorded or
// sent: before its run's review repair cycles are spent, on any step but
// review, with the reason a Test exception keeps, on another task's run, from
// a process that carries no proof its terminal was given, and from an agent
// a gate started, which carries whichever goblin's environment started the
// shared gate daemon (#2049).
func TestPipelineGoblinAcceptIsRefusedOutsideItsOwnSpentReview(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	spent := pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"ask","action":"ask-user"},{"id":"bug","action":"auto-fix"}]}`}
	early := spent
	early.Round = 2
	testStep := pipeline.Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 3, Findings: `{"findings":[{"id":"test-2","action":"ask-user"}]}`}
	cases := []struct {
		name     string
		gate     pipeline.Gate
		accept   string
		flags    []string
		terminal string
		change   func(t *testing.T)
		want     string
	}{
		{name: "before the cap is spent", gate: early, accept: "ask,bug", terminal: "task", want: "only once its 2 review repair cycles are spent, and run run's review is in round 2 with 1 left"},
		{name: "on the test step", gate: testStep, accept: "test-2", terminal: "task", want: "only on its run's review step, and this gate is its test step"},
		{name: "with a reason", gate: spent, accept: "ask,bug", flags: []string{"--reason", "only style is left"}, terminal: "task", want: "--accept and --reason is honoured only from the registered primary CFO, since the reason an approved Test exception keeps is never a goblin's"},
		{name: "on another task's run", gate: spent, accept: "ask,bug", terminal: "other", want: "this process runs in task other's terminal, and a goblin accepts only its own task's run"},
		{name: "without its terminal's proof", gate: spent, accept: "ask,bug", terminal: "task", change: func(t *testing.T) { t.Setenv(host.ProofVariable, "guessed") }, want: "this process carries no proof task task's terminal was given"},
		{name: "from an agent a gate started", gate: spent, accept: "ask,bug", terminal: "task", change: func(t *testing.T) { t.Setenv(gateAgentVariable, "1") }, want: "an agent a gate started speaks for no goblin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := registeredCFOStage(t, "goblin", c.gate)
			inGoblinTerminal(t, h.State, c.terminal)
			if c.change != nil {
				c.change(t)
			}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, append([]string{"respond", "task", "--action", "approve", "--accept", c.accept}, c.flags...), &bytes.Buffer{})

			// Assert
			lines, _ := state.TailStatus(h.State, "task", 10)
			pending, _ := wake.Pending(h.State)
			if err == nil || !strings.Contains(err.Error(), c.want) || len(runner.native) != 0 || len(lines) != 0 || len(pending) != 0 {
				t.Fatalf("err = %v, %d native calls, status %q, wakes %+v; want it refused saying %q with nothing recorded or sent", err, len(runner.native), lines, pending, c.want)
			}
		})
	}
}

// The reason an approved Test exception keeps is the CFO's decision too. A
// goblin's is refused before anything is recorded or sent, a gate the native
// engine would drop it from is refused by the driver, and the CFO's reaches
// both the native --reason and the task's own audit (#1422).
func TestPipelineTestExceptionReasonIsHonouredOnlyFromTheRegisteredCFO(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	const reason = "the only failure was the sandbox's missing compiler, read in the step log"
	testException := pipeline.Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"test-2","action":"ask-user"}]}`}
	for _, caller := range []string{"goblin", "cfo"} {
		t.Run(caller, func(t *testing.T) {
			// Arrange
			h, nm, runner := registeredCFOStage(t, caller, testException)

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "test-2", "--reason", reason}, &bytes.Buffer{})
			lines, _ := state.TailStatus(h.State, "task", 10)

			// Assert
			if caller == "goblin" {
				if err == nil || !strings.Contains(err.Error(), "does not run under the registered CFO") || len(runner.native) != 0 || len(lines) != 0 {
					t.Fatalf("a goblin reasoning its own Test exception: %v, %d native calls, status %q; want it refused with nothing recorded or sent", err, len(runner.native), lines)
				}
				return
			}
			if err != nil || len(runner.native) != 1 || strings.Join(runner.native[0].Args, " ") != "axi respond --step test --action approve --reason "+reason {
				// The request carries the native terminal's environment, so a
				// failure names its arguments and never the whole request.
				t.Fatalf("the CFO approving a Test exception: %v, want one approve carrying the reason", err)
			}
			if len(lines) != 2 || !strings.HasSuffix(lines[0], "pipeline-findings-accepted: step=test run=run round=1 findings=test-2 by=cfo") || !strings.HasSuffix(lines[1], `pipeline-approval-reason: step=test run=run round=1 reason="`+reason+`" by=cfo`) {
				t.Fatalf("status log %q, want the acceptance and the reason audited before the gate was answered", lines)
			}
		})
	}
	t.Run("a required environment refusal after configuration changes", func(t *testing.T) {
		for _, configuration := range []string{"required", "changed", "removed"} {
			t.Run(configuration, func(t *testing.T) {
				gate := pipeline.Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"test-command-no-turn","action":"ask-user","description":"The wrapper reported: cfo gate turn: the command did not start: required test environment: .venv is missing. Respond with fix to run it again."}]}`}
				h, nm, runner := registeredCFOStage(t, "cfo", gate)
				h.Data = filepath.Join(h.Root, "data")
				manifest := filepath.Join(h.Data, "projects", "project", "project.json")
				if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifest, []byte(`{"project":"project","test_environment":{"python_version":"3.12.10"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
				switch configuration {
				case "changed":
					if err := os.WriteFile(manifest, []byte(`{"project":"project","test_environment":{"python_version":"3.12.11"}}`), 0o600); err != nil {
						t.Fatal(err)
					}
				case "removed":
					if err := os.Remove(manifest); err != nil {
						t.Fatal(err)
					}
				}
				for _, flags := range [][]string{nil, {"--accept", "test-command-no-turn"}, {"--reason", "the environment is repaired"}, {"--accept", "test-command-no-turn", "--reason", "the environment is repaired"}} {
					err := pipelineCommand(t.Context(), h, nm, runner, append([]string{"respond", "task", "--action", "approve"}, flags...), &bytes.Buffer{})
					lines, _ := state.TailStatus(h.State, "task", 10)
					if err == nil || !strings.Contains(err.Error(), "--action fix --findings test-command-no-turn") || len(runner.native) != 0 || len(lines) != 0 {
						t.Fatalf("flags=%v err=%v native calls=%d status=%q; want refusal without an approval or audit", flags, err, len(runner.native), lines)
					}
				}

				err := pipelineCommand(t.Context(), h, nm, runner, []string{"respond", "task", "--action", "fix", "--findings", "test-command-no-turn"}, &bytes.Buffer{})
				if err != nil || len(runner.native) != 1 || strings.Join(runner.native[0].Args, " ") != "axi respond --step test --action fix --findings test-command-no-turn" {
					t.Fatalf("retry err=%v native calls=%d; want the existing concrete retry", err, len(runner.native))
				}
			})
		}
	})
	t.Run("a gate the engine would drop it from", func(t *testing.T) {
		// Arrange
		h, nm, runner := registeredCFOStage(t, "cfo", pipeline.Gate{RunID: "run", StepID: "step", Step: "review", Status: "awaiting_approval", Round: 1, Findings: `{"findings":[{"id":"note","action":"no-op"}]}`})

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--reason", reason}, &bytes.Buffer{})
		lines, _ := state.TailStatus(h.State, "task", 10)

		// Assert
		if err == nil || !strings.Contains(err.Error(), "applies only to an approve of the test step") || len(runner.native) != 0 || len(lines) != 0 {
			t.Fatalf("a reason on a review gate: %v, %d native calls, status %q; want it refused with nothing recorded or sent", err, len(runner.native), lines)
		}
	})
	// A --reason "$WHY" with WHY unset gives an empty reason, which approved
	// the Test exception with no reason and no audit of one (issue 1585).
	t.Run("an empty reason", func(t *testing.T) {
		// Arrange
		h, nm, runner := registeredCFOStage(t, "cfo", testException)

		// Act
		err := pipelineCommand(context.Background(), h, nm, runner, []string{"respond", "task", "--action", "approve", "--accept", "test-2", "--reason", ""}, &bytes.Buffer{})
		lines, _ := state.TailStatus(h.State, "task", 10)

		// Assert
		if err == nil || !strings.Contains(err.Error(), "--reason is empty") || len(runner.native) != 0 || len(lines) != 0 {
			t.Fatalf("an empty reason: %v, %d native calls, status %q; want it refused with nothing recorded or sent", err, len(runner.native), lines)
		}
	})
}

func TestPipelineConfiguredTestDispositionKeepsCFOAuthorityAndNativeRefusals(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CFO registration is verified on Windows only")
	}
	const report = `{"findings":[{"id":"test-1","severity":"error","category":"test-command","description":"configured test command failed with exit code 4"}],"summary":"FAIL: configured baseline"}`
	const reason = "accept the recorded baseline failure after inspecting its evidence"
	for _, test := range []struct {
		name           string
		caller         string
		flags          []string
		result         *execx.Result
		nativeError    error
		refusal        string
		isNativeCalled bool
		isStaleCFO     bool
	}{
		{name: "primary CFO", caller: "cfo", isNativeCalled: true},
		{name: "worker", caller: "goblin", refusal: "registered primary CFO"},
		{name: "stale CFO registration", caller: "cfo", isStaleCFO: true, refusal: "registered primary CFO"},
		{name: "stale requested run", caller: "cfo", flags: []string{"--action", "approve", "--accept", "test-1", "--reason", reason, "--run", "old"}, refusal: "no longer the latest run"},
		{name: "no reason", caller: "cfo", flags: []string{"--action", "approve", "--accept", "test-1"}, refusal: "unresolved"},
		{name: "blank reason", caller: "cfo", flags: []string{"--action", "approve", "--accept", "test-1", "--reason", " "}, refusal: "must say why"},
		{name: "empty reason", caller: "cfo", flags: []string{"--action", "approve", "--accept", "test-1", "--reason", ""}, refusal: "--reason is empty"},
		{name: "extra acceptance", caller: "cfo", flags: []string{"--action", "approve", "--accept", "test-1,other", "--reason", reason}, refusal: "exactly"},
		{name: "native refusal", caller: "cfo", result: &execx.Result{ExitCode: 1, Stdout: []byte("error: recorded Test binding changed\n")}, refusal: "native command exited 1", isNativeCalled: true},
		{name: "native unsupported run binding", caller: "cfo", result: &execx.Result{ExitCode: 2, Stderr: []byte("unknown flag: --run\n")}, refusal: "native command exited 2", isNativeCalled: true},
		{name: "native execution error", caller: "cfo", result: &execx.Result{}, nativeError: errors.New("native transport refused"), refusal: "native transport refused", isNativeCalled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			gate := pipeline.Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 3, Findings: report}
			h, nm, runner := registeredCFOStage(t, test.caller, gate)
			runner.nativeResult, runner.nativeError = test.result, test.nativeError
			if test.isStaleCFO {
				if err := os.Remove(filepath.Join(h.State, "primary.json")); err != nil {
					t.Fatal(err)
				}
			}
			commands := taskWorktreesRunner{Runner: runner, branches: map[string]string{runner.worktree: "feat/policy"}, runBranch: "feat/policy"}
			selectionPath := filepath.Join(h.State, "tasktmp", "task", "pipeline.json")
			before, err := os.ReadFile(selectionPath)
			if err != nil {
				t.Fatal(err)
			}
			flags := test.flags
			if flags == nil {
				flags = []string{"--action", "approve", "--accept", "test-1", "--reason", reason}
			}
			var out bytes.Buffer

			// Act
			err = pipelineCommand(t.Context(), h, nm, commands, append([]string{"respond", "task"}, flags...), &out)

			// Assert
			if test.refusal == "" && err != nil || test.refusal != "" && (err == nil || !strings.Contains(err.Error(), test.refusal)) {
				t.Fatalf("got %v; want refusal %q", err, test.refusal)
			}
			after, readErr := os.ReadFile(selectionPath)
			if readErr != nil || !bytes.Equal(before, after) || runner.gate != gate {
				t.Fatalf("disposition changed policy, gate evidence or budgets: %v", readErr)
			}
			lines, err := state.TailStatus(h.State, "task", 10)
			if err != nil {
				t.Fatal(err)
			}
			if !test.isNativeCalled {
				if len(runner.native) != 0 || len(lines) != 0 {
					t.Fatalf("refusal sent %d native commands or wrote audit %q", len(runner.native), lines)
				}
				return
			}
			want := []string{"axi", "respond", "--step", "test", "--action", "approve", "--reason", reason, "--run", "run"}
			if len(runner.native) != 1 || !slices.Equal(runner.native[0].Args, want) || runner.native[0].Dir != runner.worktree {
				t.Fatalf("got %d native commands; want one bound Test approval in the task worktree", len(runner.native))
			}
			if len(lines) != 2 || !strings.HasSuffix(lines[0], "pipeline-findings-accepted: step=test run=run round=3 findings=test-1 by=cfo") || !strings.HasSuffix(lines[1], `pipeline-approval-reason: step=test run=run round=3 reason="`+reason+`" by=cfo`) {
				t.Fatalf("audit %q; want exact acceptance and operator reason", lines)
			}
			if test.result != nil && (!strings.Contains(out.String(), string(test.result.Stdout)) || !strings.Contains(out.String(), string(test.result.Stderr))) {
				t.Fatalf("native refusal evidence was lost: %q", out.String())
			}
		})
	}
}

func TestPipelineFixTranslatesCurrentNoOpsWithoutArbitraryIgnores(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the fixture registers the primary CFO on Windows only")
	}
	const report = `{"findings":[{"id":"test-1","action":"auto-fix"},{"id":"configured-test-failure","action":"no-op"},{"id":"test-4","action":"ask-user"},{"id":"build-cleanup-blocked","action":"no-op"}]}`
	for _, test := range []struct {
		name           string
		extra          []string
		result         *execx.Result
		refusal        string
		isNativeCalled bool
	}{
		{name: "verified automatic translation", isNativeCalled: true},
		{name: "unknown arbitrary ignore", extra: []string{"--ignore", "unknown"}, refusal: "flag provided but not defined"},
		{name: "actionable arbitrary ignore", extra: []string{"--ignore", "test-4"}, refusal: "flag provided but not defined"},
		{name: "duplicate arbitrary ignore", extra: []string{"--ignore", "configured-test-failure,configured-test-failure"}, refusal: "flag provided but not defined"},
		{name: "native earlier-fix refusal", result: &execx.Result{ExitCode: 2, Stdout: []byte("error: configured-test-failure already chosen to fix in an earlier round of this step\n")}, refusal: "native command exited 2", isNativeCalled: true},
		{name: "native unaccounted finding refusal", result: &execx.Result{ExitCode: 2, Stdout: []byte("error: new-finding not addressed: list it in --findings or --ignore\n")}, refusal: "native command exited 2", isNativeCalled: true},
		{name: "unsupported native ignore flag", result: &execx.Result{ExitCode: 2, Stderr: []byte("unknown flag: --ignore\n")}, refusal: "native command exited 2", isNativeCalled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			gate := pipeline.Gate{RunID: "run", StepID: "step", Step: "test", Status: "awaiting_approval", Round: 2, Findings: report}
			h, nm, runner := registeredCFOStage(t, "cfo", gate)
			// A Test fix round is version 6's, as version 7 starts none.
			atVersionSix(t, h, nm, "task")
			runner.nativeResult = test.result
			arguments := append([]string{"respond", "task", "--action", "fix", "--findings", "test-1,test-4"}, test.extra...)
			var out bytes.Buffer

			// Act
			err := pipelineCommand(t.Context(), h, nm, runner, arguments, &out)

			// Assert
			if test.refusal == "" && err != nil || test.refusal != "" && (err == nil || !strings.Contains(err.Error(), test.refusal)) {
				t.Fatalf("got %v; want refusal %q", err, test.refusal)
			}
			if runner.gate != gate {
				t.Fatal("translation changed the parked gate or its budgets")
			}
			if !test.isNativeCalled {
				if len(runner.native) != 0 {
					t.Fatalf("refusal sent %d native commands", len(runner.native))
				}
				return
			}
			want := []string{"axi", "respond", "--step", "test", "--action", "fix", "--findings", "test-1,test-4", "--ignore", "configured-test-failure,build-cleanup-blocked", "--run", "run"}
			if len(runner.native) != 1 || !slices.Equal(runner.native[0].Args, want) || runner.native[0].Dir != runner.worktree {
				t.Fatalf("got %d native commands; want one exact bound fix with verified no-ops", len(runner.native))
			}
			if test.result != nil && (!strings.Contains(out.String(), string(test.result.Stdout)) || !strings.Contains(out.String(), string(test.result.Stderr))) {
				t.Fatalf("native refusal cause was lost: %q", out.String())
			}
		})
	}
}
