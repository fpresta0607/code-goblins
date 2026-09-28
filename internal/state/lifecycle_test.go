package state

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLifecycleStateSurvivesAReaderRestart(t *testing.T) {
	directory := t.TempDir()
	stamp := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	record := Lifecycle{ID: "task", Generation: "spawn-1", Operation: "pause-1", Action: "pause", Phase: "paused", Started: stamp, Updated: stamp, Session: "session-1", Handoff: "handoff.md", HandoffSaved: true, Kept: []string{"worktree", "branch"}}
	if err := WriteLifecycle(directory, record); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLifecycle(directory, "task")
	if err != nil || !reflect.DeepEqual(got, record) {
		t.Fatalf("restart read %+v, %v; want %+v", got, err, record)
	}
}

func TestLifecycleRejectsInvalidOrMismatchedState(t *testing.T) {
	directory := t.TempDir()
	for _, test := range []struct {
		name   string
		record Lifecycle
	}{
		{"escaping id", Lifecycle{ID: "../task", Generation: "g", Operation: "p", Action: "pause", Phase: "paused"}},
		{"unknown phase", Lifecycle{ID: "task", Generation: "g", Operation: "p", Action: "pause", Phase: "whatever"}},
		{"missing operation", Lifecycle{ID: "task", Generation: "g", Action: "pause", Phase: "paused"}},
		{"unknown action", Lifecycle{ID: "task", Generation: "g", Operation: "p", Action: "whatever", Phase: "paused"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := WriteLifecycle(directory, test.record); err == nil {
				t.Fatal("invalid state was accepted")
			}
		})
	}
	if err := os.MkdirAll(filepath.Join(directory, "lifecycle"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "lifecycle", "task.json"), []byte(`{"id":"other","generation":"g","operation":"p","action":"pause","phase":"paused"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLifecycle(directory, "task"); err == nil {
		t.Fatal("a different task's lifecycle was accepted")
	}
}
