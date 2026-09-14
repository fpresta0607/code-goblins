package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
	"gopkg.in/yaml.v3"
)

type pipelineRunner struct {
	gate       pipeline.Gate
	native     []execx.Request
	worktree   string
	activeRuns int
}

type pipelineStartRunner struct {
	worktree          string
	advance           bool
	race              string
	mutateConfig      func()
	remoteRead        int
	branchRead        int
	statusRead        int
	headRead          int
	trackingRead      int
	taskConfigRead    int
	trustedConfigRead int
	worktreeRead      int
	roleStarted       bool
	native            []execx.Request
	nonce             string
	generation        string
}

func (r *pipelineStartRunner) Run(_ context.Context, q execx.Request) (execx.Result, error) {
	const trusted = "0123456789abcdef0123456789abcdef01234567"
	switch q.Name {
	case "sqlite3":
		sql := q.Args[len(q.Args)-1]
		if strings.Contains(sql, "SELECT default_branch FROM repos") {
			return execx.Result{Stdout: []byte(`[{"default_branch":"main"}]`)}, nil
		}
		if strings.Contains(sql, "SELECT id,default_branch FROM repos") {
			return execx.Result{Stdout: []byte(`[{"id":"repo","default_branch":"main"}]`)}, nil
		}
		if strings.Contains(sql, "SELECT runs.status") {
			return execx.Result{Stdout: []byte(`[]`)}, nil
		}
		if strings.Contains(sql, "launch_validation_generation") {
			config, err := os.ReadFile(filepath.Join(filepath.Dir(q.Args[len(q.Args)-2]), "config.yaml"))
			if err != nil {
				return execx.Result{}, err
			}
			return execx.Result{Stdout: []byte(fmt.Sprintf(`[{"run_id":"run-bound","repo_id":"repo","working_path":%q,"branch":"feat/policy","submitted_head_sha":%q,"launch_nonce":%q,"validation_generation":%q,"trusted_sha":%q,"agent":"codex","model":"gpt-5.6-sol","global_config_hex":%q}]`, filepath.ToSlash(filepath.Dir(filepath.Dir(r.worktree))), trusted, r.nonce, r.generation, trusted, fmt.Sprintf("%x", config)))}, nil
		}
	case "git":
		switch strings.Join(q.Args, " ") {
		case "rev-parse --show-toplevel":
			r.worktreeRead++
			if r.race == "global config" && r.worktreeRead == 3 && r.mutateConfig != nil {
				r.mutateConfig()
			}
			return execx.Result{Stdout: []byte(r.worktree + "\n")}, nil
		case "symbolic-ref --quiet --short HEAD":
			r.branchRead++
			if r.race == "branch" && r.branchRead >= 3 {
				return execx.Result{Stdout: []byte("feat/mutated\n")}, nil
			}
			return execx.Result{Stdout: []byte("feat/policy\n")}, nil
		case "status --porcelain --untracked-files=all":
			r.statusRead++
			if r.race == "dirty" && r.statusRead >= 2 {
				return execx.Result{Stdout: []byte(" M changed.go\n")}, nil
			}
			return execx.Result{}, nil
		case "rev-parse --verify HEAD^{commit}":
			r.headRead++
			if r.race == "head" && r.headRead >= 2 {
				return execx.Result{Stdout: []byte("89abcdef0123456789abcdef0123456789abcdef\n")}, nil
			}
			return execx.Result{Stdout: []byte(trusted + "\n")}, nil
		case "show HEAD:.no-mistakes.yaml":
			r.taskConfigRead++
			if r.race == "effective primary" && r.taskConfigRead >= 2 {
				return execx.Result{Stdout: []byte("agent: claude\nauto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")}, nil
			}
			return execx.Result{Stdout: []byte("auto_fix: {review: 0, test: 1, lint: 1, rebase: 1, ci: 1}\n")}, nil
		case "ls-remote --symref origin HEAD":
			head := trusted
			if r.advance && r.remoteRead > 0 {
				head = "89abcdef0123456789abcdef0123456789abcdef"
			}
			r.remoteRead++
			return execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\n" + head + "\tHEAD\n")}, nil
		case "rev-parse --verify refs/remotes/origin/main":
			r.trackingRead++
			if r.race == "trusted ref" && r.trackingRead >= 2 {
				return execx.Result{Stdout: []byte("89abcdef0123456789abcdef0123456789abcdef\n")}, nil
			}
			return execx.Result{Stdout: []byte(trusted + "\n")}, nil
		case "show " + trusted + ":.no-mistakes.yaml":
			r.trustedConfigRead++
			if r.race == "trusted config" && r.trustedConfigRead >= 2 {
				return execx.Result{Stdout: []byte("agent: claude\nauto_fix: {review: 0}\n")}, nil
			}
			return execx.Result{Stdout: []byte("auto_fix: {review: 0}\n")}, nil
		}
	case "no-mistakes":
		r.native = append(r.native, q)
		for i, arg := range q.Args {
			if arg == "--launch-nonce" && i+1 < len(q.Args) {
				r.nonce = q.Args[i+1]
			}
			if arg == "--validation-generation" && i+1 < len(q.Args) {
				r.generation = q.Args[i+1]
			}
		}
		r.roleStarted = true
		return execx.Result{Stdout: []byte(fmt.Sprintf("launch_receipt:\n  run_id: run-bound\n  disposition: created\n  launch_nonce: %s\n  validation_generation: %s\n  branch: feat/policy\n  head_sha: %s\n  submitted_head_sha: %s\n  intent_digest: %x\ngate: review\n", r.nonce, r.generation, trusted, trusted, sha256.Sum256([]byte("ship safely"))))}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected start command: %#v", q)
}

type pipelineSwitchRunner struct {
	statusReady   chan struct{}
	statusRelease chan struct{}
	alive         bool
}

func (r *pipelineSwitchRunner) Run(ctx context.Context, q execx.Request) (execx.Result, error) {
	switch q.Name {
	case "git":
		if len(q.Args) > 0 && q.Args[0] == "status" {
			close(r.statusReady)
			select {
			case <-r.statusRelease:
				return execx.Result{}, nil
			case <-ctx.Done():
				return execx.Result{}, ctx.Err()
			}
		}
	case "herdr":
		if len(q.Args) >= 3 && q.Args[0] == "pane" && q.Args[1] == "get" {
			return execx.Result{Stdout: []byte(`{"result":{"pane":{"pane_id":"pane-1"}}}`)}, nil
		}
		if len(q.Args) >= 3 && q.Args[0] == "agent" && q.Args[1] == "get" {
			if r.alive {
				return execx.Result{Stdout: []byte(`{"result":{"agent":{"agent_status":"working"}}}`)}, nil
			}
			return execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`)}, nil
		}
		if len(q.Args) >= 4 && q.Args[0] == "pane" && q.Args[1] == "send-text" {
			return execx.Result{Stdout: []byte(`{"result":{}}`)}, nil
		}
		if len(q.Args) >= 4 && q.Args[0] == "pane" && q.Args[1] == "send-keys" {
			if q.Args[3] == "enter" {
				r.alive = true
			}
			return execx.Result{Stdout: []byte(`{"result":{}}`)}, nil
		}
	}
	return execx.Result{}, fmt.Errorf("unexpected switch command: %#v", q)
}

type pipelineSwitchGit struct {
	worktree string
}

func (g pipelineSwitchGit) Acquire(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected worktree acquisition")
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
		Args:        []string{"--test"},
		Env:         map[string]string{"GOTMPDIR": spec.GoTmp},
		PromptFile:  spec.BriefPath,
		TypedLaunch: true,
		Executable:  string(a.kind),
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
		if strings.Contains(q.Args[len(q.Args)-1], "COUNT(*) AS n FROM runs") {
			return execx.Result{Stdout: []byte(fmt.Sprintf(`[{"n":%d}]`, r.activeRuns))}, nil
		}
		data, err := json.Marshal([]pipeline.Gate{r.gate})
		return execx.Result{Stdout: data}, err
	case "no-mistakes":
		r.native = append(r.native, q)
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

func TestPipelineMigrateRejectsConfigApplyBeforeIdleLock(t *testing.T) {
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
	policy, err := pipeline.Load(filepath.Join(root, "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(nm, "config.yaml")
	config, _, err := pipeline.Render([]byte("{}"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	old := legacyPipelineSelection(t, "ordinary")
	snapshot := filepath.Join(tmp, "pipeline.json")
	if err := old.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", TaskTmp: tmp, PipelineClass: old.Class, PipelineHash: old.Hash}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	otherPolicy := old.Policy
	idleCalls := 0
	idle := func(ctx context.Context) (func() error, error) {
		idleCalls++
		applied, err := (pipeline.Config{
			Path:   configPath,
			Policy: otherPolicy,
			Idle: func(context.Context) (func() error, error) {
				return func() error { return nil }, nil
			},
		}).Apply(ctx)
		if err != nil {
			return nil, err
		}
		if len(applied.Drift) == 0 {
			return nil, errors.New("config apply did not change the shared policy")
		}
		return func() error { return nil }, nil
	}

	err = migratePipelinePolicy(context.Background(), h, nm, idle, meta, old, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "apply current shared config before migrating tasks") {
		t.Fatalf("migration error=%v, want locked config drift refusal", err)
	}
	if idleCalls != 1 {
		t.Fatalf("idle acquisitions=%d, want 1", idleCalls)
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
	for _, test := range []struct {
		name       string
		activeRuns int
		useCurrent bool
	}{
		{name: "active-legacy", activeRuns: 1},
		{name: "active-current", activeRuns: 1, useCurrent: true},
		{name: "idle"},
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
			if test.useCurrent {
				old, err = policy.Select("high-risk")
				if err != nil {
					t.Fatal(err)
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
			err = pipelineCommand(context.Background(), h, nm, runner, []string{"migrate", "task"}, &out)
			if test.activeRuns != 0 {
				if !errors.Is(err, pipeline.ErrBusy) {
					t.Fatalf("active migration error=%v", err)
				}
				unchanged, loadErr := pipeline.LoadSelection(snapshot)
				if loadErr != nil || unchanged != old {
					t.Fatalf("active migration changed snapshot: %+v %v", unchanged, loadErr)
				}
				updated, readErr := state.ReadTaskMeta(h.State, "task")
				if readErr != nil || updated.PipelineHash != old.Hash || updated.PipelineClass != old.Class {
					t.Fatalf("active migration changed metadata: %+v %v", updated, readErr)
				}
				if status, statusErr := state.TailStatus(h.State, "task", 1); statusErr != nil || len(status) != 0 {
					t.Fatalf("active migration wrote audit: %v %v", status, statusErr)
				}
				return
			}
			if err != nil {
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
			if migrated.Policy.Version != 2 || migrated.Class != old.Class || migrated.ReviewCycles != old.ReviewCycles || updated.PipelineHash != migrated.Hash || updated.PipelineClass != old.Class {
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
	rootMapping := configDocument.Content[0]
	for i := 0; i < len(rootMapping.Content); i += 2 {
		if rootMapping.Content[i].Value != "agent_path_override" {
			continue
		}
		agentPaths := rootMapping.Content[i+1]
		for j := 0; j < len(agentPaths.Content); j += 2 {
			if agentPaths.Content[j].Value == "codex" {
				agentPaths.Content[j+1].Value = "C:/tools/openrouter-codex.exe"
			}
		}
	}
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

	runner := &pipelineRunner{}
	err = resumePipelinePolicyMigration(context.Background(), h, pipeline.Reader{Commands: runner, Root: nm}, meta)
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
	deadline := time.Now().Add(5 * time.Second)
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
		ID:               "task",
		Mode:             "no-mistakes",
		Worktree:         wt,
		Project:          project,
		TaskTmp:          tmp,
		Brief:            brief,
		PipelineClass:    old.Class,
		PipelineHash:     old.Hash,
		Backend:          "herdr",
		Harness:          string(harness.Claude),
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "workspace-1",
		HerdrTabID:       "tab-1",
		HerdrPaneID:      "pane-1",
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	switchRunner := &pipelineSwitchRunner{statusReady: make(chan struct{}), statusRelease: make(chan struct{})}
	switchService := spawn.Service{
		Herdr:     &herdr.Client{Commands: switchRunner, Session: "fleet"},
		Worktrees: worktree.Service{Commands: switchRunner, Git: pipelineSwitchGit{worktree: wt}, DataDir: h.Data},
		Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
			harness.Claude: pipelineSwitchAdapter{kind: harness.Claude},
			harness.Kimi:   pipelineSwitchAdapter{kind: harness.Kimi},
		}},
		Commands: switchRunner,
		StateDir: h.State,
	}
	switchDone := make(chan error, 1)
	go func() {
		_, err := switchService.Switch(context.Background(), spawn.SwitchRequest{ID: meta.ID, Harness: harness.Kimi, Session: "fleet"})
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
	if err := <-switchDone; err != nil {
		t.Fatalf("switch: %v", err)
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
	if updated.PipelineHash != want.Hash || updated.PipelineClass != want.Class || updated.Harness != string(harness.Kimi) {
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

func TestPipelineRunBindsAndVerifiesNativeLaunch(t *testing.T) {
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
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
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
	var out bytes.Buffer
	err = pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out)
	if err != nil {
		t.Fatalf("pipelineCommand: %v", err)
	}
	if runner.remoteRead != 2 || len(runner.native) != 1 || !runner.roleStarted || runner.nonce == "" || !strings.HasPrefix(runner.generation, cfoValidationGenerationPrefix) {
		t.Fatalf("bound launch: remote_reads=%d native=%+v role_started=%v nonce=%q generation=%q", runner.remoteRead, runner.native, runner.roleStarted, runner.nonce, runner.generation)
	}
	if !strings.Contains(out.String(), "launch_receipt:") || !strings.Contains(out.String(), "pipeline launch: verified run run-bound") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestPipelineRunRefusesRemoteRaceBeforeNativeInvocation(t *testing.T) {
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
	meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
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
	runner := &pipelineStartRunner{worktree: wt, advance: true}
	err = pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("pipelineCommand error=%v, want remote race refusal", err)
	}
	if runner.remoteRead != 2 || len(runner.native) != 0 || runner.roleStarted {
		t.Fatalf("race crossed native boundary: remote_reads=%d native=%+v role_started=%v", runner.remoteRead, runner.native, runner.roleStarted)
	}
}

func TestPipelineRunRefusesEveryMutableRaceBeforeNativeInvocation(t *testing.T) {
	for _, race := range []string{"branch", "dirty", "head", "trusted ref", "trusted config", "effective primary", "global config"} {
		t.Run(race, func(t *testing.T) {
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
			meta := state.TaskMeta{ID: "task", Mode: "no-mistakes", Worktree: wt, Project: project, TaskTmp: tmp, PipelineClass: selection.Class, PipelineHash: selection.Hash}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			config, _, err := pipeline.Render([]byte("unowned: before\n"), p)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(nm, "config.yaml")
			if err := os.WriteFile(configPath, config, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "state.sqlite"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			runner := &pipelineStartRunner{worktree: wt, race: race}
			runner.mutateConfig = func() {
				changed := bytes.Replace(config, []byte("unowned: before"), []byte("unowned: after"), 1)
				if err := os.WriteFile(configPath, changed, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err = pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &bytes.Buffer{})
			if err == nil {
				t.Fatal("race was accepted")
			}
			if len(runner.native) != 0 || runner.roleStarted {
				t.Fatalf("race crossed native boundary: native=%+v role_started=%v error=%v", runner.native, runner.roleStarted, err)
			}
		})
	}
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
