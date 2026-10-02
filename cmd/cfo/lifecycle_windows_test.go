package main

import (
	"context"
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
