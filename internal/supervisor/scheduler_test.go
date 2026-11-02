package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/verify"
)

// On 2026-10-19 the supervisor filed issue #1203 for an error of its own it
// met at 08:24Z and again at 09:26Z: "Only 4.9 GB of memory is free; Start
// needs 5 GB to keep the 4 GB floor". The scheduler starts the next task at a
// reading of 5 GB or more, and the start reads memory again as it starts, so
// memory that fell short in between refuses it as passing. That is the start
// waiting for the next reading, which tries it again, not an error: the
// meter's line says why nothing started, and nobody is woken.
func TestAStartWhoseOwnMemoryReadingFallsShortWaitsForTheNextReadingAndIsNoError(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	service := handler.Service
	// Each reading reads memory once, then its start reads it once more.
	service.Options.Dispatch.Memory = (&memoryReadings{readings: [][2]float64{{8, 40}, {4.95, 40}, {8, 40}, {8, 40}}}).read
	now := time.Now().UTC()

	// Act
	err := service.checkFleet(t.Context(), now)
	service.mu.Lock()
	line := service.scheduling.Text
	service.mu.Unlock()
	calls := spawner.recorded()
	nextErr := service.checkFleet(t.Context(), now.Add(time.Minute))

	// Assert
	if err != nil {
		t.Fatalf("the reading met %q, want the start to wait for the next reading", err)
	}
	if want := "nothing starts: Only 4.9 GB of memory is free; Start needs 5 GB to keep the 4 GB floor"; line != want || len(calls) != 0 {
		t.Fatalf("line %q and dispatches %v, want %q and none", line, calls, want)
	}
	if nextErr != nil {
		t.Fatal(nextErr)
	}
	if calls := awaitDispatch(t, service, spawner, 1); len(calls) != 1 || calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned at the next reading", calls)
	}
}

// heavyLineWithAWaiter keeps the machine's heavy line in the test's own
// store, a line of two slots, and when isHeld has a run wait in it until the
// test ends: the run expects to use 6 GiB, so with the 2 GiB desktop reserve
// its floor is 8 GiB.
func heavyLineWithAWaiter(t *testing.T, isHeld bool) {
	t.Helper()
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(`{"capacity":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isHeld {
		return
	}
	said := make(chan struct{}, 1)
	waiter := verify.Admission{Dir: store, Available: func() (uint64, error) { return gigabyte, nil }, Who: "cfo gate test of a goblin closer to done", Estimate: 6 * gigabyte, Limit: verify.NoLimit, Poll: 10 * time.Millisecond, Waiting: func(time.Duration, string) {
		select {
		case said <- struct{}{}:
		default:
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	go func() {
		defer close(stopped)
		_, _ = waiter.Wait(ctx)
	}()
	select {
	case <-said:
	case <-time.After(time.Minute):
		t.Fatal("the run never joined the heavy line")
	}
}

// On 2026-11-02 the scheduler started cg-board-goblin-states at 19:38Z with
// six heavy steps of live goblins waiting in the heavy line for memory, and
// the memory floor paused it at 19:47Z (#1926). At a reading over the
// next-start mark, what memory would start or bring back waits while a run
// waits in the line for memory, a reported production defect included, and
// the scheduler's line says why; a goblin whose pause cleared for anything
// else still resumes, and with the line clear everything starts as before.
func TestTheSchedulerWaitsWhileARunWaitsInTheHeavyLineForMemory(t *testing.T) {
	const task, defect = "- **next-task** - Ship it", "- **next-task** - Repair production (priority: production-defect)"
	for _, test := range []struct {
		name     string
		row      string
		pause    string
		isHeld   bool
		dispatch []string
	}{
		{name: "a queued task behind a run held for memory", row: task, isHeld: true},
		{name: "a queued task beside an empty line", row: task, dispatch: []string{"spawn", "next-task"}},
		{name: "a production defect behind a run held for memory", row: defect, isHeld: true},
		{name: "a goblin paused for memory behind a run held for memory", row: task, pause: "memory", isHeld: true},
		{name: "a goblin paused for memory beside an empty line", row: task, pause: "memory", dispatch: []string{"resume", "paused-task"}},
		{name: "a goblin whose date passed behind a run held for memory", row: task, pause: "dependency", isHeld: true, dispatch: []string{"resume", "paused-task"}},
		{name: "a goblin whose date passed beside a production defect held for memory", row: defect, pause: "dependency", isHeld: true, dispatch: []string{"resume", "paused-task"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 6*gigabyte, spawner)
			service := handler.Service
			service.Options.Dispatch.HeavyLine = MachineHeavyLine
			heavyLineWithAWaiter(t, test.isHeld)
			now := time.Now().UTC()
			queueBriefedTask(t, h, test.row, plainBrief)
			switch test.pause {
			case "memory":
				pausedGoblin(t, h, "paused-task", "memory", "", now.Add(-time.Hour))
				if err := writeFleetWakes(h.State, fleetWakes{Schema: fleetWakesSchema, MemoryAbove: 1, MemoryReadAt: now.Add(-time.Minute)}); err != nil {
					t.Fatal(err)
				}
			case "dependency":
				pausedGoblin(t, h, "paused-task", "dependency", "date:"+now.Add(-time.Minute).Format(time.RFC3339), now.Add(-time.Hour))
			}

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
			service.starts.Lock()
			isChanging := service.starting != "" || len(service.changing) > 0
			service.starts.Unlock()
			service.mu.Lock()
			line := service.scheduling.Text
			service.mu.Unlock()
			want := "nothing starts: 1 run waits in the machine's heavy line for the 8.0 GB of memory and commit its next run needs"
			if calls := spawner.recorded(); len(calls) != 0 || isChanging || line != want {
				t.Fatalf("dispatches %v (changing %t) and line %q, want none and %q", calls, isChanging, line, want)
			}
		})
	}
}

// While runs wait in the heavy line for memory, the memory is theirs, so
// memory_ready does not offer it to the queue, and wakes once they have it.
func TestMemoryReadyWaitsWhileARunWaitsInTheHeavyLineForMemory(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	meter := &memoryReadings{}
	hold := "1 run waits in the machine's heavy line for the 8.0 GB of memory and commit its next run needs"
	s.Options.Dispatch = &Dispatch{Memory: meter.read, HeavyLine: func(Memory) (string, error) { return hold, nil }}
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	now := time.Date(2026, 11, 2, 19, 38, 0, 0, time.UTC)
	over := [2]float64{6, 6}

	// Act
	wokeHeld := readMemory(t, s, h, meter, &now, over, over, over)
	hold = ""
	wokeClear := readMemory(t, s, h, meter, &now, over)

	// Assert
	if wokeHeld != 0 || wokeClear != 1 {
		t.Fatalf("memory_ready woke %d times while a run waited in the heavy line and %d once it had its memory, want none and then one", wokeHeld, wokeClear)
	}
}
