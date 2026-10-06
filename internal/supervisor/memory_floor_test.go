package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// hostedGoblin is a native goblin whose terminal host runs, started at started:
// the test's own process stands in for its host, so started is after it.
func hostedGoblin(t *testing.T, h home.Home, id string, started time.Time) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{ID: id, SpawnGen: "generation-" + id, Backend: "native", Harness: "claude", Project: h.Root, Worktree: filepath.Join(h.Root, ".worktrees", "gb-"+id), TaskTmp: filepath.Join(h.State, "tasktmp", id)}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(host.Record{ID: id, HostPID: os.Getpid(), Started: started, Pipe: "fixture", Token: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", id+".json"), string(data))
	return meta
}

// floorBoard is a board in AFK mode, or not, with an older and a newer live
// goblin, whose memory meter reads readings in turn.
func floorBoard(t *testing.T, isAFKOn bool, spawner *spawnRecorder, readings ...[2]float64) (*Service, home.Home, state.TaskMeta, state.TaskMeta, time.Time) {
	t.Helper()
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC().Truncate(time.Second)
	older := hostedGoblin(t, h, "older-task", now.Add(time.Second))
	newer := hostedGoblin(t, h, "newer-task", now.Add(2*time.Second))
	handler.Service.Options.Dispatch.Memory = (&memoryReadings{readings: readings}).read
	if isAFKOn {
		if _, _, err := afk.TurnOn(h.State, "his own board (goblins-window.exe pid 4242)", nil, now); err != nil {
			t.Fatal(err)
		}
	}
	return handler.Service, h, older, newer, now
}

// readFleet takes one fleet reading for each minute after now.
func readFleet(t *testing.T, s *Service, now time.Time, minutes ...int) {
	t.Helper()
	for _, minute := range minutes {
		if err := s.checkFleet(t.Context(), now.Add(time.Duration(minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
}

// pausedFor writes the lifecycle record a finished pause of meta leaves.
func pausedFor(t *testing.T, h home.Home, meta state.TaskMeta, reason, phase string, at time.Time) {
	t.Helper()
	condition, err := state.NewPauseCondition(reason, "", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: reason + "-pause-" + meta.ID, Action: "pause", Phase: phase, Started: at, Updated: at, Pause: &condition, Reason: reason}); err != nil {
		t.Fatal(err)
	}
}

func TestAFKModePausesTheNewestGoblinAtTheMemoryFloor(t *testing.T) {
	for name, low := range map[string][2]float64{"memory under the floor": {3.9, 8}, "commit under the floor": {8, 3.9}} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, _, _, newer, now := floorBoard(t, true, spawner, low, low)

			// Act
			readFleet(t, s, now, 0)
			afterOne := spawner.recorded()
			readFleet(t, s, now, 1)

			// Assert
			if len(afterOne) != 0 {
				t.Fatalf("paused on one reading under the floor: %v", afterOne)
			}
			calls := awaitDispatch(t, s, spawner, 1)
			args := strings.Join(calls[0], " ")
			if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != newer.ID || !strings.Contains(args, "--generation "+newer.SpawnGen) || !strings.HasSuffix(args, "--reason memory") {
				t.Fatalf("dispatches = %v, want one pause of the newest goblin for memory, with no --until", calls)
			}
		})
	}
}

// The floor is a safety rail of AFK mode: while he is here the CFO and he
// decide what to pause, so nothing pauses by itself.
func TestNothingPausesAtTheMemoryFloorWhileAFKModeIsOff(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, _, _, _, now := floorBoard(t, false, spawner, [2]float64{3, 3}, [2]float64{3, 3}, [2]float64{3, 3})

	// Act
	readFleet(t, s, now, 0, 1, 2)

	// Assert
	if calls := spawner.recorded(); len(calls) != 0 {
		t.Fatalf("dispatches = %v, want none while AFK mode is off", calls)
	}
}

// One goblin is paused at a time: while a pause or any other change is under
// way nothing more is paused, and once it is done two fresh readings under
// the floor are needed before the next newest is paused, so a pause has the
// time to free what it frees.
func TestTheMemoryFloorPausesOneGoblinAtATime(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	low := [2]float64{3, 8}
	s, h, older, newer, now := floorBoard(t, true, spawner, low, low, low, low, low, low)

	// Act
	readFleet(t, s, now, 0, 1)
	inFlight := spawner.recorded()
	readFleet(t, s, now, 2, 3)
	whileInFlight := spawner.recorded()
	close(spawner.release)
	awaitDispatch(t, s, spawner, 1)
	pausedFor(t, h, newer, "memory", "paused", now.Add(3*time.Minute))
	readFleet(t, s, now, 4)
	afterOneMore := spawner.recorded()
	readFleet(t, s, now, 5)

	// Assert
	if len(inFlight) != 1 || inFlight[0][1] != newer.ID {
		t.Fatalf("first dispatches = %v, want the newest goblin paused", inFlight)
	}
	if len(whileInFlight) != 1 {
		t.Fatalf("dispatches while the first pause ran = %v, want no second", whileInFlight)
	}
	if len(afterOneMore) != 1 {
		t.Fatalf("dispatches one reading after the pause = %v, want none yet", afterOneMore)
	}
	calls := awaitDispatch(t, s, spawner, 2)
	if len(calls) != 2 || calls[1][0] != "pause" || calls[1][1] != older.ID {
		t.Fatalf("dispatches = %v, want the older goblin paused next", calls)
	}
}

// A goblin whose last pause at the memory floor failed is not asked again
// at every reading; the next newest is paused instead.
func TestAGoblinWhoseMemoryPauseFailedIsNotPausedAgain(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	low := [2]float64{3, 8}
	s, h, older, newer, now := floorBoard(t, true, spawner, low, low)
	pausedFor(t, h, newer, "memory", "failed", now.Add(-time.Minute))

	// Act
	readFleet(t, s, now, 0, 1)

	// Assert
	if calls := awaitDispatch(t, s, spawner, 1); len(calls) != 1 || calls[0][1] != older.ID {
		t.Fatalf("dispatches = %v, want the older goblin paused, not the one whose pause failed", calls)
	}
}

// Every pause at the memory floor is logged in AFK mode's log with the
// readings it stood on and how it went, so the morning report lists it.
func TestAPauseAtTheMemoryFloorIsLoggedWithItsReadingsAndOutcome(t *testing.T) {
	for name, test := range map[string]struct {
		err     error
		outcome string
	}{
		"paused": {nil, "paused"},
		"failed": {errors.New("the goblin's host did not answer"), "the pause failed: the goblin's host did not answer"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{err: test.err}
			s, h, _, newer, now := floorBoard(t, true, spawner, [2]float64{3.2, 8}, [2]float64{3.15, 8})

			// Act
			readFleet(t, s, now, 0, 1)
			awaitDispatch(t, s, spawner, 1)

			// Assert
			switched, err := afk.Read(h.State)
			if err != nil {
				t.Fatal(err)
			}
			entries, _, err := afk.Entries(h.State, switched.Session)
			if err != nil {
				t.Fatal(err)
			}
			pauses := afk.Pauses(entries)
			if len(pauses) != 1 || pauses[0].Task != newer.ID || pauses[0].What != "at the memory floor" || !strings.Contains(pauses[0].Evidence, "3.1 GB of memory") || !strings.Contains(pauses[0].Evidence, "4 GB floor") || pauses[0].Outcome != test.outcome {
				t.Fatalf("logged pauses = %+v, want the newest goblin's pause with its readings and outcome %q", pauses, test.outcome)
			}
			if report := s.afkReport(switched, nil, "not read in this test"); len(report.Paused) != 1 || report.Paused[0] != pauses[0] {
				t.Errorf("the report's pauses = %+v, want the logged pause", report.Paused)
			}
		})
	}
}

// The allowance floor pauses through the same path, so while AFK mode is on
// its pauses are in the morning report too.
func TestAPauseAtTheAllowanceFloorIsLoggedWhileAFKModeIsOn(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, h, _, newer, now := floorBoard(t, true, spawner)
	record, err := state.ReadLifecycle(h.State, newer.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	reset := now.Add(time.Hour)

	// Act
	if began, err := s.pauseAtFloor(newer, record, "allowance", reset.Format(time.RFC3339), "claude's weekly allowance is at the 5 percent floor until it resets"); !began || err != nil {
		t.Fatalf("pauseAtFloor = %v, %v, want the allowance pause begun", began, err)
	}
	calls := awaitDispatch(t, s, spawner, 1)

	// Assert
	if !strings.HasSuffix(strings.Join(calls[0], " "), "--reason allowance --until "+reset.Format(time.RFC3339)) {
		t.Fatalf("dispatch = %v, want the allowance floor's own reason and reset", calls)
	}
	entries, _, err := afk.Entries(h.State, "")
	if err != nil {
		t.Fatal(err)
	}
	if pauses := afk.Pauses(entries); len(pauses) != 1 || pauses[0].What != "at the allowance floor" || pauses[0].Outcome != "paused" {
		t.Fatalf("logged pauses = %+v, want the allowance pause", pauses)
	}
}

// A goblin pushing or merging is left to finish: its host has a git push,
// merge, pull, rebase, cherry-pick or am under it, or a gh pr merge. What
// cannot be read is no sign of either.
func TestAGoblinPushingOrMergingIsToldByWhatRunsUnderItsHost(t *testing.T) {
	processes := []proc.Entry{
		{PID: 10, ParentPID: 1, ExeBase: "cfo.exe"},
		{PID: 11, ParentPID: 10, ExeBase: "claude.exe"},
		{PID: 12, ParentPID: 11, ExeBase: "git.exe"},
		{PID: 20, ParentPID: 1, ExeBase: "cfo.exe"},
		{PID: 21, ParentPID: 20, ExeBase: "git.exe"},
		{PID: 30, ParentPID: 1, ExeBase: "cfo.exe"},
		{PID: 31, ParentPID: 30, ExeBase: "gh.exe"},
	}
	for name, test := range map[string]struct {
		host      int
		arguments map[int][]string
		want      bool
	}{
		"git push with options first":    {10, map[int][]string{12: {`C:\Program Files\Git\cmd\git.exe`, "-c", "credential.helper=manager", "push", "origin", "feat/x"}}, true},
		"git merge in another directory": {10, map[int][]string{12: {"git", "-C", `C:\work\gb-x`, "merge", "origin/main"}}, true},
		"git rebase":                     {10, map[int][]string{12: {"git", "rebase", "--continue"}}, true},
		"git pull":                       {10, map[int][]string{12: {"git", "pull"}}, true},
		"git cherry-pick":                {10, map[int][]string{12: {"git", "cherry-pick", "abc1234"}}, true},
		"gh pr merge":                    {30, map[int][]string{31: {"gh", "pr", "merge", "12", "--merge"}}, true},
		"git status":                     {20, map[int][]string{21: {"git", "status"}}, false},
		"git log naming merge":           {20, map[int][]string{21: {"git", "log", "--merges"}}, false},
		"gh pr view":                     {30, map[int][]string{31: {"gh", "pr", "view", "12"}}, false},
		"another host's push":            {20, map[int][]string{12: {"git", "push"}, 21: {"git", "status"}}, false},
		"an unreadable command line":     {10, map[int][]string{}, false},
	} {
		t.Run(name, func(t *testing.T) {
			arguments := func(pid int) ([]string, error) {
				if args, ok := test.arguments[pid]; ok {
					return args, nil
				}
				return nil, errors.New("access is denied")
			}

			if got := pushingOrMerging(test.host, processes, arguments); got != test.want {
				t.Errorf("pushingOrMerging = %v, want %v", got, test.want)
			}
		})
	}
}

// A pause at the floor is not held back by a log that cannot take its lines:
// the goblin is paused, the reading says why its line is missing, and the
// goblin's card says the pause went through without its line.
func TestAPauseTheLogCannotTakeStillPausesAndSaysSo(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	low := [2]float64{3, 8}
	s, h, _, newer, now := floorBoard(t, true, spawner, low, low)
	audit := filepath.Join(h.State, "afk.audit")
	if err := os.Remove(audit); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(audit, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	readFleet(t, s, now, 0)
	err := s.checkFleet(t.Context(), now.Add(time.Minute))
	calls := awaitDispatch(t, s, spawner, 1)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "AFK mode's log did not take the pause of "+newer.ID) {
		t.Errorf("the reading = %v, want it to say the log did not take the pause", err)
	}
	if len(calls) != 1 || calls[0][1] != newer.ID {
		t.Fatalf("dispatches = %v, want the newest goblin paused all the same", calls)
	}
	s.starts.Lock()
	problem := s.changeErrors[newer.ID].Message
	s.starts.Unlock()
	if !strings.HasPrefix(problem, "paused at the memory floor, but AFK mode's log did not take") {
		t.Errorf("the goblin's change problem = %q, want that it paused but its line is missing", problem)
	}
}
