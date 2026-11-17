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
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// ceoWaitStarted is when the waiting goblin of these tests made its report.
var ceoWaitStarted = time.Date(2026, 10, 5, 14, 36, 0, 0, time.UTC)

const (
	waitingGoblin = "cg-waits"
	// ceoBacklog holds a row the CEO owns and a row a goblin would take, and
	// one of each done.
	ceoBacklog = "## Queued\n- [ ] ceo-review - Review the schema plan (kind: ceo)\n- [ ] next-ship - Ship the view (kind: ship)\n## Done\n- [x] ceo-plan - Approve the rollout plan (kind: ceo)\n- [x] old-ship - Ship the list (kind: ship)\n"
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
// whose fake forge lists pulls, under the backlog ceoBacklog.
func waitingFleet(t *testing.T, pulls string) (*Service, home.Home, *fakeForge) {
	t.Helper()
	s, h := fleetService(t)
	project := t.TempDir()
	meta := state.TaskMeta{ID: waitingGoblin, GoblinName: "Jerry", Project: project, Worktree: filepath.Join(project, ".worktrees", "gb-"+waitingGoblin), Harness: "claude", Backend: "native", SpawnGen: "s1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.Data, "backlog.md"), ceoBacklog)
	forge := forgeFor(project, waitingGoblin)
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", pulls
	s.Options.CI = forge
	s.Options.GitHub = githubtest.Client(forge)
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

// ceoWaitWakes is every queued wake that says a goblin waits on the CEO.
func ceoWaitWakes(t *testing.T, h home.Home) []wake.Record {
	t.Helper()
	var waits []wake.Record
	for _, record := range fleetWakeRecords(t, h, "check") {
		if strings.HasPrefix(record.Detail, "ceo_wait: ") {
			waits = append(waits, record)
		}
	}
	return waits
}

// sweepUntil takes one fleet reading a minute after *now until until, and
// returns the wakes for goblins waiting on the CEO they raised.
func sweepUntil(t *testing.T, s *Service, h home.Home, now *time.Time, until time.Time) []wake.Record {
	t.Helper()
	before := len(ceoWaitWakes(t, h))
	for now.Before(until) {
		*now = now.Add(time.Minute)
		if err := s.checkFleet(t.Context(), *now); err != nil {
			t.Fatal(err)
		}
	}
	return ceoWaitWakes(t, h)[before:]
}

// A goblin that says it waits on the CEO wakes the CEO once it has waited ten
// minutes, naming it, what it waits for and how long it has waited, and
// again every half hour while it still waits: on 2026-11-02 such waits went
// unseen for up to two hours (#1912). AFK mode changes none of it, since the
// CEO still decides while the Overlord is away.
func TestAGoblinWaitingOnTheCEOWakesItAfterTenMinutesAndEveryHalfHour(t *testing.T) {
	for _, isAFKOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("AFK mode on: %t", isAFKOn), func(t *testing.T) {
			// Arrange
			s, h, _ := waitingFleet(t, "[]")
			if isAFKOn {
				if _, _, err := afk.TurnOn(h.State, "the board", nil, ceoWaitStarted); err != nil {
					t.Fatal(err)
				}
			}
			if s.afkOn() != isAFKOn {
				t.Fatalf("AFK mode reads on: %t, want %t", s.afkOn(), isAFKOn)
			}
			report(t, h, ceoWaitStarted, "waiting on ceo: which schema do I migrate first?")
			now := ceoWaitStarted

			// Act and Assert
			if early := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(9*time.Minute)); len(early) != 0 {
				t.Fatalf("a wait of nine minutes woke the CEO: %+v", early)
			}
			first := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(10*time.Minute))
			if len(first) != 1 || first[0].Key != waitingGoblin {
				t.Fatalf("a wait of ten minutes = %+v, want one wake keyed by the goblin", first)
			}
			for _, want := range []string{"ceo_wait: Jerry (cg-waits) has waited 10 minutes on the CEO", "waiting on ceo: which schema do I migrate first?", `next: answer it with goblins send cg-waits "<your answer>"`} {
				if !strings.Contains(first[0].Detail, want) {
					t.Errorf("wake %q lacks %q", first[0].Detail, want)
				}
			}
			if held := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(39*time.Minute)); len(held) != 0 {
				t.Fatalf("the same wait woke the CEO again within half an hour: %+v", held)
			}
			again := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(40*time.Minute))
			if len(again) != 1 || !strings.Contains(again[0].Detail, "has waited 40 minutes") {
				t.Fatalf("the wait half an hour on = %+v, want one wake saying it has waited 40 minutes", again)
			}
		})
	}
}

// Each shape of a wait on the CEO wakes it: a wait on the CEO itself, a done
// on a pull request no merge train takes because it is a draft or held, and
// a wait on, or a pause until, a backlog row the CEO owns, named with its
// title, done or not, and any of them made by a goblin whose pause failed
// and that carries on. Every wait that ends by itself or is another's wakes
// nobody, and nor does a goblin whose failed pause took hold.
func TestEachWaitOnTheCEOWakesItAndNoWaitByDesignDoes(t *testing.T) {
	pausedUntil := func(until string) *state.PauseCondition {
		return &state.PauseCondition{Reason: "dependency", Until: until, At: ceoWaitStarted}
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
		{name: "a wait on the CEO", report: "waiting on ceo: is the plan right?", want: []string{"waiting on ceo: is the plan right?"}},
		{name: "a done on a draft", report: donePull209, pulls: pull209(true), want: []string{donePull209 + ", which no merge train takes: it is a draft", "review https://github.com/o/r/pull/209"}},
		{name: "a done on a held pull request", report: donePull209, pulls: pull209(false, "Hold"), want: []string{donePull209 + ", which no merge train takes: it is held: it carries the hold label"}},
		{name: "a wait on a row the CEO owns", report: "waiting on ceo-review: the schema plan first", want: []string{"waiting on ceo-review: the schema plan first; ceo-review is the CEO's own backlog row ceo-review (Review the schema plan)", "finish backlog row ceo-review"}},
		{name: "a pause until a row the CEO owns", report: "working: migrating", pause: pausedUntil("task:ceo-review"), want: []string{"paused until the CEO's own backlog row ceo-review (Review the schema plan) is done", "goblins resume cg-waits"}},
		{name: "a wait on a row the CEO finished", report: "waiting on ceo-plan: the rollout plan first", want: []string{"ceo-plan is the CEO's own backlog row ceo-plan (Approve the rollout plan), done in the backlog", `next: tell cg-waits that ceo-plan is done with goblins send cg-waits "<what>"`}},
		{name: "a pause until a row the CEO finished", report: "working: migrating", pause: pausedUntil("task:ceo-plan"), want: []string{"paused until the CEO's own backlog row ceo-plan (Approve the rollout plan), which is done", "next: resume cg-waits with goblins resume cg-waits"}},
		{name: "a wait on the CEO after a pause that failed", report: "waiting on ceo: is the plan right?", pause: &state.PauseCondition{Reason: "memory", At: ceoWaitStarted}, phase: "failed", isHosted: true, want: []string{"waiting on ceo: is the plan right?"}},
		{name: "a wait on the CEO once a failed pause took hold", report: "waiting on ceo: is the plan right?", pause: &state.PauseCondition{Reason: "memory", At: ceoWaitStarted}, phase: "failed"},
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
			report(t, h, ceoWaitStarted, test.report)
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
			now := ceoWaitStarted

			// Act
			woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(15*time.Minute))

			// Assert
			if len(test.want) == 0 {
				if len(woke) != 0 {
					t.Fatalf("a wait by design woke the CEO: %+v", woke)
				}
				return
			}
			if len(woke) != 1 {
				t.Fatalf("the wait woke the CEO %d times, want once: %+v", len(woke), woke)
			}
			for _, want := range append(test.want, "Jerry (cg-waits) has waited 10 minutes on the CEO") {
				if !strings.Contains(woke[0].Detail, want) {
					t.Errorf("wake %q lacks %q", woke[0].Detail, want)
				}
			}
		})
	}
}

// A wait ends, and wakes nobody again, as soon as the goblin reports
// anything newer, its harness takes a prompt such as the CEO's answer, the
// Overlord pauses it, it is stopped or it is retired; and a goblin that waits
// again later is a new wait, which wakes the CEO ten minutes into it.
func TestAWaitOnTheCEOEndsWhenTheGoblinMovesOn(t *testing.T) {
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
			report(t, h, ceoWaitStarted, "waiting on ceo: which schema do I migrate first?")
			now := ceoWaitStarted
			if woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
				t.Fatalf("the wait woke the CEO %d times at ten minutes, want once", len(woke))
			}

			// Act
			test.end(t, h, now.Add(30*time.Second))
			woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(75*time.Minute))

			// Assert
			if len(woke) != 0 {
				t.Fatalf("a wait that ended woke the CEO: %+v", woke)
			}
			watched, err := readFleetWakes(h.State)
			if err != nil {
				t.Fatal(err)
			}
			for key := range watched.Woke {
				if strings.HasPrefix(key, ceoWaitKeyPrefix) {
					t.Errorf("the ended wait is still remembered as %s", key)
				}
			}
		})
	}
}

// A goblin that waits on the CEO again after a wait ended is a new wait: it
// wakes the CEO ten minutes into it, however lately the last one did.
func TestANewWaitOnTheCEOWakesTenMinutesIntoIt(t *testing.T) {
	// Arrange
	s, h, _ := waitingFleet(t, "[]")
	report(t, h, ceoWaitStarted, "waiting on ceo: which schema do I migrate first?")
	now := ceoWaitStarted
	if woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
		t.Fatalf("the first wait woke the CEO %d times, want once", len(woke))
	}
	report(t, h, ceoWaitStarted.Add(11*time.Minute), "working: on the schema")
	sweepUntil(t, s, h, &now, ceoWaitStarted.Add(12*time.Minute))

	// Act
	report(t, h, ceoWaitStarted.Add(13*time.Minute), "waiting on ceo: may I drop the old column?")
	woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(23*time.Minute))

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
	report(t, h, ceoWaitStarted, donePull209)
	now := ceoWaitStarted
	if woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(10*time.Minute)); len(woke) != 1 {
		t.Fatalf("the held pull request woke the CEO %d times, want once", len(woke))
	}

	// Act
	forge.mu.Lock()
	forge.pulls = pendingPull209
	forge.mu.Unlock()
	woke := sweepUntil(t, s, h, &now, ceoWaitStarted.Add(75*time.Minute))

	// Assert
	if len(woke) != 0 {
		t.Fatalf("a pull request the train may take kept waking the CEO: %+v", woke)
	}
	if waits, err := CEOWaits(h); err != nil || len(waits) != 0 {
		t.Fatalf("CEOWaits = %+v, %v, want none once the hold came off", waits, err)
	}
}

// The digest and the checkpoint read the waits as the sweep does, with what
// each waits for, from the record the CI poll left on disk.
func TestCEOWaitsReadsTheWaitsTheSweepWakesFor(t *testing.T) {
	// Arrange
	s, h, _ := waitingFleet(t, pull209(true))
	report(t, h, ceoWaitStarted, donePull209)
	now := ceoWaitStarted
	sweepUntil(t, s, h, &now, ceoWaitStarted.Add(2*time.Minute))

	// Act
	waits, err := CEOWaits(h)

	// Assert
	if err != nil || len(waits) != 1 {
		t.Fatalf("CEOWaits = %+v, %v, want the one goblin", waits, err)
	}
	if got := waits[0]; got.Who() != "Jerry (cg-waits)" || !got.Since.Equal(ceoWaitStarted) || got.Minutes(now) != 2 || !strings.Contains(got.What, "it is a draft") {
		t.Fatalf("CEOWaits[0] = %+v, want Jerry's wait on its draft since %s", got, ceoWaitStarted)
	}
}

// A wait quotes at most ceoWaitTextWidth bytes of the goblin's report and of
// a backlog row's title, cut short with "..." and never inside a character
// (#1954): each text here holds a two-byte character from its 200th byte on,
// so the quote ends on the 199 bytes before it.
func TestCEOWaitsCutsALongReportOrRowTitleBeforeACharacter(t *testing.T) {
	longReport := "waiting on ceo: " + strings.Repeat("x", 183) + "\u00e9 and the rest"
	longTitle := strings.Repeat("y", 199) + "\u00e9 and the rest"
	for _, test := range []struct {
		name   string
		report string
		want   string
	}{
		{name: "a long report", report: longReport, want: longReport[:199] + "..."},
		{name: "a long row title", report: "waiting on ceo-long: the title first", want: "waiting on ceo-long: the title first; ceo-long is the CEO's own backlog row ceo-long (" + longTitle[:199] + "...)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			_, h, _ := waitingFleet(t, "[]")
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] ceo-long - "+longTitle+" (kind: ceo)\n")
			report(t, h, ceoWaitStarted, test.report)

			// Act
			waits, err := CEOWaits(h)

			// Assert
			if err != nil || len(waits) != 1 {
				t.Fatalf("CEOWaits = %+v, %v, want the one goblin", waits, err)
			}
			if got := waits[0].What; got != test.want || !utf8.ValidString(got) {
				t.Fatalf("CEOWaits[0].What = %q, want %q, valid UTF-8", got, test.want)
			}
		})
	}
}
