package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fleet"
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
// goblin, the newer under the older In progress as an automatic start leaves
// it, whose memory meter reads readings in turn.
func floorBoard(t *testing.T, isAFKOn bool, spawner *spawnRecorder, readings ...[2]float64) (*Service, home.Home, state.TaskMeta, state.TaskMeta, time.Time) {
	t.Helper()
	handler, h := startBoard(t, 8*gigabyte, spawner)
	now := time.Now().UTC().Truncate(time.Second)
	older := hostedGoblin(t, h, "older-task", now.Add(time.Second))
	newer := hostedGoblin(t, h, "newer-task", now.Add(2*time.Second))
	if err := fleet.WriteAttention(h, []string{older.ID, newer.ID}); err != nil {
		t.Fatal(err)
	}
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

// readGuard takes one reading of the memory guard at now.
func readGuard(t *testing.T, s *Service, now time.Time) {
	t.Helper()
	if err := s.guardMemory(now); err != nil {
		t.Fatal(err)
	}
}

// awaitRecorded waits until goblins has been run count times, a run still under
// way included: the floor's pause runs goblins from a goroutine of its own.
func awaitRecorded(t *testing.T, spawner *spawnRecorder, count int) [][]string {
	t.Helper()
	// stall-accepted: its callers wait for a run that must not come, so a stall only makes the wait longer.
	deadline := time.Now().Add(3 * time.Second)
	for len(spawner.recorded()) < count && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return spawner.recorded()
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

// The Overlord, 2026-10-08: "nothing pauses automatically by default?? it
// should work regardless of afk thats a critical core feature". Free memory
// fell to 1.9 GB that day with AFK mode off and nothing paused.
func TestTheLowestGoblinPausesAtTheMemoryFloorWhetherAFKModeIsOnOrOff(t *testing.T) {
	for name, test := range map[string]struct {
		low     [2]float64
		isAFKOn bool
	}{
		"memory under the floor, AFK off": {[2]float64{3.9, 8}, false},
		"commit under the floor, AFK off": {[2]float64{8, 3.9}, false},
		"memory under the floor, AFK on":  {[2]float64{3.9, 8}, true},
		"commit under the floor, AFK on":  {[2]float64{8, 3.9}, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, _, _, newer, now := floorBoard(t, test.isAFKOn, spawner, test.low)

			// Act
			readGuard(t, s, now)
			calls := awaitRecorded(t, spawner, 1)

			// Assert
			if len(calls) != 1 {
				t.Fatalf("dispatches after one reading under the floor = %v, want one pause", calls)
			}
			args := strings.Join(calls[0], " ")
			if calls[0][0] != "pause" || calls[0][1] != newer.ID || !strings.Contains(args, "--generation "+newer.SpawnGen) || !strings.HasSuffix(args, "--reason memory") {
				t.Fatalf("dispatches = %v, want one pause of the lowest goblin for memory, with no --until", calls)
			}
		})
	}
}

// With no tree growing, the memory floor pauses the goblin lowest in the
// Overlord's In progress order, the one he ranks last, not the one that
// started last.
func TestTheMemoryFloorPausesTheGoblinLowestInHisOrder(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, h, older, newer, now := floorBoard(t, false, spawner, [2]float64{3, 8})
	if err := fleet.WriteAttention(h, []string{newer.ID, older.ID}); err != nil {
		t.Fatal(err)
	}

	// Act
	readGuard(t, s, now)

	// Assert
	if calls := awaitDispatch(t, s, spawner, 1); len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != older.ID {
		t.Fatalf("dispatches = %v, want the goblin lowest in his order paused", calls)
	}
}

// One goblin is paused at a time: while a pause or any other change is under
// way nothing more is paused, and once it is done the next reading still under
// the floor pauses the next lowest, so a pause has the time to free what it
// frees.
func TestTheMemoryFloorPausesOneGoblinAtATime(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{release: make(chan struct{})}
	low := [2]float64{3, 8}
	s, h, older, newer, now := floorBoard(t, true, spawner, low, low, low, low)

	// Act
	readGuard(t, s, now)
	inFlight := waitForCalls(t, spawner, 1)
	readGuard(t, s, now.Add(memoryGuardEvery))
	readGuard(t, s, now.Add(2*memoryGuardEvery))
	whileInFlight := spawner.recorded()
	close(spawner.release)
	awaitDispatch(t, s, spawner, 1)
	pausedFor(t, h, newer, "memory", "paused", now.Add(2*memoryGuardEvery))
	readGuard(t, s, now.Add(3*memoryGuardEvery))

	// Assert
	if len(inFlight) != 1 || inFlight[0][1] != newer.ID {
		t.Fatalf("first dispatches = %v, want the lowest goblin paused", inFlight)
	}
	if len(whileInFlight) != 1 {
		t.Fatalf("dispatches while the first pause ran = %v, want no second", whileInFlight)
	}
	calls := awaitDispatch(t, s, spawner, 2)
	if len(calls) != 2 || calls[1][0] != "pause" || calls[1][1] != older.ID {
		t.Fatalf("dispatches = %v, want the older goblin paused next", calls)
	}
}

// A goblin whose last pause at the memory floor failed is not asked again
// at every reading; the next lowest is paused instead.
func TestAGoblinWhoseMemoryPauseFailedIsNotPausedAgain(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, h, older, newer, now := floorBoard(t, true, spawner, [2]float64{3, 8})
	pausedFor(t, h, newer, "memory", "failed", now.Add(-time.Minute))

	// Act
	readGuard(t, s, now)

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
			s, h, _, newer, now := floorBoard(t, true, spawner, [2]float64{3.15, 8})

			// Act
			readGuard(t, s, now)
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
				t.Fatalf("logged pauses = %+v, want the lowest goblin's pause with its readings and outcome %q", pauses, test.outcome)
			}
			if report := s.afkReport(switched, nil); len(report.Paused) != 1 || !reflect.DeepEqual(report.Paused[0], pauses[0]) {
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
		{PID: 10, ParentPID: 1, ExeBase: "goblins.exe"},
		{PID: 11, ParentPID: 10, ExeBase: "claude.exe"},
		{PID: 12, ParentPID: 11, ExeBase: "git.exe"},
		{PID: 20, ParentPID: 1, ExeBase: "goblins.exe"},
		{PID: 21, ParentPID: 20, ExeBase: "git.exe"},
		{PID: 30, ParentPID: 1, ExeBase: "goblins.exe"},
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
	s, h, _, newer, now := floorBoard(t, true, spawner, [2]float64{3, 8})
	audit := filepath.Join(h.State, "afk.audit")
	if err := os.Remove(audit); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(audit, 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	err := s.guardMemory(now)
	calls := awaitDispatch(t, s, spawner, 1)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "AFK mode's log did not take the pause of "+newer.ID) {
		t.Errorf("the reading = %v, want it to say the log did not take the pause", err)
	}
	if len(calls) != 1 || calls[0][1] != newer.ID {
		t.Fatalf("dispatches = %v, want the lowest goblin paused all the same", calls)
	}
	s.starts.Lock()
	problem := s.changeErrors[newer.ID].Message
	s.starts.Unlock()
	if !strings.HasPrefix(problem, "paused at the memory floor, but AFK mode's log did not take") {
		t.Errorf("the goblin's change problem = %q, want that it paused but its line is missing", problem)
	}
}

// On 2026-11-02 the memory floor paused goblins within ten minutes of their
// start or resume, each the lowest in the Overlord's order, and each resume
// had cost a session start and a handoff (#1926). A goblin resumed from a
// memory pause in the last ten minutes is passed over for the next lowest,
// and paused only when no other goblin may be.
func TestTheMemoryFloorPassesOverAGoblinJustResumedForMemory(t *testing.T) {
	for _, test := range []struct {
		name         string
		pause, until string
		ago          time.Duration
		isAlone      bool
		wantPaused   string
		wantEvidence string
	}{
		{name: "resumed from a memory pause a minute ago", pause: "memory", ago: time.Minute, wantPaused: "older-task", wantEvidence: "older-task was the lowest goblin in the In progress order not pushing or merging past newer-task, which resumed from a memory pause in the last 10 minutes"},
		{name: "resumed from a memory pause eleven minutes ago", pause: "memory", ago: 11 * time.Minute, wantPaused: "newer-task", wantEvidence: "newer-task was the lowest goblin in the In progress order not pushing or merging"},
		{name: "resumed from a dependency a minute ago", pause: "dependency", until: "date:2026-11-02T19:00:00Z", ago: time.Minute, wantPaused: "newer-task", wantEvidence: "newer-task was the lowest goblin in the In progress order not pushing or merging"},
		{name: "resumed from a memory pause a minute ago and the only goblin", pause: "memory", ago: time.Minute, isAlone: true, wantPaused: "newer-task", wantEvidence: "newer-task was the lowest goblin in the In progress order not pushing or merging, and every goblin it could pause resumed from a memory pause in the last 10 minutes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h, older, newer, now := floorBoard(t, true, spawner, [2]float64{3, 8})
			resumedAt := now.Add(-test.ago)
			condition, err := state.NewPauseCondition(test.pause, test.until, resumedAt.Add(-time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: newer.ID, Generation: newer.SpawnGen, Operation: "auto-resume-" + newer.ID, Action: "resume", Phase: "running", Started: resumedAt, Updated: resumedAt, Pause: &condition, Reason: "Pause condition cleared"}); err != nil {
				t.Fatal(err)
			}
			if test.isAlone {
				if err := os.Remove(filepath.Join(h.State, "hosts", older.ID+".json")); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			readGuard(t, s, now)
			calls := awaitDispatch(t, s, spawner, 1)

			// Assert
			if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != test.wantPaused {
				t.Fatalf("dispatches = %v, want %s paused", calls, test.wantPaused)
			}
			entries, _, err := afk.Entries(h.State, "")
			if err != nil {
				t.Fatal(err)
			}
			if pauses := afk.Pauses(entries); len(pauses) != 1 || !strings.HasSuffix(pauses[0].Evidence, "; "+test.wantEvidence) {
				t.Errorf("logged pauses = %+v, want one whose evidence ends %q", pauses, test.wantEvidence)
			}
		})
	}
}

// The pause at the floor needs only the floor: a services record or a start
// costs file that cannot be read stops what starts, never the pause that
// keeps the machine working.
func TestTheMemoryFloorPausesWhileARecordOnlyAdmissionReadsIsUnreadable(t *testing.T) {
	for _, record := range []string{"services.json", "start-costs.json"} {
		t.Run(record, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h, _, newer, now := floorBoard(t, false, spawner, [2]float64{3.9, 8}, [2]float64{3.9, 8})
			writeFile(t, filepath.Join(h.State, record), "{")

			// Act
			readGuard(t, s, now)
			calls := awaitDispatch(t, s, spawner, 1)

			// Assert
			if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != newer.ID {
				t.Fatalf("dispatches = %v, want %s paused at the floor", calls, newer.ID)
			}
		})
	}
}

// floorCandidates are goblins named by id, lowest in the Overlord's order
// first, each with the growth of its process tree in megabytes, and the ids of
// those resumed from a memory pause a minute before now, which pickToPause
// passes over.
func floorCandidates(now time.Time, resumed []string, growth map[string]uint64, ids ...string) []floorCandidate {
	var candidates []floorCandidate
	for i, id := range ids {
		meta := state.TaskMeta{ID: id, SpawnGen: "generation-" + id}
		candidate := floorCandidate{meta: meta, host: host.Record{HostPID: 100 + i}, growth: growth[id] << 20}
		if slices.Contains(resumed, id) {
			condition, _ := state.NewPauseCondition("memory", "", now.Add(-time.Hour))
			candidate.record = state.Lifecycle{ID: id, Generation: meta.SpawnGen, Action: "resume", Phase: "running", Updated: now.Add(-time.Minute), Pause: &condition}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

// The memory floor pauses the goblin whose process tree grew most since the
// reading before, not the one lowest in the order, and falls back to the lowest
// when none grew. Pushing or merging, a resume from a memory pause in the
// last ten minutes and a turn of the heavy line that runs still pass a goblin
// over, the turn's holder until it is the last goblin it could pause (#2808).
func TestTheMemoryFloorPicksTheGoblinWhoseTreeGrewMost(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name       string
		ids        []string
		growth     map[string]uint64
		resumed    []string
		isPushing  []int
		holding    []string
		want       string
		wantWhy    string
		wantNoPick string
	}{
		{name: "the one that grew most holds the heavy line", ids: []string{"low", "mid", "high"}, growth: map[string]uint64{"low": 10, "mid": 300, "high": 20}, holding: []string{"mid"}, want: "high", wantWhy: "the goblin whose process tree grew most since the reading before, by 20.0 MB past mid, whose turn of the heavy line runs"},
		{name: "the holder is the last it could pause", ids: []string{"low", "mid"}, growth: map[string]uint64{"low": 10, "mid": 300}, isPushing: []int{100}, holding: []string{"mid"}, want: "mid", wantWhy: "the goblin whose process tree grew most since the reading before, by 300.0 MB, and the last goblin it could pause, though its turn of the heavy line runs"},
		{name: "a goblin just resumed goes before the holder", ids: []string{"low", "mid"}, growth: map[string]uint64{"low": 5, "mid": 300}, resumed: []string{"low"}, holding: []string{"mid"}, want: "low", wantWhy: "the goblin whose process tree grew most since the reading before, by 5.0 MB past mid, whose turn of the heavy line runs, and every other goblin it could pause resumed from a memory pause in the last 10 minutes"},
		{name: "the one that grew most", ids: []string{"low", "mid", "high"}, growth: map[string]uint64{"low": 10, "mid": 300, "high": 20}, want: "mid", wantWhy: "the goblin whose process tree grew most since the reading before, by 300.0 MB"},
		{name: "none grew", ids: []string{"low", "mid", "high"}, want: "low", wantWhy: "the lowest goblin in the In progress order not pushing or merging"},
		{name: "a tie goes to the lowest", ids: []string{"low", "mid", "high"}, growth: map[string]uint64{"mid": 50, "high": 50}, want: "mid", wantWhy: "the goblin whose process tree grew most since the reading before, by 50.0 MB"},
		{name: "the one that grew most is pushing", ids: []string{"low", "mid", "high"}, growth: map[string]uint64{"mid": 300, "high": 20}, isPushing: []int{101}, want: "high", wantWhy: "the goblin whose process tree grew most since the reading before, by 20.0 MB"},
		{name: "the one that grew most just resumed", ids: []string{"low", "mid", "high"}, growth: map[string]uint64{"low": 5, "mid": 300}, resumed: []string{"mid"}, want: "low", wantWhy: "the goblin whose process tree grew most since the reading before, by 5.0 MB past mid, which resumed from a memory pause in the last 10 minutes"},
		{name: "only goblins just resumed", ids: []string{"low", "mid"}, growth: map[string]uint64{"low": 5, "mid": 300}, resumed: []string{"low", "mid"}, want: "mid", wantWhy: "the goblin whose process tree grew most since the reading before, by 300.0 MB, and every goblin it could pause resumed from a memory pause in the last 10 minutes"},
		{name: "every goblin is pushing", ids: []string{"low", "mid"}, isPushing: []int{100, 101}, wantNoPick: "nothing to pause: every goblin it could pause is pushing or merging"},
		{name: "no goblin may be paused", wantNoPick: "nothing to pause: no live goblin may be paused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			candidates := floorCandidates(now, test.resumed, test.growth, test.ids...)
			for i := range candidates {
				candidates[i].isHoldingTurn = slices.Contains(test.holding, candidates[i].meta.ID)
			}
			isPushing := func(hostPID int) bool { return slices.Contains(test.isPushing, hostPID) }

			// Act
			pick, isFound, nothing := pickToPause(candidates, isPushing, now)

			// Assert
			if test.wantNoPick != "" {
				if isFound || nothing != test.wantNoPick {
					t.Fatalf("pick = %+v, %v, %q, want none because %q", pick, isFound, nothing, test.wantNoPick)
				}
				return
			}
			if !isFound || pick.meta.ID != test.want || pick.why != test.wantWhy {
				t.Fatalf("pick = %s because %q (found %v), want %s because %q", pick.meta.ID, pick.why, isFound, test.want, test.wantWhy)
			}
		})
	}
}

// While a goblin's turn of the heavy line runs, the floor waits out one
// reading that dips less than a gigabyte under it, and the next reading under
// it pauses the goblin that holds no turn, though the holder stands lowest in
// the order, saying it passed the holder. A deeper dip pauses at once, the
// holder only when it is the last goblin it could pause, and a line that
// cannot be read spares nobody, its failure reported (#2808).
func TestTheMemoryFloorPausesTheHeavyLinesHolderLast(t *testing.T) {
	for _, test := range []struct {
		name       string
		holders    []string
		holdersErr error
		readings   [][2]float64
		wantPaused string
		wantAction string
	}{
		{name: "a dip is waited out", holders: []string{"newer-task"}, readings: [][2]float64{{3.5, 8}}, wantAction: "waited out a dip under the floor while newer-task's turn of the heavy line runs: the next reading under it pauses a goblin"},
		{name: "the holder is spared", holders: []string{"newer-task"}, readings: [][2]float64{{3.5, 8}, {3.5, 8}}, wantPaused: "older-task", wantAction: "paused older-task: the lowest goblin in the In progress order not pushing or merging past newer-task, whose turn of the heavy line runs"},
		{name: "a deep dip pauses at once", holders: []string{"newer-task"}, readings: [][2]float64{{2.9, 8}}, wantPaused: "older-task", wantAction: "paused older-task: the lowest goblin in the In progress order not pushing or merging past newer-task, whose turn of the heavy line runs"},
		{name: "every goblin holds a turn", holders: []string{"newer-task", "older-task"}, readings: [][2]float64{{3.5, 8}, {3.5, 8}}, wantPaused: "newer-task", wantAction: "paused newer-task: the lowest goblin in the In progress order not pushing or merging, and the last goblin it could pause, though its turn of the heavy line runs"},
		{name: "the line cannot be read", holdersErr: errors.New("the line is unreadable"), readings: [][2]float64{{3.5, 8}}, wantPaused: "newer-task", wantAction: "paused newer-task: the lowest goblin in the In progress order not pushing or merging"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, _, _, _, now := floorBoard(t, false, spawner, test.readings...)
			s.Options.Dispatch.TurnHolders = func() ([]string, error) { return test.holders, test.holdersErr }

			// Act
			var err error
			for reading := range test.readings {
				err = errors.Join(err, s.guardMemory(now.Add(time.Duration(reading)*memoryGuardEvery)))
			}
			wantCalls := 0
			if test.wantPaused != "" {
				wantCalls = 1
			}
			calls := awaitRecorded(t, spawner, max(wantCalls, 1))

			// Assert
			if (err != nil) != (test.holdersErr != nil) || err != nil && !strings.Contains(err.Error(), "spares none for one: the line is unreadable") {
				t.Errorf("guard reading error = %v, want one only when the line cannot be read", err)
			}
			if len(calls) != wantCalls || wantCalls == 1 && (calls[0][0] != "pause" || calls[0][1] != test.wantPaused) {
				t.Fatalf("dispatches = %v, want %q paused", calls, test.wantPaused)
			}
			if guard := s.guard.reading(); guard == nil || guard.Action != test.wantAction {
				t.Fatalf("guard = %+v, want the action %q", guard, test.wantAction)
			}
		})
	}
}

// The board's memory carries the reading the guard last acted on, whether it
// paused a goblin or found none it may, and nothing before it has acted.
func TestTheBoardsMemoryCarriesTheReadingTheGuardLastActedOn(t *testing.T) {
	for _, test := range []struct {
		name       string
		isFailed   bool
		wantAction string
	}{
		{name: "a goblin paused", wantAction: "paused newer-task: the lowest goblin in the In progress order not pushing or merging"},
		{name: "no goblin may be paused", isFailed: true, wantAction: "nothing to pause: no live goblin may be paused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, h, older, newer, now := floorBoard(t, false, spawner)
			steady := Memory{Available: uint64(3.5 * gigabyte), CommitAvailable: 8 * gigabyte, Total: 32 * gigabyte}
			s.Options.Dispatch.Memory = func() (Memory, error) { return steady, nil }
			before, err := s.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if test.isFailed {
				pausedFor(t, h, older, "memory", "failed", now.Add(-time.Minute))
				pausedFor(t, h, newer, "memory", "failed", now.Add(-time.Minute))
			}

			// Act
			readGuard(t, s, now)
			after, err := s.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if before.Memory != nil && before.Memory.Guard != nil {
				t.Errorf("guard before any action = %+v, want none", before.Memory.Guard)
			}
			guard := after.Memory.Guard
			if guard == nil || !guard.At.Equal(now) || guard.Available != steady.Available || guard.CommitAvailable != steady.CommitAvailable || guard.Floor != 4*gigabyte || guard.Action != test.wantAction {
				t.Fatalf("guard = %+v, want the reading at %v with action %q", guard, now, test.wantAction)
			}
		})
	}
}

// A reading at or above the floor, or one met while a change is under way or
// the PC restarts, is not acted on, so the board keeps no guard reading for it.
func TestTheGuardKeepsNoReadingItDidNotActOn(t *testing.T) {
	for name, busy := range map[string]func(s *Service, task string){
		"a pause is under way": func(s *Service, task string) { s.markChanging(task, "pause") },
		"the PC is restarting": func(s *Service, _ string) { s.pcRestart = &PCRestart{State: "stopping"} },
		"nothing is under way": func(*Service, string) {},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			s, _, _, newer, now := floorBoard(t, false, spawner, [2]float64{8, 8}, [2]float64{3, 8})
			readGuard(t, s, now)
			afterAbove := s.guard.reading()
			s.starts.Lock()
			busy(s, newer.ID)
			s.starts.Unlock()

			// Act
			readGuard(t, s, now.Add(memoryGuardEvery))

			// Assert
			isActed := name == "nothing is under way"
			if afterAbove != nil || (s.guard.reading() != nil) != isActed || (len(awaitRecorded(t, spawner, 1)) != 0) != isActed {
				t.Fatalf("guard = %+v after a reading above the floor, %+v after one under it, dispatches %v, want acted on only when nothing is under way", afterAbove, s.guard.reading(), spawner.recorded())
			}
		})
	}
}

// A reading of the guard that fails reaches every recovery cycle's error for
// as long as the failure lasts, and the guard never publishes it on its own,
// which a recovery cycle would wipe at once.
func TestAFailingGuardReadingReachesEveryRecoveryCycleWhileItFails(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, _, _, _, _ := floorBoard(t, false, spawner)
	s.work, s.subscribers = make(chan struct{}, 1), map[chan struct{}]struct{}{}
	var reads atomic.Int32
	s.Options.Dispatch.Memory = func() (Memory, error) {
		reads.Add(1)
		return Memory{}, errors.New("memory cannot be read")
	}
	shown := func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.lastError
	}
	waitForReads := func(count int32) {
		for deadline := time.Now().Add(30 * time.Second); reads.Load() < count && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		}
	}
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.keepMemoryGuard(ctx, time.Millisecond)
	}()
	defer func() { stop(); <-done }()
	const want = "the memory guard cannot read memory: memory cannot be read"

	// Act
	waitForReads(3)
	beforeCycle := shown()
	var cycles []string
	for range 2 {
		waitForReads(reads.Load() + 3)
		s.cycle(t.Context(), true)
		cycles = append(cycles, shown())
	}

	// Assert
	if beforeCycle != "" {
		t.Errorf("the board showed %q before any recovery cycle, want the guard to leave publishing to the cycle", beforeCycle)
	}
	for cycle, line := range cycles {
		if strings.Count(line, want) != 1 {
			t.Errorf("recovery cycle %d showed %q, want %q once", cycle+1, line, want)
		}
	}
}

// The running supervisor reads memory with its own guard: its first reading
// under the floor pauses the lowest goblin, with no reading of the test's.
func TestTheRunningSupervisorsGuardPausesAGoblinUnderTheFloor(t *testing.T) {
	// Arrange
	_, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	older := hostedGoblin(t, h, "older-task", now.Add(time.Second))
	newer := hostedGoblin(t, h, "newer-task", now.Add(2*time.Second))
	if err := fleet.WriteAttention(h, []string{older.ID, newer.ID}); err != nil {
		t.Fatal(err)
	}
	spawner := &spawnRecorder{}
	options := Options{Dispatch: &Dispatch{
		Memory: func() (Memory, error) {
			return Memory{Available: 3 * gigabyte, Total: 32 * gigabyte, CommitAvailable: 8 * gigabyte, CommitLimit: 48 * gigabyte}, nil
		},
		Disk:          func() (Disk, error) { return diskWithFree(200 * gigabyte), nil },
		CommitHolders: func() ([]CommitHolder, error) { return nil, nil },
		Spawn:         spawner.spawn,
	}}

	// Act
	s, err := Start(t.Context(), h, options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	calls := awaitRecorded(t, spawner, 1)

	// Assert
	if len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != newer.ID || !strings.HasSuffix(strings.Join(calls[0], " "), "--reason memory") {
		t.Fatalf("dispatches of the running supervisor = %v, want one memory pause of %s", calls, newer.ID)
	}
}

// The fleet's reading once a minute marks a reading under the floor low and
// pauses nothing, however many readings fall under it: the guard alone pauses.
func TestAFleetReadingUnderTheFloorPausesNothing(t *testing.T) {
	// Arrange
	spawner := &spawnRecorder{}
	s, h, _, _, now := floorBoard(t, true, spawner, [2]float64{3, 8}, [2]float64{3, 8}, [2]float64{3, 8})

	// Act
	readFleet(t, s, now, 0, 1, 2)
	calls := awaitRecorded(t, spawner, 1)

	// Assert
	wakes, err := readFleetWakes(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 || !wakes.MemoryLow {
		t.Fatalf("after three fleet readings under the floor: dispatches %v, reading low %v, want no dispatch and the reading low", calls, wakes.MemoryLow)
	}
}
