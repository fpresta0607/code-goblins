package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type resumeRunner struct{ requests []execx.Request }

func (r *resumeRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, request)
	return execx.Result{Stdout: []byte("cfo/task\n")}, nil
}

func TestResumeRefusesATaskRecordedInHerdrBeforeTheGateRestarts(t *testing.T) {
	h := primaryHomeFixture(t)
	commands := &resumeRunner{}
	gate := pipeline.Reader{Root: t.TempDir(), Commands: commands}
	isSwitched := false
	runtime := commandRuntime{switchTask: func(context.Context, home.Home, spawn.SwitchRequest) (spawn.SwitchResult, error) {
		isSwitched = true
		return spawn.SwitchResult{}, nil
	}}
	meta := state.TaskMeta{ID: "task", SpawnGen: "session-1", Backend: "herdr", Project: "project", Worktree: t.TempDir()}
	prior := state.Lifecycle{ID: "task", Phase: "paused", GateRun: "run-1", GateIntent: "ship it"}

	err := resumeTask(context.Background(), h, runtime, commands, gate, meta, prior)

	want := `resume: task task runs in backend "herdr"; only a task in a native terminal can resume; retire it with cfo cleanup task --force-archive`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if len(commands.requests) != 0 || isSwitched {
		t.Fatalf("commands=%+v switched=%v; a Herdr task must be refused before the gate restarts or the switch runs", commands.requests, isSwitched)
	}
}

func TestResumeUsesSavedEngineOnlyForItsSessionAndKeepsFailedChoices(t *testing.T) {
	for _, test := range []struct {
		name, harness, generation, session string
		isFailed                           bool
	}{
		{"same harness", "claude", "s1", "session-1", false},
		{"new harness", "codex", "s1", "", false},
		{"stale choice", "codex", "s0", "session-1", false},
		{"failed resume", "codex", "s1", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := primaryHomeFixture(t)
			meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Backend: "native"}
			choice := state.EngineChoice{ID: meta.ID, Generation: test.generation, Harness: test.harness, Model: "selected-model", Effort: "high", When: "resume"}
			if err := state.WriteEngineChoice(h.State, choice); err != nil {
				t.Fatal(err)
			}
			var request spawn.SwitchRequest
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, selected spawn.SwitchRequest) (spawn.SwitchResult, error) {
				request = selected
				if test.isFailed {
					return spawn.SwitchResult{}, errors.New("launch failed")
				}
				return spawn.SwitchResult{}, nil
			}}

			err := resumeTask(context.Background(), h, runtime, &resumeRunner{}, pipeline.Reader{}, meta, state.Lifecycle{Session: "session-1", HandoffSaved: true, Handoff: "saved handoff"})

			if (err != nil) != test.isFailed || request.ResumeSession != test.session || request.ResumeHandoff != "saved handoff" || !request.ForceDirty {
				t.Fatalf("resume = %+v, %v", request, err)
			}
			if test.generation == meta.SpawnGen {
				if string(request.Harness) != choice.Harness || request.Model != choice.Model || request.Effort != choice.Effort {
					t.Fatalf("Resume ignored saved engine: %+v", request)
				}
			} else if request.Harness != "" || request.Model != "" || request.Effort != "" {
				t.Fatalf("Resume applied stale engine: %+v", request)
			}
			_, readErr := state.ReadEngineChoice(h.State, meta.ID)
			if test.isFailed && readErr != nil || !test.isFailed && test.generation == meta.SpawnGen && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("saved choice after Resume = %v", readErr)
			}
		})
	}
}
