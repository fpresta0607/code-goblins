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
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			t.Setenv("CFO_PROJECTS_ROOT", t.TempDir())
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
	restarted := &resumedGateRunner{}
	for _, test := range []struct {
		name     string
		commands execx.Runner
		want     string
	}{
		{name: "restart its saved gate", commands: restarted},
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
				if err != nil || !isSwitched || !restarted.isReplaced {
					t.Fatalf("resume = %v, switched = %v, gate replaced = %v", err, isSwitched, restarted.isReplaced)
				}
			}
		})
	}
}

// A task frozen before version 6 runs the chain its policy fixed, and that
// chain is whatever the machine config says. Once the machine config has moved
// on, cfo pipeline run refuses such a task until it is migrated, and the run
// Resume starts in place of a paused one is refused the same way, or it would
// run on a chain its policy never named.
func TestResumeStartsNoReplacementGateUnderAPolicyTheMachineHasMovedOnFrom(t *testing.T) {
	older, err := pipeline.Load(filepath.Join("..", "..", "config", "pipeline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if older.Version > 5 {
		t.Skipf("the checked-in policy is version %d, which fixes no chain to drift from", older.Version)
	}
	h, nm := gateHome(t, versionSixPolicy(t, pipeline.Reviewer{}), operatorMachineConfig)
	gatedTask(t, h, "task", older, claudeGoblin)
	meta, err := state.ReadTaskMeta(h.State, "task")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend, meta.SpawnGen = "native", "generation-1"
	commands := &resumedGateRunner{}
	isSwitched := false
	runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
		isSwitched = true
		return spawn.SwitchResult{}, nil
	}}
	prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}

	err = resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: nm, Commands: commands}, meta, prior, nil)

	if err == nil || !strings.Contains(err.Error(), "cfo pipeline migrate task") || !strings.Contains(err.Error(), "agent") {
		t.Fatalf("error = %v, want the drift named with the migration that clears it", err)
	}
	if len(commands.native) != 0 || isSwitched {
		t.Fatalf("native=%+v switched=%v, want nothing recovered, started or relaunched", commands.native, isSwitched)
	}
}

func TestResumeStartsNoReplacementGateItCannotSelectAgentsFor(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*testing.T, *state.TaskMeta, home.Home)
		want   string
	}{
		{"a goblin coming back on a harness no gate runs on", func(t *testing.T, meta *state.TaskMeta, h home.Home) {
			choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "grok", Model: "grok-4", Effort: "high", When: "resume"}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
		}, "resume it on a harness a gate can prove"},
		{"a snapshot its record does not match", func(_ *testing.T, meta *state.TaskMeta, _ home.Home) {
			meta.PipelineHash = strings.Repeat("0", 64)
		}, "cfo pipeline migrate task"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := versionSixPolicy(t, pipeline.Reviewer{})
			h, nm := gateHome(t, policy, operatorMachineConfig)
			gatedTask(t, h, "task", policy, claudeGoblin)
			meta, err := state.ReadTaskMeta(h.State, "task")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend, meta.SpawnGen = "native", "generation-1"
			test.change(t, &meta, h)
			commands := &resumedGateRunner{}
			runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
				return spawn.SwitchResult{}, nil
			}}
			prior := state.Lifecycle{ID: "task", Phase: "paused", Started: time.Now().Add(-time.Hour), GateRun: "run-1", GateIntent: "ship safely"}

			err = resumeTask(t.Context(), h, runtime, commands, pipeline.Reader{Root: nm, Commands: commands}, meta, prior, nil)

			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to say %q", err, test.want)
			}
			if len(commands.native) != 0 {
				t.Fatalf("native=%+v, want nothing recovered or started", commands.native)
			}
		})
	}
}
