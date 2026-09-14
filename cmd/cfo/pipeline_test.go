package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type pipelineRunner struct {
	gate       pipeline.Gate
	native     []execx.Request
	worktree   string
	activeRuns int
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
