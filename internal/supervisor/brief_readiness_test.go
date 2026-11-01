package supervisor

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSchedulerLeavesDraftsWaitingWithoutBlockingReadyWork(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **draft-task** - Draft (repo: example)\n- **next-task** - Ready (repo: example)\n")
	writeFile(t, filepath.Join(h.Data, "draft-task", "brief.md"), strings.ReplaceAll(strings.ReplaceAll(plainBrief, "next-task", "draft-task"), "ready: true", "ready: false"))
	writeFile(t, filepath.Join(h.Data, "next-task", "brief.md"), plainBrief)
	spawner.during = func(args []string) {
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: args[1], SpawnGen: "fake"}); err != nil {
			t.Error(err)
		}
	}

	// Act
	if err := handler.Service.checkFleet(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	calls := awaitDispatch(t, handler.Service, spawner, 1)

	// Assert
	if len(calls) != 1 || calls[0][1] != "next-task" {
		t.Fatalf("dispatches = %v, want ready task past the draft", calls)
	}
}

func TestDraftBriefDoesNotRaiseAnIdleFleetWake(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Draft (repo: example)", strings.ReplaceAll(plainBrief, "ready: true", "ready: false"))
	now := time.Now()

	// Act
	for _, at := range []time.Time{now, now.Add(idleWakeAfter + time.Minute)} {
		if err := handler.Service.checkFleet(t.Context(), at); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if wakes := fleetWakeRecords(t, h, "idle"); len(wakes) != 0 {
		t.Fatalf("draft raised runnable-work wakes: %+v", wakes)
	}
	if len(spawner.recorded()) != 0 {
		t.Fatal("draft launched")
	}
}

func TestSchedulerRetriesWhenBriefChangesDuringAdmission(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{err: errors.New("spawn refused"), output: fleet.ErrBriefNotReady.Error() + ": the brief changed during admission"}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ready (repo: example)", plainBrief)
	now := time.Now()

	// Act: the first start catches a concurrent edit, then the next scan retries.
	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, handler.Service, spawner, 1)
	spawner.err, spawner.output = nil, ""
	spawner.during = func(args []string) {
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: args[1], SpawnGen: "fake"}); err != nil {
			t.Error(err)
		}
	}
	if err := handler.Service.checkFleet(t.Context(), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	// Assert
	if calls := awaitDispatch(t, handler.Service, spawner, 2); len(calls) != 2 {
		t.Fatalf("dispatches = %v", calls)
	}
	if err := handler.Service.checkFleet(t.Context(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(spawner.recorded()) != 2 {
		t.Fatal("started the admitted identity twice")
	}
}

func TestReadyBriefStillHonorsAQueuedHold(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ready (repo: example) (hold: author reviewing)", plainBrief)
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

	// Act and assert
	for index := range 3 {
		if err := handler.Service.checkFleet(t.Context(), now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if len(spawner.recorded()) != 0 {
		t.Fatal("ready brief bypassed its hold")
	}
	writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- **next-task** - Ready (repo: example)\n")
	if err := handler.Service.checkFleet(t.Context(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, handler.Service, spawner, 1)
}
