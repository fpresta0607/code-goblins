package supervisor

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// cfoWaitStarted is when the waiting goblin of these tests made its report.
var cfoWaitStarted = time.Date(2026, 10, 5, 14, 36, 0, 0, time.UTC)

const (
	waitingGoblin = "cg-waits"
	// cfoBacklog holds a row the CFO owns and a row a goblin would take, and
	// one of each done.
	cfoBacklog = "## Queued\n- [ ] cfo-review - Review the schema plan (kind: cfo)\n- [ ] next-ship - Ship the view (kind: ship)\n## Done\n- [x] cfo-plan - Approve the rollout plan (kind: cfo)\n- [x] old-ship - Ship the list (kind: ship)\n"
	// pulls209 is the goblin's pull request, green, with the draft flag and
	// labels each test gives it.
	pulls209 = `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","baseRefName":"main","mergeable":"MERGEABLE","isDraft":%t,"labels":[%s],"author":{"login":"o"},"statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-10-05T14:30:00Z"}]}]`
	pendingPull209 = `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","baseRefName":"main","mergeable":"MERGEABLE","isDraft":false,"labels":[],"author":{"login":"o"},"statusCheckRollup":[
{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":""}]}]`
	donePull209 = "done: PR https://github.com/o/r/pull/209"
)

// pull209 is the goblin's green pull request, a draft or not, with labels.
func pull209(isDraft bool, labels ...string) string {
	quoted := make([]string, len(labels))
	for index, label := range labels {
		quoted[index] = `{"name":"` + label + `"}`
	}
	return fmt.Sprintf(pulls209, isDraft, strings.Join(quoted, ","))
}

// waitingFleet is a fleet whose one live goblin, Jerry, works in a project
// whose fake forge lists pulls, under the backlog cfoBacklog.
func waitingFleet(t *testing.T, pulls string) (*Service, home.Home, *fakeForge) {
	t.Helper()
	s, h := fleetService(t)
	project := t.TempDir()
	meta := state.TaskMeta{ID: waitingGoblin, GoblinName: "Jerry", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+waitingGoblin), Harness: "claude", Backend: "native", SpawnGen: "s1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "backlog.md"), cfoBacklog)
	forge := forgeFor(project, waitingGoblin)
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", pulls
	s.Options.CI = forge
	return s, h, forge
}

// report writes line to the goblin's status log as made at at.
func report(t *testing.T, h home.Home, at time.Time, line string) {
	t.Helper()
	file, err := os.OpenFile(state.StatusPath(h.State, waitingGoblin), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(at.Format(time.RFC3339) + " " + line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// recordLifecycle writes the goblin's lifecycle record of its current generation.
func recordLifecycle(t *testing.T, h home.Home, action, phase string, pause *state.PauseCondition) {
	t.Helper()
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: waitingGoblin, Generation: "s1", Operation: "op-1", Action: action, Phase: phase, Pause: pause}); err != nil {
		t.Fatal(err)
	}
}

// cfoWaitWakes is every queued wake that says a goblin waits on the CFO.
func cfoWaitWakes(t *testing.T, h home.Home) []wake.Record {
	t.Helper()
	var waits []wake.Record
	for _, record := range fleetWakeRecords(t, h, "check") {
		if strings.HasPrefix(record.Detail, "cfo_wait: ") {
			waits = append(waits, record)
		}
	}
	return waits
}

// sweepUntil takes one fleet reading a minute after *now until until, and
// returns the wakes for goblins waiting on the CFO they raised.
func sweepUntil(t *testing.T, s *Service, h home.Home, now *time.Time, until time.Time) []wake.Record {
	t.Helper()
	before := len(cfoWaitWakes(t, h))
	for now.Before(until) {
		*now = now.Add(time.Minute)
		if err := s.checkFleet(t.Context(), *now); err != nil {
			t.Fatal(err)
		}
	}
	return cfoWaitWakes(t, h)[before:]
}

// A goblin that says it waits on the CFO wakes the CFO once it has waited ten
// minutes, naming it, what it waits for and how long it has waited, and
// again every half hour while it still waits: on 2026-11-02 such waits went
// unseen for up to two hours (#1912). AFK mode changes none of it, since the
// CFO still decides while the Overlord is away.
func TestAGoblinWaitingOnTheCFOWakesItAfterTenMinutesAndEveryHalfHour(t *testing.T) {
	for _, isAFKOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("AFK mode on: %t", isAFKOn), func(t *testing.T) {
			// Arrange
			s, h, _ := waitingFleet(t, "[]")
			if isAFKOn {
				if _, _, err := afk.TurnOn(h.State, "the board", nil, cfoWaitStarted); err != nil {
					t.Fatal(err)
				}
			}
			if s.afkOn() != isAFKOn {
				t.Fatalf("AFK mode reads on: %t, want %t", s.afkOn(), isAFKOn)
			}
			report(t, h, cfoWaitStarted, "waiting on cfo: which schema do I migrate first?")
			now := cfoWaitStarted

			// Act and Assert
			if early := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(9*time.Minute)); len(early) != 0 {
				t.Fatalf("a wait of nine minutes woke the CFO: %+v", early)
			}
			first := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(10*time.Minute))
			if len(first) != 1 || first[0].Key != waitingGoblin {
				t.Fatalf("a wait of ten minutes = %+v, want one wake keyed by the goblin", first)
			}
			for _, want := range []string{"cfo_wait: Jerry (cg-waits) has waited 10 minutes on the CFO", "waiting on cfo: which schema do I migrate first?", `next: answer it with cfo send cg-waits "<your answer>"`} {
				if !strings.Contains(first[0].Detail, want) {
					t.Errorf("wake %q lacks %q", first[0].Detail, want)
				}
			}
			if held := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(39*time.Minute)); len(held) != 0 {
				t.Fatalf("the same wait woke the CFO again within half an hour: %+v", held)
			}
			again := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(40*time.Minute))
			if len(again) != 1 || !strings.Contains(again[0].Detail, "has waited 40 minutes") {
				t.Fatalf("the wait half an hour on = %+v, want one wake saying it has waited 40 minutes", again)
			}
		})
	}
}

// Each shape of a wait on the CFO wakes it: a wait on the CFO itself, a done
// on a pull request no merge train takes because it is a draft or held, and
// a wait on, or a pause until, a backlog row the CFO owns, named with its
// title, done or not, and any of them made by a goblin whose pause failed
// and that carries on. Every wait that ends by itself or is another's wakes
// nobody, and nor does a goblin whose failed pause took hold.
func TestEachWaitOnTheCFOWakesItAndNoWaitByDesignDoes(t *testing.T) {
	pausedUntil := func(until string) *state.PauseCondition {
		return &state.PauseCondition{Reason: "dependency", Until: until, At: cfoWaitStarted}
	}
	for _, test := range []struct {
		name   string
		report string
		pulls  string
		pause  *state.PauseCondition
		// phase is the pause's phase, paused unless it says otherwise.
		phase string
		// isHosted gives the goblin a terminal host that runs.
		isHosted bool
		want     []string
	}{
		{name: "a wait on the CFO", report: "waiting on cfo: is the plan right?", want: []string{"waiting on cfo: is the plan right?"}},
		{name: "a done on a draft", report: donePull209, pulls: pull209(true), want: []string{donePull209 + ", which no merge train takes: it is a draft", "review https://github.com/o/r/pull/209"}},
		{name: "a done on a held pull request", report: donePull209, pulls: pull209(false, "Hold"), want: []string{donePull209 + ", which no merge train takes: it is held: it carries the hold label"}},
		{name: "a wait on a row the CFO owns", report: "waiting on cfo-review: the schema plan first", want: []string{"waiting on cfo-review: the schema plan first; cfo-review is the CFO's own backlog row cfo-review (Review the schema plan)", "finish backlog row cfo-review"}},
		{name: "a pause until a row the CFO owns", report: "working: migrating", pause: pausedUntil("task:cfo-review"), want: []string{"paused until the CFO's own backlog row cfo-review (Review the schema plan) is done", "cfo resume cg-waits"}},
		{name: "a wait on a row the CFO finished", report: "waiting on cfo-plan: the rollout plan first", want: []string{"cfo-plan is the CFO's own backlog row cfo-plan (Approve the rollout plan), done in the backlog", `next: tell cg-waits that cfo-plan is done with cfo send cg-waits "<what>"`}},
		{name: "a pause until a row the CFO finished", report: "working: migrating", pause: pausedUntil("task:cfo-plan"), want: []string{"paused until the CFO's own backlog row cfo-plan (Approve the rollout plan), which is done", "next: resume cg-waits with cfo resume cg-waits"}},
		{name: "a wait on the CFO after a pause that failed", report: "waiting on cfo: is the plan right?", pause: &state.PauseCondition{Reason: "memory", At: cfoWaitStarted}, phase: "failed", isHosted: true, want: []string{"waiting on cfo: is the plan right?"}},
		{name: "a wait on the CFO once a failed pause took hold", report: "waiting on cfo: is the plan right?", pause: &state.PauseCondition{Reason: "memory", At: cfoWaitStarted}, phase: "failed"},
		{name: "a wait on CI", report: "waiting on ci: the checks of #209"},
		{name: "a wait on a deploy", report: "waiting on deploy: the preview"},
		{name: "a wait on the Overlord", report: "waiting on overlord: his sign-in"},
		{name: "a wait on memory", report: "waiting on memory: the full suite needs 5 GB"},
		{name: "a wait on another goblin's task", report: "waiting on cg-other: its schema lands first"},
		{name: "a wait on a row a goblin takes", report: "waiting on next-ship: its view lands first"},
		{name: "a wait on a row a goblin finished", report: "waiting on old-ship: its list landed"},
		{name: "a done on a pull request a train may take", report: donePull209, pulls: pendingPull209},
		{name: "a pause until a row a goblin takes", report: "working: migrating", pause: pausedUntil("task:next-ship")},
		{name: "a pause until another goblin's task", report: "working: migrating", pause: pausedUntil("task:cg-other")},
		{name: "work", report: "working: migrating the schema"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, h, _ := waitingFleet(t, cmp.Or(test.pulls, "[]"))
			report(t, h, cfoWaitStarted, test.report)
			if test.pause != nil {
				recordLifecycle(t, h, "pause", cmp.Or(test.phase, "paused"), test.pause)
			}
			if test.isHosted {
				terminal, err := json.Marshal(host.Record{ID: waitingGoblin, HostPID: os.Getpid(), Started: time.Now(), Pipe: "fixture", Token: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(h.State, "hosts", waitingGoblin+".json"), string(terminal))
			}
			now := cfoWaitStarted

			// Act
			woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(15*time.Minute))

			// Assert
			if len(test.want) == 0 {
				if len(woke) != 0 {
					t.Fatalf("a wait by design woke the CFO: %+v", woke)
				}
				return
			}
			if len(woke) != 1 {
				t.Fatalf("the wait woke the CFO %d times, want once: %+v", len(woke), woke)
			}
			for _, want := range append(test.want, "Jerry (cg-waits) has waited 10 minutes on the CFO") {
				if !strings.Contains(woke[0].Detail, want) {
					t.Errorf("wake %q lacks %q", woke[0].Detail, want)
				}
			}
		})
	}
}

// A wait ends, and wakes nobody again, as soon as the goblin reports
// anything newer, its harness takes a prompt such as the CFO's answer, the
// Overlord pauses it, it is stopped or it is retired; and a goblin that waits
// again later is a new wait, which wakes the CFO ten minutes into it.
func TestAWaitOnTheCFOEndsWhenTheGoblinMovesOn(t *testing.T) {
	for _, test := range []struct {
		name string
		end  func(t *testing.T, h home.Home, at time.Time)
	}{
		{name: "a newer report", end: func(t *testing.T, h home.Home, at time.Time) { report(t, h, at, "working: on the schema") }},
		{name: "an answer", end: func(t *testing.T, h home.Home, at time.Time) {
			directory := nativehook.SpoolDir(h.State)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			event, err := json.Marshal(nativehook.Event{Schema: 1, ID: "prompt-1", Harness: "claude", Kind: "prompt", TaskID: waitingGoblin, Generation: "s1", Role: "goblin", OccurredAt: at, Prompt: true})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(directory, "prompt-1.event.json"), string(event))
		}},
		{name: "a pause by the Overlord", end: func(t *testing.T, h home.Home, at time.Time) {
			recordLifecycle(t, h, "pause", "paused", &state.PauseCondition{Reason: "overlord", At: at})
		}},
		{name: "a stop", end: func(t *testing.T, h home.Home, at time.Time) { recordLifecycle(t, h, "stop", "stopped", nil) }},
		{name: "its retirement", end: func(t *testing.T, h home.Home, at time.Time) {
			if err := os.Remove(state.TaskMetaPath(h.State, waitingGoblin)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, h, _ := waitingFleet(t, "[]")
			report(t, h, cfoWaitStarted, "waiting on cfo: which schema do I migrate first?")
			now := cfoWaitStarted
			if woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
				t.Fatalf("the wait woke the CFO %d times at ten minutes, want once", len(woke))
			}

			// Act
			test.end(t, h, now.Add(30*time.Second))
			woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(75*time.Minute))

			// Assert
			if len(woke) != 0 {
				t.Fatalf("a wait that ended woke the CFO: %+v", woke)
			}
			watched, err := readFleetWakes(h.State)
			if err != nil {
				t.Fatal(err)
			}
			for key := range watched.Woke {
				if strings.HasPrefix(key, cfoWaitKeyPrefix) {
					t.Errorf("the ended wait is still remembered as %s", key)
				}
			}
		})
	}
}

// A goblin that waits on the CFO again after a wait ended is a new wait: it
// wakes the CFO ten minutes into it, however lately the last one did.
func TestANewWaitOnTheCFOWakesTenMinutesIntoIt(t *testing.T) {
	// Arrange
	s, h, _ := waitingFleet(t, "[]")
	report(t, h, cfoWaitStarted, "waiting on cfo: which schema do I migrate first?")
	now := cfoWaitStarted
	if woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
		t.Fatalf("the first wait woke the CFO %d times, want once", len(woke))
	}
	report(t, h, cfoWaitStarted.Add(11*time.Minute), "working: on the schema")
	sweepUntil(t, s, h, &now, cfoWaitStarted.Add(12*time.Minute))

	// Act
	report(t, h, cfoWaitStarted.Add(13*time.Minute), "waiting on cfo: may I drop the old column?")
	woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(23*time.Minute))

	// Assert
	if len(woke) != 1 || !strings.Contains(woke[0].Detail, "may I drop the old column?") || !strings.Contains(woke[0].Detail, "has waited 10 minutes") {
		t.Fatalf("the new wait = %+v, want one wake for it ten minutes in", woke)
	}
}

// A done on a pull request the train would not take waits only while the
// pull request stays a draft or held: once the CI poll reads it ready and
// off hold, the train's to take, and the wait ends.
func TestADoneOnAHeldPullRequestEndsWhenTheHoldComesOff(t *testing.T) {
	// Arrange
	s, h, forge := waitingFleet(t, pull209(false, "hold"))
	report(t, h, cfoWaitStarted, donePull209)
	now := cfoWaitStarted
	if woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
		t.Fatalf("the held pull request woke the CFO %d times, want once", len(woke))
	}

	// Act
	forge.mu.Lock()
	forge.pulls = pendingPull209
	forge.mu.Unlock()
	woke := sweepUntil(t, s, h, &now, cfoWaitStarted.Add(75*time.Minute))

	// Assert
	if len(woke) != 0 {
		t.Fatalf("a pull request the train may take kept waking the CFO: %+v", woke)
	}
	if waits, err := CFOWaits(h); err != nil || len(waits) != 0 {
		t.Fatalf("CFOWaits = %+v, %v, want none once the hold came off", waits, err)
	}
}

// The digest and the checkpoint read the waits as the sweep does, with what
// each waits for, from the record the CI poll left on disk.
func TestCFOWaitsReadsTheWaitsTheSweepWakesFor(t *testing.T) {
	// Arrange
	s, h, _ := waitingFleet(t, pull209(true))
	report(t, h, cfoWaitStarted, donePull209)
	now := cfoWaitStarted
	sweepUntil(t, s, h, &now, cfoWaitStarted.Add(2*time.Minute))

	// Act
	waits, err := CFOWaits(h)

	// Assert
	if err != nil || len(waits) != 1 {
		t.Fatalf("CFOWaits = %+v, %v, want the one goblin", waits, err)
	}
	if got := waits[0]; got.Who() != "Jerry (cg-waits)" || !got.Since.Equal(cfoWaitStarted) || got.Minutes(now) != 2 || !strings.Contains(got.What, "it is a draft") {
		t.Fatalf("CFOWaits[0] = %+v, want Jerry's wait on its draft since %s", got, cfoWaitStarted)
	}
}
