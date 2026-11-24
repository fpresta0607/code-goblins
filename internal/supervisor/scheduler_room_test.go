package supervisor

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// roomRow is the queued task a reading finds waiting, which names its harness.
const roomRow = "- **next-task** - Ship it (repo: code-goblins, harness: claude)"

// schedulingLine is what the meter says the scheduler made of its latest
// reading.
func schedulingLine(service *Service) string {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.scheduling == nil {
		return ""
	}
	return service.scheduling.Text
}

// roomBoard is a board with a claude start measured at claudeCost gigabytes,
// work waiting as pause says, and free memory of free gigabytes: a queued
// task, a goblin paused for memory, or both.
func roomBoard(t *testing.T, free float64, claudeCost float64, pause string) (*Service, *spawnRecorder, time.Time) {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, gb(free), spawner)
	if claudeCost > 0 {
		if err := writeStartCost(h.State, "claude", StartCost{Bytes: gb(claudeCost), Task: "earlier"}); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	queueBriefedTask(t, h, roomRow, plainBrief)
	if pause == "memory" {
		pausedGoblin(t, h, "paused-task", "memory", "", now.Add(-time.Hour))
	}
	return handler.Service, spawner, now
}

// Under the floor nothing starts or resumes, whatever waits, and a goblin
// paused for memory stays paused: the floor is the fleet's own, from its
// settings, and a start under it is what the pause at the floor answers.
func TestTheSchedulerStartsAndResumesNothingWhileFreeMemoryIsUnderTheFloor(t *testing.T) {
	for _, test := range []struct {
		name  string
		pause string
	}{
		{name: "a queued task"},
		{name: "a goblin paused for memory", pause: "memory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, spawner, now := roomBoard(t, 3.9, 0, test.pause)

			// Act
			err := service.checkFleet(t.Context(), now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			service.starts.Lock()
			isChanging := service.starting != "" || len(service.changing) > 0
			service.starts.Unlock()
			if calls := spawner.recorded(); len(calls) != 0 || isChanging {
				t.Fatalf("dispatches %v (changing %t) under the floor, want none", calls, isChanging)
			}
		})
	}
}

// A start is admitted by what it costs: free memory less the start's measured
// cost must stay at or above the floor. With a floor of 4 GB and a claude
// start measured at 2.5 GB, 6 GB free refuses and 6.6 GB admits, for a queued
// task and for a goblin paused for memory alike, at one reading.
func TestTheSchedulerAdmitsAStartOnlyWhenFreeMemoryLessItsCostStaysAtTheFloor(t *testing.T) {
	for _, test := range []struct {
		name     string
		pause    string
		free     float64
		dispatch []string
	}{
		{name: "a queued task with too little free", free: 6},
		{name: "a queued task with enough free", free: 6.6, dispatch: []string{"spawn", "next-task"}},
		{name: "a goblin paused for memory with too little free", pause: "memory", free: 6},
		{name: "a goblin paused for memory with enough free", pause: "memory", free: 6.6, dispatch: []string{"resume", "paused-task"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, spawner, now := roomBoard(t, test.free, 2.5, test.pause)

			// Act
			err := service.checkFleet(t.Context(), now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if test.dispatch != nil {
				if calls := awaitDispatch(t, service, spawner, 1); !slices.Equal(calls[0][:2], test.dispatch) {
					t.Fatalf("dispatches=%v, want %v first", calls, test.dispatch)
				}
				return
			}
			line := schedulingLine(service)
			if calls := spawner.recorded(); len(calls) != 0 || !strings.Contains(line, "a claude start needs 6.5 GB") {
				t.Fatalf("dispatches %v and line %q, want none and the 6.5 GB a claude start needs", calls, line)
			}
			if test.pause == "memory" && !strings.HasPrefix(line, "paused-task resumes first: ") {
				t.Fatalf("line %q, want the paused goblin to come back first", line)
			}
		})
	}
}

// The floor is the home's own: a home that sets 6 GB keeps 6 GB, so 6.9 GB
// free refuses the start of a harness not yet measured, which costs 1 GB.
func TestTheSchedulerKeepsTheFloorTheHomesSettingsSet(t *testing.T) {
	for _, test := range []struct {
		name     string
		free     float64
		dispatch []string
	}{
		{name: "just under", free: 6.9},
		{name: "enough", free: 7.1, dispatch: []string{"spawn", "next-task"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, gb(test.free), spawner)
			writeFile(t, h.Root+"/config/fleet.json", `{"memory_floor_gb":6}`)
			queueBriefedTask(t, h, roomRow, plainBrief)

			// Act
			err := handler.Service.checkFleet(t.Context(), time.Now().UTC())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if test.dispatch != nil {
				if calls := awaitDispatch(t, handler.Service, spawner, 1); !slices.Equal(calls[0][:2], test.dispatch) {
					t.Fatalf("dispatches=%v, want %v first", calls, test.dispatch)
				}
				return
			}
			if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("dispatches %v, want none", calls)
			}
		})
	}
}

// A settings file the scheduler cannot read leaves nothing admitted: no
// default stands in for the floor, and the reading says why.
func TestTheSchedulerStartsNothingWhenTheMemoryFloorCannotBeRead(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, gb(40), spawner)
	writeFile(t, h.Root+"/config/fleet.json", `{"memory_floor_gb":0}`)
	queueBriefedTask(t, h, roomRow, plainBrief)

	// Act
	err := handler.Service.checkFleet(t.Context(), time.Now().UTC())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "memory floor cannot be read") {
		t.Fatalf("the reading met %v, want the unreadable floor reported", err)
	}
	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("dispatches %v, want none", calls)
	}
}

// On 2026-11-18 a goblin paused for memory resumed into the room a services
// start was waiting for (#2695). A services start that waits for memory holds
// the cost it was admitted with against every resume: with 8 GB free and 3.8
// GB held, the paused goblin needs 4 + 3.8 + 1 GB and stays paused, and once
// the reservation is gone it resumes at one reading.
func TestAServicesStartWaitingForMemoryKeepsAMemoryPausedGoblinPaused(t *testing.T) {
	// Arrange
	own, isKnown := proc.StartTime(os.Getpid())
	if !isKnown {
		t.Skip("this system cannot read a process's start")
	}
	service, spawner, now := roomBoard(t, 8, 0, "memory")
	h := service.Store.Home
	if err := services.WriteRecord(h.State, services.Record{Admitting: map[string]services.Admitting{"Example": {Task: "task-a", Cost: gb(3.8), PID: os.Getpid(), Started: own, Since: now}}}); err != nil {
		t.Fatal(err)
	}
	room, err := ReadRoom(h, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	memory := Memory{Available: gb(8), CommitAvailable: gb(8)}
	isRoomFree := room.Shortfall(memory, "") == ""
	paused, err := state.ReadLifecycle(h.State, "paused-task")
	if err != nil {
		t.Fatal(err)
	}
	condition := *paused.Pause

	// Act
	isClearedHeld, clearedErr := service.pauseCleared(t.Context(), condition, now, &fleetWakes{}, isRoomFree)
	heldErr := service.checkFleet(t.Context(), now)
	heldCalls := spawner.recorded()
	heldLine := schedulingLine(service)
	if err := services.WriteRecord(h.State, services.Record{}); err != nil {
		t.Fatal(err)
	}
	releasedErr := service.checkFleet(t.Context(), now.Add(time.Minute))

	// Assert
	if clearedErr != nil || heldErr != nil || releasedErr != nil {
		t.Fatalf("errors %v, %v and %v", clearedErr, heldErr, releasedErr)
	}
	if isClearedHeld || len(heldCalls) != 0 || !strings.HasPrefix(heldLine, "paused-task resumes first: ") || !strings.Contains(heldLine, "services starting") {
		t.Fatalf("while the services start waits, pause cleared %t, dispatches %v and line %q, want it held with the 3.8 GB named", isClearedHeld, heldCalls, heldLine)
	}
	if calls := awaitDispatch(t, service, spawner, 1); !slices.Equal(calls[0][:2], []string{"resume", "paused-task"}) {
		t.Fatalf("dispatches=%v after the reservation ended, want the goblin resumed", calls)
	}
}
