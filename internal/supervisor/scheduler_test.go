package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if want := "nothing starts: Only 4.9 GB of memory is free, and a claude start needs 5.0 GB: floor 4.0, start 1.0"; line != want || len(calls) != 0 {
		t.Fatalf("line %q and dispatches %v, want %q and none", line, calls, want)
	}
	if nextErr != nil {
		t.Fatal(nextErr)
	}
	if calls := awaitDispatch(t, service, spawner, 1); len(calls) != 1 || calls[0][0] != "spawn" || calls[0][1] != "next-task" {
		t.Fatalf("dispatches=%v, want next-task spawned at the next reading", calls)
	}
}

// heavyLine keeps the machine's heavy line in the test's own store, a line of
// capacity slots, and when waiter is not zero has a run that expects to use
// that much wait in it until the test ends. On a line of two slots the 2 GiB
// desktop reserve makes the next run's floor its estimate and 2 GiB: 4.5 GiB
// for a run of the default estimate when none waits.
func heavyLine(t *testing.T, capacity int, waiter uint64) {
	t.Helper()
	t.Setenv("GOBLINS_VERIFY_DIR", t.TempDir())
	store, err := verify.AdmissionDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "capacity.json"), []byte(fmt.Sprintf(`{"capacity":%d}`, capacity)), 0o600); err != nil {
		t.Fatal(err)
	}
	if waiter == 0 {
		return
	}
	said := make(chan struct{}, 1)
	run := verify.Admission{Dir: store, Available: func() (uint64, error) { return gigabyte, nil }, Who: "goblins gate test of a goblin closer to done", Estimate: waiter, Limit: verify.NoLimit, Poll: 10 * time.Millisecond, Waiting: func(time.Duration, string) {
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
		_, _ = run.Wait(ctx)
	}()
	select {
	case <-said:
	case <-time.After(time.Minute):
		t.Fatal("the run never joined the heavy line")
	}
}

// On 2026-11-02 the scheduler started cg-board-goblin-states at 19:38Z with
// six heavy steps of live goblins waiting in the heavy line for memory, and
// the memory floor paused it at 19:47Z (#1926). The Overlord then set the
// rule: a goblin starts or resumes by itself only while free memory, less the
// about 1 GB its session takes, still meets the floor the heavy line keeps
// for its next test run. Over the old 5 GB mark but short of that, nothing
// starts or resumes, a production defect included, and the scheduler's line
// says why; with the floor and a session free, and on a line of one slot
// whose floor is the fleet's own, everything starts as before.
func TestTheSchedulerStartsOnlyWithTheHeavyLinesNextFloorAndASessionFree(t *testing.T) {
	const task, defect = "- **next-task** - Ship it", "- **next-task** - Repair production (priority: production-defect)"
	const short = "nothing starts, keeping the test slots open: 5.2 GB of memory and commit is free; the next test run needs 4.5 GB and a new goblin 1 GB"
	const behind = "nothing starts, keeping the test slots open: 6.0 GB of memory and commit is free; the next test run needs 8.0 GB and a new goblin 1 GB"
	for _, test := range []struct {
		name     string
		row      string
		pause    string
		capacity int
		waiter   uint64
		free     uint64
		dispatch []string
		line     string
	}{
		{name: "a queued task over the old mark short of the floor and a session", row: task, capacity: 2, free: 5*gigabyte + gigabyte/4, line: short},
		{name: "a queued task with the floor and a session free", row: task, capacity: 2, free: 5*gigabyte + gigabyte/2, dispatch: []string{"spawn", "next-task"}},
		{name: "a queued task behind a run held for memory", row: task, capacity: 2, waiter: 6 * gigabyte, free: 6 * gigabyte, line: behind},
		{name: "a queued task at the old mark on a line of one slot", row: task, capacity: 1, free: 5 * gigabyte, dispatch: []string{"spawn", "next-task"}},
		{name: "a production defect behind a run held for memory", row: defect, capacity: 2, waiter: 6 * gigabyte, free: 6 * gigabyte, line: behind},
		{name: "a goblin paused for memory behind a run held for memory", row: task, pause: "memory", capacity: 2, waiter: 6 * gigabyte, free: 6 * gigabyte, line: behind},
		{name: "a goblin paused for memory with the floor and a session free", row: task, pause: "memory", capacity: 2, free: 6 * gigabyte, dispatch: []string{"resume", "paused-task"}},
		{name: "a goblin whose date passed behind a run held for memory", row: task, pause: "dependency", capacity: 2, waiter: 6 * gigabyte, free: 6 * gigabyte, line: behind},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, test.free, spawner)
			service := handler.Service
			service.Options.Dispatch.HeavyLine = MachineHeavyLine
			heavyLine(t, test.capacity, test.waiter)
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
			if calls := spawner.recorded(); len(calls) != 0 || isChanging || line != test.line {
				t.Fatalf("dispatches %v (changing %t) and line %q, want none and %q", calls, isChanging, line, test.line)
			}
		})
	}
}

// While a new goblin would leave the heavy line short of its next floor,
// memory_ready does not offer the memory to the queue, and wakes once it
// would not.
func TestMemoryReadyWaitsWhileANewGoblinWouldTakeTheTestSlotsMemory(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	meter := &memoryReadings{}
	hold := "6.0 GB of memory and commit is free; the next test run needs 8.0 GB and a new goblin 1 GB"
	s.Options.Dispatch = &Dispatch{Memory: meter.read, HeavyLine: func(Memory) (LineRoom, error) { return LineRoom{Hold: hold}, nil }}
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	now := time.Date(2026, 11, 2, 19, 38, 0, 0, time.UTC)
	over := [2]float64{6, 6}

	// Act
	wokeHeld := readMemory(t, s, h, meter, &now, over, over, over)
	hold = ""
	wokeClear := readMemory(t, s, h, meter, &now, over)

	// Assert
	if wokeHeld != 0 || wokeClear != 1 {
		t.Fatalf("memory_ready woke %d times while a new goblin would take the test slots' memory and %d once it would not, want none and then one", wokeHeld, wokeClear)
	}
}

// memory_ready names the floor it kept for the heavy line's next test run and
// how many runs wait there, which is the rule it woke by, so it never reads as
// a start at the 5 GB mark while the line refuses one (#2369).
func TestMemoryReadyNamesTheFloorItKeptAndTheRunsWaiting(t *testing.T) {
	cases := []struct {
		name   string
		waiter uint64
		kept   string
	}{
		{"one run waiting", 6 * gigabyte, "keep the next test run's 8.0 GB floor and 1 GB for a new goblin, with 1 run waiting in the test line"},
		{"no run waiting", 0, "keep the next test run's 4.5 GB floor and 1 GB for a new goblin, with 0 runs waiting in the test line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			s, h := fleetService(t)
			meter := &memoryReadings{}
			s.Options.Dispatch = &Dispatch{Memory: meter.read, HeavyLine: MachineHeavyLine}
			heavyLine(t, 2, tc.waiter)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			now := time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC)
			over := [2]float64{9.5, 10}

			// Act
			woke := readMemory(t, s, h, meter, &now, over, over)

			// Assert
			if woke != 1 {
				t.Fatalf("memory_ready woke %d times, want one", woke)
			}
			detail := fleetWakeRecords(t, h, "memory")[0].Detail
			for _, want := range []string{"9.5 GB of memory and 10.0 GB of commit are free", tc.kept, "next: dispatch the next queued task, next-task"} {
				if !strings.Contains(detail, want) {
					t.Errorf("memory wake %q lacks %q", detail, want)
				}
			}
		})
	}
}

// lostNetwork is what goblins spawn printed last when the scheduler's start
// of a queued task failed at its fetch on 2026-11-25, as DNS failed.
const lostNetwork = "spawn: acquire task worktree: worktree: fetch origin: worktree: git fetch --quiet origin exited with code 128: fatal: unable to access 'https://github.com/example/project.git/': Could not resolve host: github.com"

// failingSpawn answers the first failures spawns with output and an error, and
// every later one as started, recording each.
func failingSpawn(spawner *spawnRecorder, output string, failures int) func(context.Context, []string) (string, error) {
	return func(ctx context.Context, args []string) (string, error) {
		_, _ = spawner.spawn(ctx, args)
		if len(spawner.recorded()) <= failures {
			return output, errors.New("exit status 1")
		}
		return "", nil
	}
}

// On 2026-11-25 the scheduler's start of a queued task failed on a DNS fault
// at its fetch, and the task then waited for a Start by hand though the
// network came back minutes later (#2905). A start that failed because the
// network could not be reached is tried again by the scheduler once its wait
// has passed, and the board says when; any other failure still waits for a
// Start.
func TestAStartThatLostTheNetworkIsTriedAgainByItself(t *testing.T) {
	cases := []struct {
		name       string
		output     string
		wantSpawns int
		wantError  string
	}{
		{name: "the network could not be reached", output: lostNetwork, wantSpawns: 2, wantError: lostNetwork + "; the network could not be reached, so it tries again by itself at "},
		{name: "the start was refused", output: "refused: the auth preflight is red", wantSpawns: 1, wantError: "refused: the auth preflight is red"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
			handler.Service.Options.Dispatch.Spawn = failingSpawn(spawner, testCase.output, 1)
			now := time.Now().UTC()
			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			awaitDispatch(t, handler.Service, spawner, 1)
			snapshot, err := handler.Service.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			failed := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == "next-task" })

			// Act: a reading after the first wait has passed.
			if err := handler.Service.checkFleet(t.Context(), now.Add(2*time.Minute)); err != nil {
				t.Fatal(err)
			}
			awaitDispatch(t, handler.Service, spawner, testCase.wantSpawns)

			// Assert
			if failed < 0 || !strings.HasPrefix(snapshot.Tasks[failed].StartError, testCase.wantError) {
				t.Fatalf("board's start error = %+v, want it to start with %q", snapshot.Tasks, testCase.wantError)
			}
			if calls := spawner.recorded(); len(calls) != testCase.wantSpawns {
				t.Fatalf("spawns = %d, want %d", len(calls), testCase.wantSpawns)
			}
		})
	}
}

// A network that stays away is not tried for ever: the waits double from a
// minute up to half an hour, each holding the next try until it has passed,
// and after networkStartTries failed tries the task waits for a Start, as any
// other failed start does.
func TestAStartThatKeepsLosingTheNetworkStopsTryingAfterItsTries(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	handler.Service.Options.Dispatch.Spawn = failingSpawn(spawner, lostNetwork, 1000)
	waits := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	read := func(now time.Time, spawns int) {
		t.Helper()
		if err := handler.Service.checkFleet(t.Context(), now); err != nil {
			t.Fatal(err)
		}
		awaitDispatch(t, handler.Service, spawner, spawns)
	}
	read(time.Now().UTC(), 1)

	// Act and Assert: a reading just before each wait has passed tries
	// nothing, and one just after it tries again.
	for try, wait := range waits {
		failedAt := time.Now().UTC()
		read(failedAt.Add(wait-30*time.Second), 0)
		if calls := spawner.recorded(); len(calls) != try+1 {
			t.Fatalf("spawns %v into a %v wait = %d, want %d", wait-30*time.Second, wait, len(calls), try+1)
		}
		read(failedAt.Add(wait+30*time.Second), try+2)
	}
	read(time.Now().UTC().Add(time.Hour), 0)

	// Assert
	if calls := spawner.recorded(); len(calls) != networkStartTries {
		t.Fatalf("spawns = %d, want %d", len(calls), networkStartTries)
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Scheduling == nil || len(snapshot.Scheduling.Waiting) != 1 || snapshot.Scheduling.Waiting[0].Why != "its last start failed: "+lostNetwork {
		t.Fatalf("scheduling = %+v, want the task waiting for a Start on its last failure", snapshot.Scheduling)
	}
}

// An Overlord Start that fails for another reason while a start the network
// failed waits to be tried again ends that wait: the task waits for a Start on
// its new failure, and the scheduler does not try it by itself.
func TestAStartThatFailsOtherwiseWhileItWaitsOnTheNetworkIsNotTriedAgain(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	queueBriefedTask(t, h, "- **next-task** - Ship it (repo: code-goblins)", plainBrief)
	handler.Service.Options.Dispatch.Spawn = failingSpawn(spawner, lostNetwork, 1)
	now := time.Now().UTC()
	if err := handler.Service.checkFleet(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, handler.Service, spawner, 1)
	disk := handler.Service.Options.Dispatch.Disk
	handler.Service.Options.Dispatch.Disk = func() (Disk, error) { return Disk{}, errors.New("the drive is gone") }
	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	refused := cardOf(t, handler, "next-task").StartError
	handler.Service.Options.Dispatch.Disk = disk

	// Act: a reading after the network failure's wait has passed.
	if err := handler.Service.checkFleet(t.Context(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	awaitDispatch(t, handler.Service, spawner, 0)

	// Assert
	const diskRefusal = "Free disk cannot be read, so nothing starts: the drive is gone"
	if response.Code != 202 || refused != diskRefusal {
		t.Fatalf("start = %d %s with start error %q, want it accepted and failing with %q", response.Code, response.Body, refused, diskRefusal)
	}
	if calls := spawner.recorded(); len(calls) != 1 {
		t.Fatalf("spawns = %d, want 1", len(calls))
	}
	snapshot, err := handler.Service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Scheduling == nil || len(snapshot.Scheduling.Waiting) != 1 || snapshot.Scheduling.Waiting[0].Why != "its last start failed: "+diskRefusal {
		t.Fatalf("scheduling = %+v, want the task waiting for a Start on the disk refusal", snapshot.Scheduling)
	}
}
