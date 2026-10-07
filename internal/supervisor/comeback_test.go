package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The sign-in a restart ended began at lastSignIn; this one at thisSignIn.
var (
	lastSignIn = time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	thisSignIn = time.Date(2026, 10, 6, 22, 0, 0, 0, time.UTC)
)

// comebackRecorder stands in for bringing the CFO and goblins back: it
// records the order they come back in and answers each goblin with its
// outcome, CameBack unless told otherwise.
type comebackRecorder struct {
	mu       sync.Mutex
	order    []string
	outcomes map[string]GoblinComeback
	cfoErr   error
}

func (r *comebackRecorder) cfo(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, NativeCFOTerminal)
	return r.cfoErr
}

func (r *comebackRecorder) goblin(_ context.Context, id string) GoblinComeback {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, id)
	if outcome, ok := r.outcomes[id]; ok {
		return outcome
	}
	return GoblinComeback{Outcome: CameBack, Said: "back on its conversation"}
}

func (r *comebackRecorder) came() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.order)
}

// readingsMeter reads each of readings in turn, [available, commit] in GB,
// and the last one again once they run out.
type readingsMeter struct {
	mu       sync.Mutex
	readings [][2]float64
}

func (m *readingsMeter) read() (Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.readings[0]
	if len(m.readings) > 1 {
		m.readings = m.readings[1:]
	}
	return Memory{Available: uint64(next[0] * gigabyte), CommitAvailable: uint64(next[1] * gigabyte), Total: 32 * gigabyte, CommitLimit: 48 * gigabyte}, nil
}

// comebackBoard is a board whose last sign-in a restart ended, with a record
// of the comeback planned in it, so this sign-in's supervisor plans one.
func comebackBoard(t *testing.T, recorder *comebackRecorder, readings ...[2]float64) (*Service, home.Home, *spawnRecorder) {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoard(t, 8*gigabyte, spawner)
	service := handler.Service
	meter := &readingsMeter{readings: readings}
	service.Options.Dispatch.Memory = meter.read
	service.Options.Comeback = &Comeback{
		SignedIn: func() (time.Time, error) { return thisSignIn, nil },
		CFO:      recorder.cfo,
		Goblin:   recorder.goblin,
	}
	if err := state.WriteComeback(h.State, state.Comeback{SignedIn: lastSignIn, Planned: lastSignIn.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	return service, h, spawner
}

// terminalStarted records a native terminal for id whose host started at
// started and no longer answers.
func terminalStarted(t *testing.T, h home.Home, id string, started time.Time) {
	t.Helper()
	data, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-test-ended-` + id, Token: "ended", HostPID: 999999, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", id+".json"), string(data))
}

// workingGoblin is a native goblin whose terminal started at started.
func workingGoblin(t *testing.T, h home.Home, id string, started time.Time) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{ID: id, SpawnGen: "generation-" + id, Backend: "native", Harness: "claude", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-"+id), TaskTmp: filepath.Join(h.State, "tasktmp", id)}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	terminalStarted(t, h, id, started)
	return meta
}

// awaitComeback waits until no comeback step is under way.
func awaitComeback(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		service.starts.Lock()
		isBusy := service.isCFOComingBack || len(service.changing) > 0
		service.starts.Unlock()
		if !isBusy {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the comeback step did not finish")
}

// reading takes one memory reading at minute of this sign-in and waits for
// the comeback step it started.
func reading(t *testing.T, service *Service, minute int) {
	t.Helper()
	if err := service.checkFleet(t.Context(), thisSignIn.Add(time.Duration(minute)*time.Minute)); err != nil {
		t.Fatal(err)
	}
	awaitComeback(t, service)
}

// After a restart the CFO comes back first, then each goblin that was
// working, one at a time, a reading apart once memory has read at the
// next-start mark twice; a goblin that finished, was paused or stopped, ran
// in an older sign-in or started in this one stays as it is.
func TestARestartBringsTheCFOBackFirstThenEachWorkingGoblinOneAtATime(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{}
	service, h, spawner := comebackBoard(t, recorder, [2]float64{8, 8})
	terminalStarted(t, h, NativeCFOTerminal, lastSignIn.Add(time.Minute))
	for _, id := range []string{"alpha", "bravo", "charlie"} {
		workingGoblin(t, h, id, lastSignIn.Add(time.Hour))
	}
	finished := workingGoblin(t, h, "finished", lastSignIn.Add(time.Hour))
	service.Store.mu.Lock()
	service.Store.db.Tasks[finished.ID] = Evaluation{Phase: "done", Generation: finished.SpawnGen, At: lastSignIn.Add(2 * time.Hour)}
	err := service.Store.save()
	service.Store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	pausedGoblin(t, h, "paused", "memory", "", lastSignIn.Add(time.Hour))
	terminalStarted(t, h, "paused", lastSignIn.Add(time.Hour))
	stopped := workingGoblin(t, h, "stopped", lastSignIn.Add(time.Hour))
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: stopped.ID, Generation: stopped.SpawnGen, Operation: "stop-1", Action: "stop", Phase: "stopped", Started: lastSignIn, Updated: lastSignIn}); err != nil {
		t.Fatal(err)
	}
	workingGoblin(t, h, "older", lastSignIn.Add(-time.Hour))
	workingGoblin(t, h, "this-sign-in", thisSignIn.Add(time.Minute))

	// Act
	reading(t, service, 0)
	afterFirst := recorder.came()
	for minute := 1; minute <= 4; minute++ {
		reading(t, service, minute)
	}

	// Assert
	if !slices.Equal(afterFirst, []string{NativeCFOTerminal}) {
		t.Errorf("the first reading brought back %v, want the CFO alone", afterFirst)
	}
	if came := recorder.came(); !slices.Equal(came, []string{NativeCFOTerminal, "alpha", "bravo", "charlie"}) {
		t.Errorf("came back in order %v, want the CFO, then alpha, bravo and charlie", came)
	}
	record, err := state.ReadComeback(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !record.SignedIn.Equal(thisSignIn) || record.CFO == nil || record.CFO.State != state.ComebackBack || len(record.Goblins) != 3 {
		t.Fatalf("the comeback record is %+v", record)
	}
	for _, entry := range record.Goblins {
		if entry.State != state.ComebackBack {
			t.Errorf("%s is %s, want back", entry.ID, entry.State)
		}
	}
	// The paused goblin's own pause resumes it only once nothing waits to
	// come back.
	if calls := spawner.recorded(); len(calls) != 1 || calls[0][0] != "resume" || calls[0][1] != "paused" {
		t.Errorf("dispatches %v, want the paused goblin resumed after the comeback", calls)
	}
}

// Memory gates the comeback as it gates a start: the CFO comes back at the
// first reading at or above the 4 GB floor, and a goblin only after memory
// and commit both read at the 5 GB next-start mark twice in a row.
func TestTheComebackWaitsForMemoryAndCommitAtTheMarks(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{}
	service, h, _ := comebackBoard(t, recorder,
		[2]float64{3.5, 8}, // under the floor: nothing
		[2]float64{4.5, 8}, // over the floor: the CFO
		[2]float64{8, 8},   // first reading at the mark
		[2]float64{8, 4.5}, // commit under the mark: the count starts again
		[2]float64{8, 8},   // first again
		[2]float64{8, 8},   // second: alpha
	)
	terminalStarted(t, h, NativeCFOTerminal, lastSignIn.Add(time.Minute))
	workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
	var came [][]string

	// Act
	for minute := range 6 {
		reading(t, service, minute)
		came = append(came, recorder.came())
	}

	// Assert
	want := [][]string{{}, {"cfo"}, {"cfo"}, {"cfo"}, {"cfo"}, {"cfo", "alpha"}}
	for index := range want {
		if !slices.Equal(came[index], want[index]) && !(len(came[index]) == 0 && len(want[index]) == 0) {
			t.Errorf("after reading %d came back %v, want %v", index+1, came[index], want[index])
		}
	}
}

// A goblin that cannot come back stays stopped with the reason on its card
// and wakes the CFO, and the goblins after it still come back.
func TestAGoblinThatCannotComeBackStaysStoppedWithItsReasonAndBlocksNoOther(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{outcomes: map[string]GoblinComeback{
		"alpha": {Outcome: DidNotComeBack, Said: "Codex is not signed in"},
	}}
	service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
	alpha := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
	workingGoblin(t, h, "bravo", lastSignIn.Add(time.Hour))

	// Act
	for minute := range 3 {
		reading(t, service, minute)
	}

	// Assert
	if came := recorder.came(); !slices.Equal(came, []string{"alpha", "bravo"}) {
		t.Fatalf("came back %v, want alpha tried and bravo back", came)
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	cards := map[string]Task{}
	for _, task := range snapshot.Tasks {
		cards[task.ID] = task
	}
	card := cards["alpha"]
	if card.Comeback == nil || card.Comeback.State != state.ComebackStopped {
		t.Fatalf("alpha's card shows comeback %+v, want stopped", card.Comeback)
	}
	if card.ActionError != "Did not come back after the restart: Codex is not signed in. The CFO was told." {
		t.Errorf("alpha's card says %q", card.ActionError)
	}
	if cards["bravo"].Comeback != nil || cards["bravo"].ActionError != "" {
		t.Errorf("bravo, which came back, still shows %+v %q", cards["bravo"].Comeback, cards["bravo"].ActionError)
	}
	if snapshot.Comeback == nil || len(snapshot.Comeback.Goblins) != 2 {
		t.Fatalf("the board's comeback is %+v", snapshot.Comeback)
	}
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	woke := slices.IndexFunc(records, func(record wake.Record) bool {
		return record.Kind == "check" && record.Key == alpha.ID && strings.Contains(record.Detail, "Codex is not signed in") && strings.Contains(record.Detail, "cfo switch alpha --harness claude")
	})
	if woke < 0 {
		t.Errorf("no wake told the CFO alpha did not come back: %+v", records)
	}
}

// The CFO that cannot come back on its own conversation stays closed with
// the reason, and the goblins still come back.
func TestACFOThatCannotComeBackLeavesTheGoblinsComingBack(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{cfoErr: errors.New("its conversation s-1 could not be resumed; Reopen on its bar starts it on a new one")}
	service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
	terminalStarted(t, h, NativeCFOTerminal, lastSignIn.Add(time.Minute))
	workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))

	// Act
	for minute := range 2 {
		reading(t, service, minute)
	}

	// Assert
	if came := recorder.came(); !slices.Equal(came, []string{NativeCFOTerminal, "alpha"}) {
		t.Fatalf("came back %v", came)
	}
	record, err := state.ReadComeback(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if record.CFO == nil || record.CFO.State != state.ComebackStopped || !strings.Contains(record.CFO.Reason, "s-1 could not be resumed") {
		t.Errorf("the CFO's comeback is %+v", record.CFO)
	}
}

// A goblin there is no room for yet waits for its turn with the reason, and
// comes back once there is room.
func TestAGoblinWithNoRoomYetWaitsWithTheReason(t *testing.T) {
	// Arrange
	recorder := &comebackRecorder{outcomes: map[string]GoblinComeback{
		"alpha": {Outcome: WaitsForRoom, Said: "live goblin cap reached"},
	}}
	service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
	alpha := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))

	// Act
	for minute := range 2 {
		reading(t, service, minute)
	}
	snapshot, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	delete(recorder.outcomes, "alpha")
	reading(t, service, 2)

	// Assert
	card := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == alpha.ID })
	if card < 0 || snapshot.Tasks[card].Comeback == nil || snapshot.Tasks[card].Comeback.State != state.ComebackWaiting || snapshot.Tasks[card].Comeback.Reason != "Waits for room: live goblin cap reached" {
		t.Fatalf("alpha's card while it waits: %+v", snapshot.Tasks)
	}
	record, err := state.ReadComeback(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if record.Goblins[0].State != state.ComebackBack {
		t.Errorf("alpha is %s after room came, want back", record.Goblins[0].State)
	}
}

// A home with no comeback record has no sign-in before to read, so its first
// supervisor brings nothing back and only marks this sign-in; a supervisor
// that starts again in the same sign-in carries on with what waits rather
// than planning again.
func TestTheFirstSupervisorBringsNothingBackAndALaterOneInTheSameSignInCarriesOn(t *testing.T) {
	t.Run("no record", func(t *testing.T) {
		// Arrange
		recorder := &comebackRecorder{}
		service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
		if err := os.Remove(state.ComebackPath(h.State)); err != nil {
			t.Fatal(err)
		}
		terminalStarted(t, h, NativeCFOTerminal, lastSignIn.Add(time.Minute))
		workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))

		// Act
		for minute := range 3 {
			reading(t, service, minute)
		}

		// Assert
		if came := recorder.came(); len(came) != 0 {
			t.Errorf("brought back %v with no record of the sign-in before", came)
		}
		record, err := state.ReadComeback(h.State)
		if err != nil || !record.SignedIn.Equal(thisSignIn) || record.CFO != nil || len(record.Goblins) != 0 {
			t.Errorf("the record is %+v, %v; want this sign-in with nothing to bring back", record, err)
		}
	})
	t.Run("same sign-in", func(t *testing.T) {
		// Arrange
		recorder := &comebackRecorder{}
		service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
		alpha := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
		bravo := workingGoblin(t, h, "bravo", lastSignIn.Add(time.Hour))
		if err := state.WriteComeback(h.State, state.Comeback{SignedIn: thisSignIn, Goblins: []state.ComebackEntry{
			{ID: alpha.ID, Generation: alpha.SpawnGen, State: state.ComebackBack},
			{ID: bravo.ID, Generation: bravo.SpawnGen, State: state.ComebackWaiting},
		}}); err != nil {
			t.Fatal(err)
		}

		// Act
		for minute := range 3 {
			reading(t, service, minute)
		}

		// Assert
		if came := recorder.came(); !slices.Equal(came, []string{"bravo"}) {
			t.Errorf("came back %v, want bravo alone", came)
		}
	})
}

func TestTheComebackRechecksWaitingGoblinsBeforeLaunching(t *testing.T) {
	for _, testCase := range []struct {
		name, evaluation, lifecycle   string
		isStale, isRetired, isRunning bool
		shouldLaunch                  bool
	}{
		{name: "done", evaluation: "done"},
		{name: "merged", evaluation: "merged"},
		{name: "paused", lifecycle: "paused"},
		{name: "stopped", lifecycle: "stopped"},
		{name: "pausing", lifecycle: "pausing"},
		{name: "resuming", lifecycle: "resuming"},
		{name: "stopping", lifecycle: "stopping"},
		{name: "failed", lifecycle: "failed"},
		{name: "retired", isRetired: true},
		{name: "terminal already answers", isRunning: true},
		{name: "old completion", evaluation: "done", isStale: true, shouldLaunch: true},
		{name: "old pause", lifecycle: "paused", isStale: true, shouldLaunch: true},
		{name: "still working", lifecycle: "running", shouldLaunch: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &comebackRecorder{}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			meta := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
			reading(t, service, 0)
			generation := meta.SpawnGen
			if testCase.isStale {
				generation = "previous-generation"
			}
			if testCase.evaluation != "" {
				service.Store.mu.Lock()
				service.Store.db.Tasks[meta.ID] = Evaluation{Phase: testCase.evaluation, Generation: generation, At: thisSignIn}
				err := service.Store.save()
				service.Store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			if testCase.lifecycle != "" {
				action := "resume"
				if testCase.lifecycle == "paused" || testCase.lifecycle == "pausing" {
					action = "pause"
				} else if testCase.lifecycle == "stopped" || testCase.lifecycle == "stopping" {
					action = "stop"
				}
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: generation, Operation: "changed", Action: action, Phase: testCase.lifecycle, Started: thisSignIn}); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isRetired {
				if err := os.Remove(state.TaskMetaPath(h.State, meta.ID)); err != nil {
					t.Fatal(err)
				}
			}
			if testCase.isRunning {
				if err := os.Remove(filepath.Join(h.State, "hosts", meta.ID+".json")); err != nil {
					t.Fatal(err)
				}
				program, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				typed := filepath.Join(h.Root, "typed.txt")
				hostProgram(t, h.State, meta.ID, typed, program, "-test.run=^TestNativeTerminalProgram$", "--", "native-terminal-program", typed, h.State)
			}
			if !testCase.shouldLaunch {
				service.Options.Dispatch.Disk = func() (Disk, error) { return Disk{}, errors.New("disk cannot be read") }
			}

			reading(t, service, 1)

			came := recorder.came()
			if testCase.shouldLaunch {
				if !slices.Equal(came, []string{meta.ID}) {
					t.Fatalf("launched %v, want alpha resumed", came)
				}
			} else if len(came) != 0 {
				t.Fatalf("launched %v after %s", came, testCase.name)
			}
			record, err := state.ReadComeback(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.shouldLaunch || testCase.isRunning {
				if len(record.Goblins) != 1 || record.Goblins[0].State != state.ComebackBack {
					t.Fatalf("comeback %+v, want alpha back", record.Goblins)
				}
			} else if len(record.Goblins) != 0 {
				t.Fatalf("comeback %+v, want alpha left as it was", record.Goblins)
			}
		})
	}
}

func TestTheComebackCarriesWaitingTaskIdentityAcrossGenerationPublication(t *testing.T) {
	for _, priorState := range []string{state.ComebackWaiting, state.ComebackBack, state.ComebackStopped} {
		t.Run(priorState, func(t *testing.T) {
			recorder := &comebackRecorder{}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			meta := workingGoblin(t, h, "alpha", lastSignIn.Add(-time.Hour))
			if err := state.WriteComeback(h.State, state.Comeback{SignedIn: lastSignIn, Goblins: []state.ComebackEntry{
				{ID: meta.ID, Generation: "before-switch", State: priorState},
			}}); err != nil {
				t.Fatal(err)
			}

			reading(t, service, 0)
			record, err := state.ReadComeback(h.State)
			if err != nil {
				t.Fatal(err)
			}
			reading(t, service, 1)

			if priorState == state.ComebackWaiting {
				if len(record.Goblins) != 1 || record.Goblins[0].Generation != meta.SpawnGen || !slices.Equal(recorder.came(), []string{meta.ID}) {
					t.Fatalf("planned %+v and resumed %v, want the waiting task's current generation", record.Goblins, recorder.came())
				}
			} else if len(record.Goblins) != 0 || len(recorder.came()) != 0 {
				t.Fatalf("planned %+v and resumed %v from an earlier %s entry", record.Goblins, recorder.came(), priorState)
			}
		})
	}
}

func TestTheComebackCountsOnlyTheCurrentSignInsMemoryReadings(t *testing.T) {
	for _, isSameSignIn := range []bool{false, true} {
		t.Run(fmt.Sprint(isSameSignIn), func(t *testing.T) {
			recorder := &comebackRecorder{}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			meta := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
			if isSameSignIn {
				if err := state.WriteComeback(h.State, state.Comeback{SignedIn: thisSignIn, Goblins: []state.ComebackEntry{
					{ID: meta.ID, Generation: meta.SpawnGen, State: state.ComebackWaiting},
				}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeFleetWakes(h.State, fleetWakes{MemoryAbove: 1, MemoryReadAt: thisSignIn.Add(-30 * time.Second)}); err != nil {
				t.Fatal(err)
			}

			reading(t, service, 0)
			first := recorder.came()
			reading(t, service, 1)

			if !isSameSignIn && len(first) != 0 || isSameSignIn && !slices.Equal(first, []string{meta.ID}) {
				t.Fatalf("first reading resumed %v, same sign-in %v", first, isSameSignIn)
			}
			if !slices.Equal(recorder.came(), []string{meta.ID}) {
				t.Fatalf("resumed %v, want alpha exactly once", recorder.came())
			}
		})
	}
}

func TestTheComebackKeepsTheReasonForAGoblinThatRunsAfterAnError(t *testing.T) {
	recorder := &comebackRecorder{outcomes: map[string]GoblinComeback{
		"alpha": {Outcome: CameBack, Said: "instruction confirmation timed out"},
	}}
	service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
	workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))

	reading(t, service, 0)
	reading(t, service, 1)

	record, err := state.ReadComeback(h.State)
	if err != nil || len(record.Goblins) != 1 || record.Goblins[0].State != state.ComebackBack || record.Goblins[0].Reason != "instruction confirmation timed out" {
		t.Fatalf("comeback %+v, error %v, want a running goblin with its error retained", record, err)
	}
	records, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(records, func(record wake.Record) bool { return record.Kind == "check" && record.Key == "alpha" }) {
		t.Fatalf("running goblin raised a failed-resume wake: %+v", records)
	}
}

func TestTheComebackDoesNotOverlapCompetingLaunchReservations(t *testing.T) {
	for _, kind := range []string{"start", "resume", "CFO"} {
		t.Run(kind, func(t *testing.T) {
			recorder := &comebackRecorder{outcomes: map[string]GoblinComeback{
				"alpha": {Outcome: WaitsForRoom, Said: "stand-in waits"},
			}}
			service, h, _ := comebackBoard(t, recorder, [2]float64{8, 8})
			alpha := workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
			if err := state.WriteComeback(h.State, state.Comeback{SignedIn: thisSignIn, Goblins: []state.ComebackEntry{
				{ID: alpha.ID, Generation: alpha.SpawnGen, State: state.ComebackWaiting},
			}}); err != nil {
				t.Fatal(err)
			}
			service.changing = map[string]string{}
			var admissions, overlaps atomic.Int32
			service.Options.Dispatch.Disk = func() (Disk, error) {
				admissions.Add(1)
				service.starts.Lock()
				if service.starting != "" || service.changing["manual"] != "" || service.isCFOComingBack {
					overlaps.Add(1)
				}
				service.starts.Unlock()
				return diskWithFree(200 * gigabyte), nil
			}
			stop := make(chan struct{})
			var workers sync.WaitGroup
			for range 3 {
				workers.Go(func() {
					for {
						select {
						case <-stop:
							return
						default:
						}
						service.starts.Lock()
						isReserved := service.starting == "" && len(service.changing) == 0 && !service.isCFOComingBack
						if isReserved {
							switch kind {
							case "start":
								service.starting = "manual"
							case "resume":
								service.changing["manual"] = "resume"
							case "CFO":
								service.isCFOComingBack = true
							}
						}
						service.starts.Unlock()
						if !isReserved {
							runtime.Gosched()
							continue
						}
						time.Sleep(5 * time.Millisecond)
						service.starts.Lock()
						service.starting = ""
						delete(service.changing, "manual")
						service.isCFOComingBack = false
						time.Sleep(2 * time.Millisecond)
						service.starts.Unlock()
					}
				})
			}
			finish := sync.OnceFunc(func() { close(stop); workers.Wait(); awaitComeback(t, service) })
			defer finish()
			memory := Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}
			wakes := fleetWakes{MemoryAbove: 2}

			for range 128 {
				isWaiting, err := service.comeBack(thisSignIn, memory, &wakes)
				if err != nil || !isWaiting {
					t.Fatalf("comeback waiting %v, error %v", isWaiting, err)
				}
				runtime.Gosched()
			}
			finish()
			if _, err := service.comeBack(thisSignIn, memory, &wakes); err != nil {
				t.Fatal(err)
			}
			awaitComeback(t, service)

			if overlaps.Load() != 0 || admissions.Load() == 0 {
				t.Fatalf("%d of %d comeback admissions overlapped a competing %s", overlaps.Load(), admissions.Load(), kind)
			}
			record, err := state.ReadComeback(h.State)
			if err != nil || len(record.Goblins) != 1 || record.Goblins[0].State != state.ComebackWaiting {
				t.Fatalf("comeback %+v, error %v, want the entry still waiting", record, err)
			}
		})
	}
}

func TestManualStartRemainsAllowedWhileTheComebackWaits(t *testing.T) {
	recorder := &comebackRecorder{}
	service, h, spawner := comebackBoard(t, recorder, [2]float64{8, 8})
	workingGoblin(t, h, "alpha", lastSignIn.Add(time.Hour))
	reading(t, service, 0)
	queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
	spawner.release = make(chan struct{})
	finish := sync.OnceFunc(func() { close(spawner.release) })
	defer finish()
	handler := NewHTTP(service, "board.local", nil)

	response := postStart(handler, `{"task":"next-task"}`, "board.local", "http://board.local", orderToken)
	if response.Code != 202 {
		t.Fatalf("manual Start = %d %s, want accepted while alpha waits", response.Code, response.Body)
	}
	isWaiting, err := service.comeBack(thisSignIn.Add(time.Minute), Memory{Available: 8 * gigabyte, CommitAvailable: 8 * gigabyte}, &fleetWakes{MemoryAbove: 2})
	finish()
	waitStarted(t, handler, "next-task")

	if err != nil || !isWaiting || len(recorder.came()) != 0 {
		t.Fatalf("comeback waiting %v, error %v, resumed %v during the manual Start", isWaiting, err, recorder.came())
	}
	record, err := state.ReadComeback(h.State)
	if err != nil || len(record.Goblins) != 1 || record.Goblins[0].State != state.ComebackWaiting {
		t.Fatalf("comeback %+v, error %v, want alpha waiting for the next reading", record, err)
	}
	reading(t, service, 2)
	if !slices.Equal(recorder.came(), []string{"alpha"}) {
		t.Fatalf("after the manual Start resumed %v, want alpha", recorder.came())
	}
}
