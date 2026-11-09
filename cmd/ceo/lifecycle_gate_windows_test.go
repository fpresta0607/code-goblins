package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type absentResumeGate struct {
	queryError error
}

func (runner absentResumeGate) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	switch request.Name {
	case "git":
		return execx.Result{Stdout: []byte("refactor/current-task\n")}, nil
	case "sqlite3":
		return execx.Result{Stdout: []byte("[]")}, runner.queryError
	default:
		return execx.Result{}, errors.New("unexpected command: " + request.Name)
	}
}

func TestResumeDoesNotInventAGateForADirectPRBranch(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       string
		queryError error
		want       string
	}{
		{name: "direct PR with a saved gate from earlier work", mode: "direct-PR"},
		{name: "managed validation still needs its evidence", mode: "no-mistakes", want: "no gate evidence"},
		{name: "unreadable validation is not absent", mode: "direct-PR", queryError: errors.New("database locked"), want: "state query failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, root := gateHome(t, versionSixPolicy(t, pipeline.Reviewer{}), operatorMachineConfig)
			t.Setenv("CEO_HOME", h.Root)
			t.Setenv("CEO_STATE_OVERRIDE", h.State)
			t.Setenv("CEO_PROJECTS_ROOT", t.TempDir())
			meta := state.TaskMeta{ID: "task", Backend: "native", Mode: test.mode, SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root}
			prior := state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "old-run", GateIntent: "validate earlier branch", GateHead: strings.Repeat("a", 40)}
			commands := absentResumeGate{queryError: test.queryError}
			isSwitched := false
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				isSwitched = true
				if request.ID != meta.ID || request.Generation != meta.SpawnGen || !request.IsResume || !request.ForceDirty {
					t.Errorf("resume lost its task or retained work: %+v", request)
				}
				return spawn.SwitchResult{}, nil
			}}

			// Act
			err := resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: root, Commands: commands}, meta, prior, nil)

			// Assert
			if test.want == "" {
				if err != nil || !isSwitched {
					t.Fatalf("direct-PR resume = %v, switched = %v; no run owns its current branch", err, isSwitched)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) || isSwitched {
				t.Fatalf("resume = %v, switched = %v, want refusal %q", err, isSwitched, test.want)
			}
		})
	}
}

func TestDirectPRResumePreservesAGateThatOwnsItsBranch(t *testing.T) {
	retained := &resumedGateRunner{}
	for _, test := range []struct {
		name     string
		commands execx.Runner
		want     string
	}{
		{name: "retain its saved gate", commands: retained},
		{name: "refuse another run", commands: refusingGateRunner{run: "other-run", intent: "other work"}, want: "another validation run owns the branch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, root := gateHome(t, versionSixPolicy(t, pipeline.Reviewer{}), operatorMachineConfig)
			meta := state.TaskMeta{ID: "task", Backend: "native", Mode: "direct-PR", SpawnGen: "generation-1", Project: h.Root, Worktree: h.Root}
			prior := state.Lifecycle{ID: meta.ID, Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}
			isSwitched := false
			runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
				isSwitched = true
				return spawn.SwitchResult{}, nil
			}}

			// Act
			err := resumeTask(t.Context(), h, runtime, test.commands, pipeline.Reader{Root: root, Commands: test.commands}, meta, prior, nil)

			// Assert
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) || isSwitched {
					t.Fatalf("resume = %v, switched = %v", err, isSwitched)
				}
			} else {
				if err != nil || !isSwitched || len(retained.native) != 0 {
					t.Fatalf("resume = %v, switched = %v, native calls = %d", err, isSwitched, len(retained.native))
				}
			}
		})
	}
}

func TestResumeRestoresContributorWithoutSelectingValidationAgents(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *state.TaskMeta, home.Home)
	}{
		{"changed frozen record", func(_ *testing.T, meta *state.TaskMeta, _ home.Home) { meta.PipelineHash = strings.Repeat("0", 64) }},
		{"older gate policy", func(t *testing.T, meta *state.TaskMeta, _ home.Home) {
			older := legacyPipelineSelection(t, "ordinary")
			if err := older.Save(filepath.Join(meta.TaskTmp, "pipeline.json")); err != nil {
				t.Fatal(err)
			}
			meta.PipelineHash, meta.PipelineClass = older.Hash, older.Class
		}},
		{"returning harness differs from validation", func(t *testing.T, meta *state.TaskMeta, h home.Home) {
			choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "codex", Model: "selected-model", Effort: "high", When: "resume"}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			policy := versionSixPolicy(t, pipeline.Reviewer{})
			h, root := gateHome(t, policy, operatorMachineConfig)
			gatedTask(t, h, "task", policy, claudeGoblin)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.SpawnGen = "native", "generation-1"
			test.change(t, &meta, h)
			commands := &resumedGateRunner{}
			isSwitched := false
			runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
				isSwitched = true
				return spawn.SwitchResult{}, nil
			}}
			prior := state.Lifecycle{ID: meta.ID, Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}
			// Act
			err = resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: root, Commands: commands}, meta, prior, nil)
			// Assert
			if err != nil || !isSwitched || len(commands.native) != 0 {
				t.Fatalf("resume = %v, switched = %v, native calls = %d", err, isSwitched, len(commands.native))
			}
			frozen, err := pipeline.LoadSelection(filepath.Join(meta.TaskTmp, "pipeline.json"))
			if err != nil || frozen.Class != meta.PipelineClass {
				t.Fatalf("retained policy = %+v, %v", frozen, err)
			}
		})
	}
}
