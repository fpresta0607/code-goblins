package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// liveGoblins records count native goblins whose terminal hosts run.
func liveGoblins(t *testing.T, h home.Home, count int) {
	t.Helper()
	for index := range count {
		id := fmt.Sprintf("running-task-%d", index)
		if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Backend: "native", SpawnGen: "generation-" + id}); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(host.Record{ID: id, HostPID: os.Getpid(), Started: time.Now().UTC(), Pipe: "fixture", Token: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(h.State, "hosts", id+".json"), string(data))
	}
}

// On 2026-10-07 the board refused a Start with "No free slot: 8 of 8 goblins
// live" while 9 GB of memory was free: a goblin count capped the fleet. Slots
// go by memory alone, so the ninth goblin starts.
func TestStartWithEightGoblinsLiveAndNineGBFreeStarts(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 9*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	liveGoblins(t, h, 8)

	// Act
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	if response.Code == 202 {
		waitStarted(t, handler, "next-task")
	}

	// Assert
	if response.Code != 202 {
		t.Fatalf("start=%d %s, want the ninth goblin started with 9 GB free", response.Code, response.Body)
	}
	if calls := spawner.recorded(); len(calls) != 1 || calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned", calls)
	}
}

func TestSchedulerStartsTheNextTaskWithEightGoblinsLiveAndNineGBFree(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 9*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	liveGoblins(t, h, 8)

	// Act
	if err := handler.Service.checkFleet(t.Context(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Assert
	if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned", calls)
	}
}

func TestSchedulerDoesNotResumeIntoAnotherAllowancePause(t *testing.T) {
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC()
	meta := pausedGoblin(t, h, "ready-task", "dependency", "date:2000-01-01T00:00:00Z", now.Add(-time.Hour))
	meta.Harness = "codex"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
		return quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Windows: []quota.Window{{ID: "week", Kind: "weekly", PercentUsed: 98, ResetsAt: now.Add(time.Hour)}}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 2, ResetsAt: now.Add(time.Hour), BoundedBy: []string{"week"}}}}}}, ""
	}

	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}

	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("resumed at the floor: %v", calls)
	}
}

// A goblin starts or resumes by itself only while memory and commit, less the
// about 1 GB its session takes, still meet the floor the machine's heavy line
// keeps for its next test run, the Overlord's rule of 2026-11-02 (#1926).
// The lower of the two is what is free, and the floor and a session exactly
// is enough.
func TestSlotHoldKeepsTheNextTestRunsFloorAndASession(t *testing.T) {
	const floor = 4*gigabyte + gigabyte/2
	for name, row := range map[string]struct {
		physical, commit uint64
		want             string
	}{
		"the floor and a session free":        {floor + gigabyte, floor + gigabyte, ""},
		"a byte short in physical memory":     {floor + gigabyte - 1, 40 * gigabyte, "5.4 GB of memory and commit is free; the next test run needs 4.5 GB and a new goblin 1 GB"},
		"commit short over the old 5 GB mark": {40 * gigabyte, 5*gigabyte + gigabyte/4, "5.2 GB of memory and commit is free; the next test run needs 4.5 GB and a new goblin 1 GB"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			memory := Memory{Available: row.physical, CommitAvailable: row.commit}

			// Act
			hold := SlotHold(floor, memory)

			// Assert
			if hold != row.want {
				t.Errorf("SlotHold = %q; want %q", hold, row.want)
			}
		})
	}
}
