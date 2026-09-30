package crewstate

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPausedTaskDoesNotBecomeUnknownWhenItsTerminalIsGone(t *testing.T) {
	directory := t.TempDir()
	meta := state.TaskMeta{ID: "task", SpawnGen: "current", Worktree: t.TempDir(), Backend: "native"}
	if err := state.WriteTaskMeta(directory, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(directory, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Reason: "Paused from the board"}); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(context.Background(), directory, meta.ID, fakeEndpoint{})
	if err != nil || got.State != Paused || got.Detail != "Paused from the board" {
		t.Fatalf("paused task = %+v %v", got, err)
	}
	meta.SpawnGen = "replacement"
	if err := state.WriteTaskMeta(directory, meta); err != nil {
		t.Fatal(err)
	}
	got, err = Resolve(context.Background(), directory, meta.ID, fakeEndpoint{})
	if err != nil || got.State == Paused {
		t.Fatalf("old pause hid a replacement task: %+v %v", got, err)
	}
}
