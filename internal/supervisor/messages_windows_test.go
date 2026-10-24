package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The Overlord, 2026-10-08: "message queued to chief no matter whats running
// smoothly". A message he types to a goblin on the board is taken at once and
// delivered once, whatever the goblin is doing.

// messageTo queues the Overlord's message to task's goblin as the board
// sends it, and returns the action the store took.
func messageTo(t *testing.T, store *Store, meta state.TaskMeta, id, text string) Action {
	t.Helper()
	queued, err := store.Queue(Action{ID: id, Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: text})
	if err != nil {
		t.Fatalf("the message was refused: %v", err)
	}
	return queued
}

func actionOf(store *Store, id string) Action {
	actions := store.Snapshot().Actions
	return actions[slices.IndexFunc(actions, func(a Action) bool { return a.ID == id })]
}

// atPhase records meta's lifecycle in phase, as its action left it.
func atPhase(t *testing.T, stateDir string, meta state.TaskMeta, action, phase string) {
	t.Helper()
	if err := state.WriteLifecycle(stateDir, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: action + "-1", Action: action, Phase: phase, Updated: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestAMessageToAWorkingGoblinIsTypedIntoItsTerminalOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	// A task of a gated mode reads its branch before typing, so its worktree
	// has to be a repository, and a gate with no run of the branch holds none.
	gitWorktree(t, meta.Worktree)
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "use the staging database")

	// Act
	err := store.ProcessOne(context.Background(), service.execute)
	typed := goblin.waitForLines(t, 1)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(typed) != 1 || !strings.Contains(typed[0], "use the staging database") {
		t.Fatalf("the goblin received %q, want the message once", typed)
	}
	if action := actionOf(store, "message-1"); action.Status != "succeeded" && action.Awaiting == nil {
		t.Fatalf("message = %+v, want it delivered", action)
	}
}

func TestAMessageToAPausedGoblinIsKeptForItsResume(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "pause", "paused")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
	messageTo(t, store, meta, "message-1", "check the flaky test first")
	messageTo(t, store, meta, "message-2", "then rebase")

	// Act
	for range 2 {
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	record, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != "paused" || !strings.Contains(record.ResumeNote, "check the flaky test first") || !strings.Contains(record.ResumeNote, "then rebase") {
		t.Fatalf("record = %+v, want both messages kept for its resume", record)
	}
	for _, id := range []string{"message-1", "message-2"} {
		if action := actionOf(store, id); action.Status != "succeeded" {
			t.Fatalf("%s = %+v, want it taken", id, action)
		}
	}
}

func TestAMessageToAResumingGoblinWaitsAndIsTypedOnceItRuns(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "resume", "resuming")
	// A task of a gated mode reads its branch before typing, so its worktree
	// has to be a repository, and a gate with no run of the branch holds none.
	gitWorktree(t, meta.Worktree)
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "skip the docs")

	// Act
	waiting := store.ProcessOne(context.Background(), service.execute)
	during := actionOf(store, "message-1")
	typedDuring := goblin.lines(t)
	atPhase(t, h.State, meta, "resume", "running")
	time.Sleep(1100 * time.Millisecond)
	ran := store.ProcessOne(context.Background(), service.execute)
	typed := goblin.waitForLines(t, 1)

	// Assert
	if !strings.Contains(during.Message, "Waits for") || during.Status != "queued" || len(typedDuring) != 0 {
		t.Fatalf("while it resumed: %v, message %+v, typed %q, want it waiting with nothing typed", waiting, during, typedDuring)
	}
	if ran != nil || len(typed) != 1 || !strings.Contains(typed[0], "skip the docs") {
		t.Fatalf("once it ran: %v, typed %q, want the message typed once", ran, typed)
	}
}

// A message is kept only for a resume that can run: a failed resume of its
// goblin's own session, or the task's interrupted resume, which a Resume
// takes up again. A resume whose session a later start replaced, as a cfo
// switch run to rescue the goblin does, is neither, and decides nothing of
// where the message goes, so it is typed into the goblin's running terminal
// (issue #1221).
func TestAMessageIsKeptOnlyForAResumeThatCanRun(t *testing.T) {
	for _, tc := range []struct {
		name            string
		phase           string
		generation      string
		resumeOperation string
		isKept          bool
	}{
		{"a failed resume a later start replaced", "failed", "g0", "", false},
		{"a resume cut short that a later start replaced", "resuming", "g0", "", false},
		{"a failed resume of its own session", "failed", "g1", "", true},
		{"the task's interrupted resume", "failed", "g0", "resume-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			meta.ResumeOperation = tc.resumeOperation
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: tc.generation, RequestGeneration: tc.generation, Operation: "resume-1", Action: "resume", Phase: tc.phase, Updated: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			// A task of a gated mode reads its branch before custody is asked
			// about, so its worktree has to be a repository.
			gitWorktree(t, meta.Worktree)
			goblin := goblinTerminal(t, h.State, meta.ID)
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			messageTo(t, store, meta, "message-1", "use the staging database")

			// Act
			err := store.ProcessOne(context.Background(), service.execute)
			var typed []string
			if tc.isKept {
				typed = goblin.lines(t)
			} else {
				typed = goblin.waitForLines(t, 1)
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			record, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			isKept := strings.Contains(record.ResumeNote, "use the staging database")
			action := actionOf(store, "message-1")
			if tc.isKept && (!isKept || len(typed) != 0 || action.Message != "Kept for its resume.") {
				t.Fatalf("message = %+v, resume note %q, typed %q, want it kept for its resume with nothing typed", action, record.ResumeNote, typed)
			}
			if !tc.isKept && (isKept || len(typed) != 1 || !strings.Contains(typed[0], "use the staging database")) {
				t.Fatalf("message = %+v, resume note %q, typed %q, want it typed once into its terminal and not kept", action, record.ResumeNote, typed)
			}
		})
	}
}

// switchSession publishes a new session for meta's task as a cfo switch does
// once it has stopped the old harness: a new generation, and no resume.
func switchSession(t *testing.T, stateDir string, meta state.TaskMeta) {
	t.Helper()
	meta.SpawnGen, meta.ResumeOperation = "g2", ""
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
}

// toldCFO is what task's notify wakes tell the CFO.
func toldCFO(t *testing.T, stateDir, task string) []string {
	t.Helper()
	pending, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var told []string
	for _, entry := range pending {
		if entry.Kind == "notify" && entry.Key == task {
			told = append(told, entry.Detail)
		}
	}
	return told
}

// A message kept for a resume goes to the CFO with its words once a later
// start, such as a cfo switch run to rescue the goblin, replaces the session
// that resume was for, since no resume of that record runs again: one kept
// before the switch, and one kept while it runs, holding the task's metadata
// lock but not its lifecycle lock until it publishes the new session. The
// supervisor's next recovery reading hands each on once, and an answer given
// while paused stays in the note. A message kept for a resume of the current
// session stays kept (issue #1280).
func TestAMessageKeptForAResumeGoesToTheCFOOnceALaterStartReplacesItsSession(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	paused := state.Lifecycle{ID: "task-1", Generation: "g1", RequestGeneration: "g1", Operation: "pause-1", Action: "pause", Phase: "paused", Started: started, Updated: started}
	failed := state.Lifecycle{ID: "task-1", Generation: "g1", RequestGeneration: "g1", Operation: "resume-1", Action: "resume", Phase: "failed", Started: started, Updated: started.Add(3 * time.Minute)}
	answer := "The Overlord answered your question: use the blue one"
	for _, tc := range []struct {
		name     string
		record   state.Lifecycle
		answered string
		when     string
		why      string
	}{
		{name: "kept for a paused goblin before the switch", record: paused, when: "before", why: "while it was paused"},
		{name: "kept for a paused goblin while the switch runs", record: paused, when: "during", why: "while it was paused"},
		{name: "kept for a failed resume before the switch", record: failed, when: "before", why: "a message kept before it began at " + started.Format(time.RFC3339)},
		{name: "kept for a failed resume while the switch runs", record: failed, when: "during", why: "a message kept before it began at " + started.Format(time.RFC3339)},
		{name: "kept after an answer given while paused", record: paused, answered: answer, when: "before", why: "while it was paused"},
		{name: "kept for a paused goblin no later start replaced", record: paused, when: "never"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			record := tc.record
			record.ResumeNote = tc.answered
			if err := state.WriteLifecycle(h.State, record); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}

			// Act
			switch tc.when {
			case "before":
				keptForResume(t, store, meta, "use the staging database")
				switchSession(t, h.State, meta)
			case "during":
				metadataLock := state.MetadataLockName(meta.ID)
				if _, err := lock.AcquireExclusiveNamed(h.State, metadataLock); err != nil {
					t.Fatal(err)
				}
				keptForResume(t, store, meta, "use the staging database")
				switchSession(t, h.State, meta)
				if err := lock.ReleaseExclusiveNamed(h.State, metadataLock); err != nil {
					t.Fatal(err)
				}
			case "never":
				keptForResume(t, store, meta, "use the staging database")
			}
			service.cycle(context.Background(), true)
			service.cycle(context.Background(), true)

			// Assert
			told := toldCFO(t, h.State, meta.ID)
			after, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.when == "never" {
				if len(told) != 0 || after.ResumeNote != fromOverlord("use the staging database") {
					t.Fatalf("the CFO was told %q and the resume note is %q, want the message still kept for its resume", told, after.ResumeNote)
				}
				return
			}
			if len(told) != 1 || !strings.HasPrefix(told[0], "message not delivered: task-1's session was replaced by a later start") || !strings.Contains(told[0], tc.why) || !strings.HasSuffix(told[0], "The Overlord wrote: use the staging database") {
				t.Fatalf("the CFO was told %q, want the message once with its words and %q", told, tc.why)
			}
			if after.ResumeNote != tc.answered || after.Phase != tc.record.Phase || after.Generation != "g1" {
				t.Fatalf("record = %+v, want it as it was with only the message taken out of its note", after)
			}
		})
	}
}

// A pause, resume or stop under way holds the task's lifecycle lock and owns
// its record, so a message kept for a resume whose session a later start
// replaced is left to it: a pause or stop hands it on before it writes its
// own record, and a resume, refused for the replaced session, leaves it for a
// later reading, which hands it on once the lock is free.
func TestAMessageKeptForAReplacedResumeHeldByALifecycleOperationIsLeftToIt(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "pause", "paused")
	keptForResume(t, store, meta, "use the staging database")
	switchSession(t, h.State, meta)
	name := state.LifecycleLockName(meta.ID)
	if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
		t.Fatal(err)
	}

	// Act
	held := handOverReplacedResumes(h.State)
	heldRecord, heldErr := state.ReadLifecycle(h.State, meta.ID)
	heldTold := toldCFO(t, h.State, meta.ID)
	if err := lock.ReleaseExclusiveNamed(h.State, name); err != nil {
		t.Fatal(err)
	}
	freed := handOverReplacedResumes(h.State)

	// Assert
	if held != nil || heldErr != nil || heldRecord.ResumeNote != fromOverlord("use the staging database") || len(heldTold) != 0 {
		t.Errorf("under the lock: %v, record %+v, %v, the CFO told %q; want the message left kept", held, heldRecord, heldErr, heldTold)
	}
	record, err := state.ReadLifecycle(h.State, meta.ID)
	if told := toldCFO(t, h.State, meta.ID); freed != nil || err != nil || record.ResumeNote != "" || len(told) != 1 {
		t.Errorf("once the lock is free: %v, record %+v, %v, the CFO told %q; want the message handed on once", freed, record, err, told)
	}
}

// A pause or stop of the later session hands on what a replaced session's
// record keeps through HandOverReplacedNote before it writes its own record
// over it: the CFO is told each board message once, with its words, and the
// record is left for the caller to overwrite. The current session's record
// keeps its messages for its own resume, so the CFO is told nothing.
func TestHandingOnAReplacedSessionsNoteTellsTheCFOEachMessageOnce(t *testing.T) {
	kept := []string{"use the staging database", "and skip the migration"}
	for _, tc := range []struct {
		name       string
		isReplaced bool
		want       []string
	}{
		{name: "a record whose session a later start replaced", isReplaced: true, want: kept},
		{name: "the current session's record", isReplaced: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			atPhase(t, h.State, meta, "pause", "paused")
			keptForResume(t, store, meta, kept...)
			if tc.isReplaced {
				switchSession(t, h.State, meta)
			}
			current, err := state.ReadTaskMeta(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			record, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			isHanded, err := HandOverReplacedNote(h.State, current, record)

			// Assert
			told := toldCFO(t, h.State, meta.ID)
			if !isHanded || err != nil || len(told) != len(tc.want) {
				t.Fatalf("hand-off = %v, %v, the CFO was told %q, want each of %q once", isHanded, err, told, tc.want)
			}
			for i, words := range tc.want {
				if !strings.HasPrefix(told[i], "message not delivered: task-1's session was replaced by a later start while it was paused") || !strings.HasSuffix(told[i], "The Overlord wrote: "+words) {
					t.Fatalf("the CFO was told %q, want %q with why it was not delivered", told[i], words)
				}
			}
			after, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || after.ResumeNote != fromOverlord(kept[0])+"\n"+fromOverlord(kept[1]) {
				t.Fatalf("resume note = %q, %v, want both messages still in it", after.ResumeNote, err)
			}
		})
	}
}

// A wake episode that fails to publish once the messages a replaced session's
// record keeps are on the CFO's notify wake is said, but the messages leave
// the note all the same, so the next pass tells the CFO nothing more: before,
// each pass told the CFO the same messages again until a publish went
// through.
func TestAReplacedSessionsMessagesGoToTheCFOOnceWhenTheirWakeFailsToPublish(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "pause", "paused")
	keptForResume(t, store, meta, "use the staging database", "and skip the migration")
	switchSession(t, h.State, meta)
	if err := os.Mkdir(filepath.Join(h.State, ".watcher-down"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	first := handOverReplacedResumes(h.State)
	record, readErr := state.ReadLifecycle(h.State, meta.ID)
	second := handOverReplacedResumes(h.State)

	// Assert
	if first == nil || readErr != nil || record.ResumeNote != "" {
		t.Fatalf("first pass = %v, record %+v, %v; want the failed publish said and the messages out of the note", first, record, readErr)
	}
	told := toldCFO(t, h.State, meta.ID)
	if second != nil || len(told) != 2 || !strings.HasSuffix(told[0], "The Overlord wrote: use the staging database") || !strings.HasSuffix(told[1], "The Overlord wrote: and skip the migration") {
		t.Fatalf("second pass = %v, the CFO was told %q, want each message once", second, told)
	}
}

// A Resume of a failed resume whose relaunched terminal is already up
// relaunches nothing, so it hands each board message kept for that resume to
// the CFO, with its words and that resume's start, before it finishes the
// task as running. Before, the messages rode into the running record's note,
// which nothing reads again, so neither the goblin nor the CFO had them (issue
// #1281). A Resume that relaunches carries them in its instruction, so the
// CFO is told nothing.
func TestAResumeOfAFailedResumeAlreadyUpHandsItsKeptMessagesToTheCFO(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	kept := []string{"use the staging database", "and skip the migration"}
	for _, tc := range []struct {
		name        string
		isRunning   bool
		wantCarried []string
		wantTold    []string
	}{
		{name: "its relaunched terminal is already up", isRunning: true, wantTold: kept},
		{name: "its relaunched terminal has ended", isRunning: false, wantCarried: kept},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			meta.ResumeOperation = "resume-1"
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: meta.ResumeOperation, Action: "resume", Phase: "failed", Started: started, Updated: started.Add(3 * time.Minute)}); err != nil {
				t.Fatal(err)
			}
			keptForResume(t, store, meta, kept...)
			var carried []string
			controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				IsRunning: func(context.Context, state.TaskMeta) (bool, error) { return tc.isRunning, nil },
				Memory:    func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil },
				Resume: func(_ context.Context, _ state.TaskMeta, prior state.Lifecycle) error {
					_, carried = keptMessages(prior.ResumeNote)
					return nil
				},
				Notify: func(state.Lifecycle) error { return nil },
				HandOver: func(current state.TaskMeta, prior state.Lifecycle) (bool, error) {
					return HandOverReplacedNote(h.State, current, prior)
				},
				HandOverRelaunched: func(prior state.Lifecycle) (bool, error) {
					return HandOverRelaunchedNote(h.State, prior)
				},
			}}

			// Act
			record, err := controller.Run(context.Background(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-2", Action: "resume", Reason: "overlord"})

			// Assert
			if err != nil || record.Phase != "running" || !slices.Equal(carried, tc.wantCarried) {
				t.Fatalf("resume = %+v, %v, relaunch carried %q; want it running with %q carried", record, err, carried, tc.wantCarried)
			}
			told := toldCFO(t, h.State, meta.ID)
			if len(told) != len(tc.wantTold) {
				t.Fatalf("the CFO was told %q, want each of %q once", told, tc.wantTold)
			}
			for i, words := range tc.wantTold {
				if !strings.HasPrefix(told[i], "message not delivered: task-1's failed resume was finished as running because a Resume found the terminal it started already up") || !strings.Contains(told[i], "a message kept before that resume began at "+started.Format(time.RFC3339)) || !strings.HasSuffix(told[i], "The Overlord wrote: "+words) {
					t.Fatalf("the CFO was told %q, want %q with why it was not delivered and the resume's start", told[i], words)
				}
			}
		})
	}
}

// A stop of a paused goblin, or of one whose resume failed, ends it before the
// resume its record keeps board messages for, so each goes to the CFO with its
// words and why before the stop writes its own record, which keeps no note,
// over the one that kept them; a failed resume's also names that resume's
// start. Before, the stop's record dropped them, and they reached neither the
// goblin nor the CFO (issue #1317).
func TestAStopOfAPausedGoblinHandsItsKeptMessagesToTheCFO(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	kept := []string{"use the staging database", "and skip the migration"}
	for _, tc := range []struct {
		name   string
		action string
		phase  string
		why    string
	}{
		{name: "a paused goblin", action: "pause", phase: "paused", why: "message not delivered: task-1 was stopped while it was paused, so the resume the message was kept for never runs"},
		{name: "a goblin whose resume failed", action: "resume", phase: "failed", why: "message not delivered: task-1 was stopped after its resume failed, so that resume never runs again; a message kept before it began at " + started.Format(time.RFC3339) + " went with it, and one kept after it failed did not"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: tc.action + "-1", Action: tc.action, Phase: tc.phase, Started: started, Updated: started.Add(3 * time.Minute)}); err != nil {
				t.Fatal(err)
			}
			keptForResume(t, store, meta, kept...)
			controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Stop: func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
				Archive: func(context.Context, state.TaskMeta, *state.Lifecycle) (lifecycle.Preservation, error) {
					return lifecycle.Preservation{}, nil
				},
				Notify: func(state.Lifecycle) error { return nil },
				HandOver: func(current state.TaskMeta, prior state.Lifecycle) (bool, error) {
					return HandOverReplacedNote(h.State, current, prior)
				},
				HandOverEnded: func(current state.TaskMeta, prior state.Lifecycle, action string) (bool, error) {
					return HandOverEndedNote(h.State, current, prior, action)
				},
			}}

			// Act
			record, err := controller.Run(context.Background(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "stop-1", Action: "stop", Reason: "overlord"})

			// Assert
			if err != nil || record.Phase != "stopped" {
				t.Fatalf("stop = %+v, %v; want it stopped", record, err)
			}
			told := toldCFO(t, h.State, meta.ID)
			if len(told) != len(kept) {
				t.Fatalf("the CFO was told %q, want each of %q once", told, kept)
			}
			for i, words := range kept {
				if !strings.HasPrefix(told[i], tc.why) || !strings.HasSuffix(told[i], "The Overlord wrote: "+words) {
					t.Fatalf("the CFO was told %q, want %q with why it was not delivered: %q", told[i], words, tc.why)
				}
			}
		})
	}
}

// A pause of a goblin whose resume failed writes its own record over the failed
// resume's, so that resume never runs again: each board message kept for it
// goes to the CFO with its words, why and that resume's start before the
// pause's record replaces it. Before, the pause's record kept no note and its
// later resume told the goblin nothing of them (issue #1382).
func TestAPauseOfAGoblinWhoseResumeFailedHandsItsKeptMessagesToTheCFO(t *testing.T) {
	// Arrange
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	kept := []string{"use the staging database", "and skip the migration"}
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	meta.ResumeOperation = "resume-1"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: meta.ResumeOperation, Action: "resume", Phase: "failed", Started: started, Updated: started.Add(3 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	keptForResume(t, store, meta, kept...)
	controller := lifecycle.Service{StateDir: h.State, PrepareWait: 10 * time.Millisecond, PauseWait: 10 * time.Millisecond, Operations: lifecycle.Operations{
		Prepare: func(context.Context, state.TaskMeta, string) error { return nil },
		Stop:    func(context.Context, state.TaskMeta, *state.Lifecycle) ([]string, error) { return nil, nil },
		Notify:  func(state.Lifecycle) error { return nil },
		HandOver: func(current state.TaskMeta, prior state.Lifecycle) (bool, error) {
			return HandOverReplacedNote(h.State, current, prior)
		},
		HandOverEnded: func(current state.TaskMeta, prior state.Lifecycle, action string) (bool, error) {
			return HandOverEndedNote(h.State, current, prior, action)
		},
	}}

	// Act
	record, err := controller.Run(context.Background(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Reason: "memory"})

	// Assert
	if err != nil || record.Phase != "paused" {
		t.Fatalf("pause = %+v, %v; want it paused", record, err)
	}
	told := toldCFO(t, h.State, meta.ID)
	if len(told) != len(kept) {
		t.Fatalf("the CFO was told %q, want each of %q once", told, kept)
	}
	why := "message not delivered: task-1 was paused after its resume failed, so that resume never runs again; a message kept before it began at " + started.Format(time.RFC3339) + " went with it, and one kept after it failed did not"
	for i, words := range kept {
		if !strings.HasPrefix(told[i], why) || !strings.HasSuffix(told[i], "The Overlord wrote: "+words) {
			t.Fatalf("the CFO was told %q, want %q with why it was not delivered: %q", told[i], words, why)
		}
	}
}

// A cleanup that retires a goblin before the resume its record keeps board
// messages for hands each to the CFO with its words and why, and takes it out
// of the note, so it is handed on once and what the note held before it, such
// as an answer given while paused, stays: a paused goblin's, a failed
// resume's, which also names that resume's start, and a record whose session a
// later start replaced, as that is handed on. Before, the messages stayed in a
// record no reading looks at once the task is retired (issue #1317).
func TestRetiringAGoblinHandsTheMessagesKeptForItsResumeToTheCFO(t *testing.T) {
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	kept := []string{"use the staging database", "and skip the migration"}
	answer := "Answered while paused: ship it."
	for _, tc := range []struct {
		name       string
		action     string
		phase      string
		isReplaced bool
		why        string
	}{
		{name: "a paused goblin", action: "pause", phase: "paused", why: "message not delivered: task-1 was retired by a cleanup while it was paused, so the resume the message was kept for never runs"},
		{name: "a goblin whose resume failed", action: "resume", phase: "failed", why: "message not delivered: task-1 was retired by a cleanup after its resume failed, so that resume never runs again; a message kept before it began at " + started.Format(time.RFC3339) + " went with it, and one kept after it failed did not"},
		{name: "a record whose session a later start replaced", action: "pause", phase: "paused", isReplaced: true, why: "message not delivered: task-1's session was replaced by a later start while it was paused, so the resume the message was kept for never runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: tc.action + "-1", Action: tc.action, Phase: tc.phase, Started: started, Updated: started.Add(3 * time.Minute), ResumeNote: answer}); err != nil {
				t.Fatal(err)
			}
			keptForResume(t, store, meta, kept...)
			if tc.isReplaced {
				switchSession(t, h.State, meta)
			}
			current, err := state.ReadTaskMeta(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			isHanded, err := HandOverRetiredNote(h.State, current)
			again, againErr := HandOverRetiredNote(h.State, current)

			// Assert
			if !isHanded || err != nil || !again || againErr != nil {
				t.Fatalf("hand-offs = %v, %v and %v, %v; want both to hand on with nothing met", isHanded, err, again, againErr)
			}
			told := toldCFO(t, h.State, meta.ID)
			if len(told) != len(kept) {
				t.Fatalf("the CFO was told %q, want each of %q once", told, kept)
			}
			for i, words := range kept {
				if !strings.HasPrefix(told[i], tc.why) || !strings.HasSuffix(told[i], "The Overlord wrote: "+words) {
					t.Fatalf("the CFO was told %q, want %q with why it was not delivered: %q", told[i], words, tc.why)
				}
			}
			after, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil || after.ResumeNote != answer || after.Phase != tc.phase {
				t.Fatalf("record = %+v, %v; want it %s with only %q left in its note", after, err, tc.phase, answer)
			}
		})
	}
}

// A cleanup retires a goblin whose lifecycle record cannot be read, a force
// archive's above all, with a warning that no message kept for its resume was
// handed to the CFO, and leaves the record as it was. Before, such a record
// refused every cleanup, so the task could not be retired without editing its
// state by hand.
func TestRetiringAGoblinWhoseRecordCannotBeReadGoesOnWithAWarning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record string
	}{
		{name: "a malformed record", record: "{"},
		{name: "another task's record", record: `{"id":"task-2"}`},
		{name: "a record of no known action", record: `{"id":"task-1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			_, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			path := state.LifecyclePath(h.State, meta.ID)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.record), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			isRetirable, err := HandOverRetiredNote(h.State, meta)

			// Assert
			warning := "the lifecycle record of task-1 cannot be read, so no message kept for its resume was handed to the CFO: "
			if !isRetirable || err == nil || !strings.HasPrefix(err.Error(), warning) {
				t.Fatalf("hand-off = %v, %v; want the cleanup to go on with a warning that starts %q", isRetirable, err, warning)
			}
			if told := toldCFO(t, h.State, meta.ID); len(told) != 0 {
				t.Fatalf("the CFO was told %q, want nothing", told)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != tc.record {
				t.Fatalf("record = %q, %v; want it as it was, %q", after, err, tc.record)
			}
		})
	}
}

// A cleanup goes on once the messages a retired goblin's record keeps are on
// the CFO's notify wake, though their wake episode then fails to publish, with
// a warning that says they went to the CFO and what the publish met.
func TestRetiringAGoblinSaysItsKeptMessagesWentToTheCFOWhenTheirWakeFailsToPublish(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	atPhase(t, h.State, meta, "pause", "paused")
	keptForResume(t, store, meta, "use the staging database")
	if err := os.Mkdir(filepath.Join(h.State, ".watcher-down"), 0o700); err != nil {
		t.Fatal(err)
	}

	// Act
	isRetirable, err := HandOverRetiredNote(h.State, meta)

	// Assert
	warning := "the messages kept for its resume went to the CFO, but "
	if !isRetirable || err == nil || !strings.HasPrefix(err.Error(), warning) {
		t.Fatalf("hand-off = %v, %v; want the cleanup to go on with a warning that starts %q", isRetirable, err, warning)
	}
	if told := toldCFO(t, h.State, meta.ID); len(told) != 1 || !strings.HasSuffix(told[0], "The Overlord wrote: use the staging database") {
		t.Fatalf("the CFO was told %q, want the message once", told)
	}
}

// keptForResume has the supervisor keep the Overlord's messages to meta's
// paused goblin for its resume, in order.
func keptForResume(t *testing.T, store *Store, meta state.TaskMeta, texts ...string) {
	t.Helper()
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: store.Home.State}}}
	for _, text := range texts {
		id := fmt.Sprintf("message-%d", len(store.Snapshot().Actions)+1)
		messageTo(t, store, meta, id, text)
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		if action := actionOf(store, id); action.Message != "Kept for its resume." {
			t.Fatalf("%s = %+v, want it kept for its resume", id, action)
		}
	}
}

// deleteMessage presses the delete button of a message the board shows kept
// for task's resume, which asks the action API, with the board's token, to
// withdraw it.
func deleteMessage(store *Store, task, id, text string) *httptest.ResponseRecorder {
	handler := NewHTTP(&Service{Store: store, Instance: "instance-1"}, "board.local", nil)
	return boardRequest(handler, "POST", "/api/actions", fmt.Sprintf(`{"id":%q,"kind":"message_withdraw","task_id":%q,"generation":"","text":%q}`, id, task, text))
}

// keptOnCard is what the board's card for task says its resume will carry.
func keptOnCard(t *testing.T, store *Store, task string) []string {
	t.Helper()
	snapshot, err := (&Service{Store: store}).Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(each Task) bool { return each.ID == task })
	if index < 0 || snapshot.Tasks[index].Lifecycle == nil {
		t.Fatalf("tasks = %+v, want %s's card with its lifecycle", snapshot.Tasks, task)
	}
	return snapshot.Tasks[index].Lifecycle.KeptMessages
}

// The Overlord, 2026-10-09, of Bernie's paused panel: "why is there no delete
// button for paused queued messages?". Deleting a message kept for a paused
// goblin's resume withdraws it from the resume note, so the resume never
// carries it, and leaves the rest of the note as it was.
func TestDeletingAKeptMessageWithdrawsItFromTheResume(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answered string
		kept     []string
		deleted  string
		left     []string
	}{
		{"the only message", "", []string{"i can message?"}, "i can message?", nil},
		{"one of two", "", []string{"i can message?", "then rebase"}, "i can message?", []string{"then rebase"}},
		{"one of two with the same words", "", []string{"go on", "go on"}, "go on", []string{"go on"}},
		{"one whose words open another's", "", []string{"go on\nand rebase", "go on"}, "go on", []string{"go on\nand rebase"}},
		{"one after an answer given while paused", "Use the staging database.", []string{"i can message?"}, "i can message?", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			atPhase(t, h.State, meta, "pause", "paused")
			if tc.answered != "" {
				record, err := state.ReadLifecycle(h.State, meta.ID)
				if err != nil {
					t.Fatal(err)
				}
				record.ResumeNote = tc.answered
				if err := state.WriteLifecycle(h.State, record); err != nil {
					t.Fatal(err)
				}
			}
			keptForResume(t, store, meta, tc.kept...)
			before := keptOnCard(t, store, meta.ID)

			// Act
			response := deleteMessage(store, meta.ID, "delete-1", tc.deleted)
			retried := deleteMessage(store, meta.ID, "delete-1", tc.deleted)

			// Assert
			if response.Code != http.StatusAccepted || retried.Code != http.StatusAccepted {
				t.Fatalf("delete = %d %s, retried = %d %s, want both accepted", response.Code, response.Body, retried.Code, retried.Body)
			}
			if after := keptOnCard(t, store, meta.ID); !slices.Equal(before, tc.kept) || !slices.Equal(after, tc.left) {
				t.Fatalf("kept on the card before %q and after %q, want %q then %q", before, after, tc.kept, tc.left)
			}
			record, err := state.ReadLifecycle(h.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{}
			if tc.answered != "" {
				want = append(want, tc.answered)
			}
			for _, text := range tc.left {
				want = append(want, fromOverlord(text))
			}
			if record.ResumeNote != strings.Join(want, "\n") {
				t.Fatalf("resume note = %q, want %q", record.ResumeNote, strings.Join(want, "\n"))
			}
		})
	}
}

// A message the goblin already has cannot be deleted: one typed into its
// terminal, or one its resume carried. Neither can one never kept.
func TestDeletingAMessageIsRefusedOnceItWasDeliveredOrNeverKept(t *testing.T) {
	for _, tc := range []struct {
		name    string
		deliver func(t *testing.T, store *Store, meta state.TaskMeta) (deleted string)
	}{
		{"typed into its terminal", func(t *testing.T, store *Store, meta state.TaskMeta) string {
			goblin := goblinTerminal(t, store.Home.State, meta.ID)
			messageTo(t, store, meta, "message-1", "use the staging database")
			if err := store.ProcessOne(context.Background(), (&Service{Store: store, Options: Options{CFO: &CFOConnection{State: store.Home.State}}}).execute); err != nil {
				t.Fatal(err)
			}
			goblin.waitForLines(t, 1)
			return "use the staging database"
		}},
		{"carried by its resume", func(t *testing.T, store *Store, meta state.TaskMeta) string {
			atPhase(t, store.Home.State, meta, "pause", "paused")
			keptForResume(t, store, meta, "check the flaky test first")
			resumed, err := state.ReadLifecycle(store.Home.State, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			resumed.Action, resumed.Phase = "resume", "running"
			if err := state.WriteLifecycle(store.Home.State, resumed); err != nil {
				t.Fatal(err)
			}
			return "check the flaky test first"
		}},
		{"never kept", func(t *testing.T, store *Store, meta state.TaskMeta) string {
			atPhase(t, store.Home.State, meta, "pause", "paused")
			keptForResume(t, store, meta, "check the flaky test first")
			return "check the flaky"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			deleted := tc.deliver(t, store, meta)
			before, beforeErr := state.ReadLifecycle(h.State, meta.ID)
			actions := len(store.Snapshot().Actions)

			// Act
			response := deleteMessage(store, meta.ID, "delete-1", deleted)

			// Assert
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "task-1 has no such message kept for its resume") {
				t.Fatalf("delete = %d %s, want it refused as not kept", response.Code, response.Body)
			}
			if after := len(store.Snapshot().Actions); after != actions {
				t.Fatalf("actions went from %d to %d, want none taken", actions, after)
			}
			if after, err := state.ReadLifecycle(h.State, meta.ID); beforeErr == nil && (err != nil || after.ResumeNote != before.ResumeNote) {
				t.Fatalf("resume note = %q, %v, want %q as it was", after.ResumeNote, err, before.ResumeNote)
			}
		})
	}
}

func TestAMessageToTheCFOWaitsWhileNoCFORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)

	// Act
	queued, err := store.Queue(Action{ID: "message-1", Kind: "message", Text: "hello"})

	// Assert
	if err != nil || queued.Status != "queued" {
		t.Fatalf("message = %+v, %v, want it taken", queued, err)
	}
	if store.HasRunnable() {
		t.Fatal("a message to the CFO runs while no CFO runs")
	}
}

// gitWorktree makes dir a repository on a branch, as a task's worktree is.
func gitWorktree(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q", "-b", "feat/work"}, {"commit", "-q", "--allow-empty", "-m", "root"}} {
		command := exec.Command("git", args...)
		command.Dir = dir
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
}

// gateFake is a gate whose custody answer the test sets, and which says
// whether its custody was read at all.
type gateFake struct {
	err  error
	asks int
}

func (g *gateFake) Progress(context.Context, string, string) (pipeline.Progress, error) {
	return pipeline.Progress{}, nil
}

func (g *gateFake) CanSteer(context.Context, string, string, string) error {
	g.asks++
	return g.err
}

// gatedGoblin is a no-mistakes task whose worktree is a real repository.
func gatedGoblin(t *testing.T, h home.Home) state.TaskMeta {
	t.Helper()
	meta := treeGoblin(t, h.State, t.TempDir(), "gated", false)
	meta.Mode, meta.Harness = "no-mistakes", "pi"
	gitWorktree(t, meta.Worktree)
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// A message for a goblin whose gate owns its branch is queued only once that
// custody was actually read and refused, and it then reaches neither the gate
// nor the CFO. Once the gate lets go it is no longer queued for the gate, and
// the queue holds the one message throughout.
func TestAGateHeldMessageIsQueuedAfterCustodyIsReadAndDeliveredAfterRelease(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	gate := &gateFake{err: pipeline.ErrCustodyHeld}
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: gate}}
	action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "take this after the gate", Status: "queued", CreatedAt: time.Now().Add(-2 * messageWait)}
	store.db.Actions = append(store.db.Actions, action)

	// Act: while the gate holds it, with a message older than the ordinary
	// wait, which would otherwise be the CFO's.
	held, err := service.deliverMessage(context.Background(), action)

	// Assert
	if gate.asks == 0 {
		t.Fatal("custody was never read, so nothing about a hold is proved")
	}
	if !errors.Is(err, ErrDeferred) || !strings.Contains(held.Reason, "Queued for "+meta.ID) {
		t.Fatalf("a gate-held message came back as %v %v", held, err)
	}
	if strings.Contains(held.Reason, "CFO") {
		t.Fatalf("a gate-held message was handed to the CFO: %q", held.Reason)
	}

	// Act: the gate lets go, and the same queued action is tried again.
	gate.err = nil
	released, err := service.deliverMessage(context.Background(), action)
	if gate.asks < 2 {
		t.Fatalf("custody was read %d times, want it asked again after release", gate.asks)
	}

	// Assert: it is no longer queued for the gate and gone to nobody for its
	// age, and the queue still holds exactly the one message.
	if strings.Contains(released.Reason, "Queued for "+meta.ID) || strings.Contains(released.Reason, "CFO") {
		t.Fatalf("a released message reads %q %v, want it waiting for its goblin", released.Reason, err)
	}
	waiting := 0
	for _, each := range store.db.Actions {
		if each.Kind == "message" && each.TaskID == meta.ID {
			waiting++
		}
	}
	if waiting != 1 {
		t.Fatalf("the queue holds %d copies of the message, want exactly one", waiting)
	}
}

// A custody nobody could read is not a hold: nothing is typed, and the board
// says so in words he can act on rather than claiming the gate has it.
func TestACustodyThatCannotBeReadQueuesAndTypesNothing(t *testing.T) {
	for _, test := range []struct {
		name string
		gate ProgressReader
	}{
		{"a gate that cannot answer", &gateFake{err: errors.New("the no-mistakes database cannot be read")}},
		{"a home with no gate", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := gatedGoblin(t, h)
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: test.gate}}
			action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "take this", Status: "queued", CreatedAt: time.Now()}

			// Act
			evaluation, err := service.deliverMessage(context.Background(), action)

			// Assert
			// It waits in the queue rather than being typed, so nothing can
			// break a gate, and it says the custody could not be read rather
			// than claiming the gate owns the branch.
			if !errors.Is(err, ErrDeferred) {
				t.Fatalf("an unreadable custody came back as %v %v, want it queued", evaluation, err)
			}
			if !strings.Contains(evaluation.Reason, "Delivery waits until its gate custody can be read") {
				t.Fatalf("the queue state reads %q", evaluation.Reason)
			}
			if strings.Contains(evaluation.Reason, "holds the branch") {
				t.Fatalf("an unreadable custody was reported as a hold: %q", evaluation.Reason)
			}
		})
	}
}

// A message waiting for a gate to let go is withdrawn from that queue, so no
// later attempt delivers it.
func TestAQueuedMessageForAGateHeldGoblinIsWithdrawn(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", TaskID: "gated", Text: "take this after the gate", Status: "queued", CreatedAt: time.Now().UTC()})

	// Act
	withdrawn, err := store.withdrawMessage(Action{ID: "m2", Kind: "message_withdraw", TaskID: "gated", Text: "take this after the gate"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.Status != "succeeded" || !strings.Contains(withdrawn.Message, "gate let go") {
		t.Fatalf("the withdrawal reads %+v", withdrawn)
	}
	for _, left := range store.db.Actions {
		if left.Kind == "message" && left.Status == "queued" && left.TaskID == "gated" {
			t.Fatalf("the withdrawn message is still queued: %+v", left)
		}
	}
}

// A goblin whose gate holds nothing keeps the ordinary handoff: a message its
// terminal cannot take goes to the CFO once the ordinary wait passes, which is
// what a queue that never expired had stopped doing.
func TestAClearCustodyWithoutATerminalKeepsTheOrdinaryHandoff(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}
	action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "too late", Status: "queued", CreatedAt: time.Now().Add(-2 * messageWait)}

	// Act
	evaluation, err := service.deliverMessage(context.Background(), action)

	// Assert
	if !strings.Contains(evaluation.Reason, "CFO") && err == nil {
		t.Fatalf("a message past the ordinary wait for a goblin with no terminal reads %q, want the ordinary handoff", evaluation.Reason)
	}
}

// A withdrawal at a full queue is not refused when it also frees the slot it
// took, since removing the queued message and keeping its receipt does not
// grow the queue.
func TestAWithdrawalAtAFullQueueStillWithdrawsAQueuedMessage(t *testing.T) {
	// Arrange: the queue at its limit, with nothing closed to reuse.
	store, _ := testStore(t)
	store.db.Actions = nil
	for i := 0; i < maxActions; i++ {
		store.db.Actions = append(store.db.Actions, Action{ID: "f" + strconv.Itoa(i), Kind: "message", TaskID: "other-" + strconv.Itoa(i), Text: "filler", Status: "queued"})
	}
	store.db.Actions[0] = Action{ID: "m1", Kind: "message", TaskID: "gated", Text: "take this after the gate", Status: "queued"}

	// Act
	withdrawn, err := store.withdrawMessage(Action{ID: "m2", Kind: "message_withdraw", TaskID: "gated", Text: "take this after the gate"})

	// Assert
	if err != nil {
		t.Fatalf("a withdrawal at a full queue was refused: %v", err)
	}
	if withdrawn.Status != "succeeded" {
		t.Fatalf("the withdrawal reads %+v", withdrawn)
	}
	if len(store.db.Actions) > maxActions {
		t.Fatalf("the queue grew past its limit to %d", len(store.db.Actions))
	}
}

// A message held by a gate keeps waiting across a restart: while a start is on
// its way its terminal does not run yet, and the message must not take the
// ordinary handoff just because the task is starting.
func TestAHeldMessageKeepsWaitingWhileTheGoblinIsStarting(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	gate := &gateFake{err: pipeline.ErrCustodyHeld}
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: gate}, starting: meta.ID}
	action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "hold this", Status: "queued", CreatedAt: time.Now().Add(-2 * messageWait)}

	// Act
	evaluation, err := service.deliverMessage(context.Background(), action)

	// Assert
	if gate.asks == 0 {
		t.Fatal("custody was never read for a task that is starting")
	}
	if !errors.Is(err, ErrDeferred) {
		t.Fatalf("a held message for a starting goblin came back as %v %v, want it still waiting", evaluation, err)
	}
	if strings.Contains(evaluation.Reason, "CFO") {
		t.Fatalf("a held message for a starting goblin was handed to the CFO: %q", evaluation.Reason)
	}
}

// A message held by a gate survives a restart, is delivered to exactly one
// recipient once the gate lets go, and one withdrawn first is never delivered.
func TestAHeldMessageSurvivesARestartAndIsDeliveredOnce(t *testing.T) {
	// Arrange: a queued message for a gated goblin, and the store reopened as
	// a restart opens it.
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	held, err := store.Queue(Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "deliver me once"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	// Act: the reopened store holds it.
	after := actionOf(reopened, held.ID)

	// Assert
	if after.ID != held.ID || after.Status != "queued" || after.Text != "deliver me once" {
		t.Fatalf("after a restart the message reads %+v, want it still queued", after)
	}

	// Act: the gate lets go, and the goblin's terminal takes it once.
	gate := &gateFake{}
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: reopened, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: gate}}
	first := reopened.ProcessOne(context.Background(), service.execute)
	typedOnce := goblin.waitForLines(t, 1)
	second := reopened.ProcessOne(context.Background(), service.execute)
	typedTwice := goblin.lines(t)

	// Assert: one recipient, one delivery.
	if first != nil || second != nil {
		t.Fatalf("the delivery ended %v and then %v", first, second)
	}
	if len(typedOnce) != 1 || !strings.Contains(typedOnce[0], "deliver me once") {
		t.Fatalf("the goblin received %q, want the message once", typedOnce)
	}
	if len(typedTwice) != 1 {
		t.Fatalf("the goblin received %d lines, want exactly one delivery", len(typedTwice))
	}

	// Act and Assert: a second message withdrawn before the gate lets go is
	// never delivered.
	withdrawn, err := reopened.Queue(Action{ID: "m2", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "never deliver me"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.withdrawMessage(Action{ID: "m3", Kind: "message_withdraw", TaskID: meta.ID, Text: withdrawn.Text}); err != nil {
		t.Fatalf("the withdrawal was refused: %v", err)
	}
	if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	for _, said := range goblin.lines(t) {
		if strings.Contains(said, "never deliver me") {
			t.Fatalf("a withdrawn message was delivered: %q", said)
		}
	}
}

// Custody that cannot be read across a restart keeps its message: the reopened
// store still holds it, and it is delivered exactly once when custody reads
// clear, while a withdrawn one is never delivered.
func TestAnUnreadableCustodyKeepsItsMessageAcrossARestart(t *testing.T) {
	// Arrange: a gated goblin whose custody check fails, and a queued message.
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	queued, err := store.Queue(Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "wait for clear custody"})
	if err != nil {
		t.Fatal(err)
	}
	unreadable := &gateFake{err: errors.New("the no-mistakes database cannot be read")}
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: unreadable}}
	action := actionOf(store, queued.ID)
	action.CreatedAt = time.Now().Add(-2 * messageWait)

	// Act: the check fails, so the message waits even past the ordinary limit.
	var evaluation Evaluation
	evaluation, err = service.deliverMessage(context.Background(), action)

	// Assert
	if !errors.Is(err, ErrDeferred) || !strings.Contains(evaluation.Reason, "Delivery waits until its gate custody can be read") {
		t.Fatalf("an unreadable custody came back as %v %v, want it queued in plain words", evaluation, err)
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if after := actionOf(reopened, queued.ID); after.Status != "queued" {
		t.Fatalf("after a restart the message reads %+v, want it still queued", after)
	}

	// Act: custody reads clear, and the goblin's terminal takes it once.
	goblin := goblinTerminal(t, h.State, meta.ID)
	clear := &Service{Store: reopened, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}
	if err := reopened.ProcessOne(context.Background(), clear.execute); err != nil {
		t.Fatal(err)
	}
	typed := goblin.waitForLines(t, 1)
	if len(typed) != 1 || !strings.Contains(typed[0], "wait for clear custody") {
		t.Fatalf("the goblin received %q, want it once", typed)
	}
	if err := reopened.ProcessOne(context.Background(), clear.execute); err != nil {
		t.Fatal(err)
	}

	// Assert: exactly one delivery.
	if lines := goblin.lines(t); len(lines) != 1 {
		t.Fatalf("the goblin received %d lines, want exactly one delivery", len(lines))
	}
}
