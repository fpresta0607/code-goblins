package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

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
			choice := state.EngineChoice{ID: meta.ID, Generation: meta.SpawnGen, Harness: "pi", Model: "anthropic/claude-opus-5-5", Effort: "high", When: "resume"}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
		}, "resume it on claude or codex"},
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
