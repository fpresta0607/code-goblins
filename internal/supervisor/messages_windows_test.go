package supervisor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-08: "message queued to chief no matter whats running
// smoothly". A message he types to a goblin on the board is taken at once and
// delivered once, whatever the goblin is doing.

// messageTo queues the Overlord's message to task's goblin as the board
// sends it, and returns the action the store took.
func messageTo(t *testing.T, store *Store, meta state.TaskMeta, id, text string) Action {
	t.Helper()
	queued, err := store.Queue(Action{ID: id, Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: text})
	if err != nil {
		t.Fatalf("the message was refused: %v", err)
	}
	return queued
}

func actionOf(store *Store, id string) Action {
	actions := store.Snapshot().Actions
	return actions[slices.IndexFunc(actions, func(a Action) bool { return a.ID == id })]
}

// atPhase records meta's lifecycle in phase, as its action left it.
func atPhase(t *testing.T, stateDir string, meta state.TaskMeta, action, phase string) {
	t.Helper()
	if err := state.WriteLifecycle(stateDir, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: action + "-1", Action: action, Phase: phase, Updated: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestAMessageToAWorkingGoblinIsTypedIntoItsTerminalOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
	messageTo(t, store, meta, "message-1", "use the staging database")

	// Act
	err := store.ProcessOne(context.Background(), service.execute)
	typed := goblin.waitForLines(t, 1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(typed) != 1 || !strings.Contains(typed[0], "use the staging database") {
		t.Fatalf("the goblin received %q, want the message once", typed)
	}
	if action := actionOf(store, "message-1"); action.Status != "succeeded" && action.Awaiting == nil {
		t.Fatalf("message = %+v, want it delivered", action)
	}
}

func TestAMessageToAPausedGoblinIsKeptForItsResume(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "pause", "paused")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
	messageTo(t, store, meta, "message-1", "check the flaky test first")
	messageTo(t, store, meta, "message-2", "then rebase")

	// Act
	for range 2 {
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	record, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != "paused" || !strings.Contains(record.ResumeNote, "check the flaky test first") || !strings.Contains(record.ResumeNote, "then rebase") {
		t.Fatalf("record = %+v, want both messages kept for its resume", record)
	}
	for _, id := range []string{"message-1", "message-2"} {
		if action := actionOf(store, id); action.Status != "succeeded" {
			t.Fatalf("%s = %+v, want it taken", id, action)
		}
	}
}

func TestAMessageToAResumingGoblinWaitsAndIsTypedOnceItRuns(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "resume", "resuming")
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
	messageTo(t, store, meta, "message-1", "skip the docs")

	// Act
	waiting := store.ProcessOne(context.Background(), service.execute)
	during := actionOf(store, "message-1")
	typedDuring := goblin.lines(t)
	atPhase(t, h.State, meta, "resume", "running")
	time.Sleep(1100 * time.Millisecond)
	ran := store.ProcessOne(context.Background(), service.execute)
	typed := goblin.waitForLines(t, 1)

	// Assert
	if !strings.Contains(during.Message, "Waits for") || during.Status != "queued" || len(typedDuring) != 0 {
		t.Fatalf("while it resumed: %v, message %+v, typed %q, want it waiting with nothing typed", waiting, during, typedDuring)
	}
	if ran != nil || len(typed) != 1 || !strings.Contains(typed[0], "skip the docs") {
		t.Fatalf("once it ran: %v, typed %q, want the message typed once", ran, typed)
	}
}

func TestAMessageToTheCFOWaitsWhileNoCFORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)

	// Act
	queued, err := store.Queue(Action{ID: "message-1", Kind: "message", Text: "hello"})

	// Assert
	if err != nil || queued.Status != "queued" {
		t.Fatalf("message = %+v, %v, want it taken", queued, err)
	}
	if store.HasRunnable() {
		t.Fatal("a message to the CFO runs while no CFO runs")
	}
}
