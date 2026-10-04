package main

import (
	"context"
	"testing"
	"time"

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

func TestResumeUsesSavedSessionOnlyWithinOneDayOfThePause(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		age         time.Duration
		wantSession string
	}{
		{name: "recent pause", age: time.Hour, wantSession: "saved-session"},
		{name: "one day old", age: 24 * time.Hour},
		{name: "older pause", age: 48 * time.Hour},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := primaryHomeFixture(t)
			var received spawn.SwitchRequest
			runtime := commandRuntime{switchTask: func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				received = request
				return spawn.SwitchResult{}, nil
			}}
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Worktree: t.TempDir()}
			prior := state.Lifecycle{Session: "saved-session", Started: time.Now().Add(-testCase.age), Handoff: "retained-handoff.md", HandoffSaved: true, ResumeNote: "The Overlord answered: continue"}

			err := resumeTask(t.Context(), h, runtime, &resumeRunner{}, pipeline.Reader{}, meta, prior)

			if err != nil || received.ResumeSession != testCase.wantSession || received.ResumeHandoff != prior.Handoff || received.ID != meta.ID || received.ResumeNote != prior.ResumeNote {
				t.Fatalf("resume request=%+v error=%v", received, err)
			}
		})
	}
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
