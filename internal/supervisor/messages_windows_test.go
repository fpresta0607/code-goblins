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

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/routing"
	"github.com/fpresta0607/code-goblins/internal/spawn"
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
	for _, harness := range []string{"claude", "codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			meta.Harness = harness
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
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
		})
	}
}

func TestAMessageRefusedBeforeTypingStaysQueuedUntilTheComposerReturns(t *testing.T) {
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "keep this through the picker")
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	typeIntoGoblin = func(context.Context, string, state.TaskMeta, string, bool, bool) error { return fleet.ErrNotTyped }

	err := store.ProcessOne(t.Context(), service.execute)
	if !errors.Is(err, ErrDeferred) || actionOf(store, "message-1").Status != "queued" {
		t.Fatalf("delivery = %v, action = %+v, want durable queue", err, actionOf(store, "message-1"))
	}
	var received []string
	typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string, _, _ bool) error {
		received = append(received, text)
		return nil
	}
	store.deferredUntil["message-1"] = time.Now().Add(-time.Second)
	if err := store.ProcessOne(t.Context(), service.execute); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || !strings.Contains(received[0], "keep this through the picker") || actionOf(store, "message-1").Status != "succeeded" {
		t.Fatalf("delivered %q, action = %+v, want the saved message once", received, actionOf(store, "message-1"))
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
			// A no-mistakes goblin works in a real repository, so its gate
			// can read the branch it is asked about.
			gitWorktree(t, meta.Worktree)
			goblin := goblinTerminal(t, store.Home.State, meta.ID)
			messageTo(t, store, meta, "message-1", "use the staging database")
			// A gate that answers clear, as a live supervisor's does: custody
			// nobody can read queues the message instead of typing it.
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: store.Home.State}, Gate: &gateFake{}}}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
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
	// The identity is named here, as the other fixtures do: a CI runner has no
	// global git user, so a commit that relies on one dies at setup.
	for _, args := range [][]string{
		{"init", "-q", "-b", "feat/work"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "root"},
	} {
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
	// Arrange: a gated goblin whose custody check cannot be read, a message it
	// keeps, and a pause already under way.
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	queued, err := store.Queue(Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "wait for clear custody"})
	if err != nil {
		t.Fatal(err)
	}
	// The age is written to the store itself, so the restart reads the message
	// it must keep rather than one this test aged in memory.
	store.mu.Lock()
	for i := range store.db.Actions {
		if store.db.Actions[i].ID == queued.ID {
			store.db.Actions[i].CreatedAt = time.Now().Add(-2 * messageWait)
		}
	}
	store.mu.Unlock()
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	atPhase(t, h.State, meta, "pause", "pausing")
	unreadable := &gateFake{err: errors.New("the no-mistakes database cannot be read")}

	// Act: the store is reopened, as a restart opens it, and the pause's own
	// attempt runs with custody still unreadable.
	reopened, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if after := actionOf(reopened, queued.ID); !after.CreatedAt.Equal(store.Snapshot().Actions[0].CreatedAt) {
		t.Fatalf("the reopened message reads %v, want its saved age", after.CreatedAt)
	}
	paused := &Service{Store: reopened, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: unreadable}}
	// A deferred message answers ErrDeferred, which is the queue working, not a
	// failure of the pass.
	if err := reopened.ProcessOne(context.Background(), paused.execute); err != nil && !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}

	// Assert: it waited, though it is past the ordinary limit, and stayed queued.
	waited := actionOf(reopened, queued.ID)
	if waited.Status != "queued" {
		t.Fatalf("after a restart an unreadable custody reads %+v, want it still queued", waited)
	}
	if !strings.Contains(waited.Message, "Waits for") && !strings.Contains(waited.Message, "gate custody can be read") {
		t.Fatalf("the queued message reads %q, want it waiting rather than handed on", waited.Message)
	}

	// Act: a withdrawal while it still waits, then custody reads clear.
	if _, err := reopened.withdrawMessage(Action{ID: "m2", Kind: "message_withdraw", TaskID: meta.ID, Text: queued.Text}); err != nil {
		t.Fatalf("the withdrawal was refused: %v", err)
	}
	goblin := goblinTerminal(t, h.State, meta.ID)
	// The pause has ended, so the goblin's terminal takes typing again.
	atPhase(t, h.State, meta, "resume", "running")
	clear := &Service{Store: reopened, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}
	if err := reopened.ProcessOne(context.Background(), clear.execute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	// Assert: nothing was delivered, since the only message was withdrawn.
	if lines := goblin.lines(t); len(lines) != 0 {
		t.Fatalf("the goblin received %q, want nothing after a withdrawal", lines)
	}

	// Act and Assert: a message kept through the same unreadable custody is
	// delivered exactly once once custody reads clear.
	second, err := reopened.Queue(Action{ID: "m3", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "deliver me once"})
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.ProcessOne(context.Background(), paused.execute); err != nil && !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}
	if kept := actionOf(reopened, second.ID); kept.Status != "queued" {
		t.Fatalf("the second message reads %+v, want it queued while custody is unreadable", kept)
	}
	// Each pass takes one queued action, and a deferred one waits a second
	// before the next try, so the queue is drained with that second between.
	for pass := 0; pass < 4 && len(goblin.lines(t)) == 0; pass++ {
		if err := reopened.ProcessOne(context.Background(), clear.execute); err != nil && !errors.Is(err, ErrDeferred) {
			t.Fatal(err)
		}
		time.Sleep(1200 * time.Millisecond)
	}
	delivered := goblin.waitForLines(t, 1)
	if len(delivered) != 1 || !strings.Contains(delivered[0], "deliver me once") {
		t.Fatalf("the goblin received %q, want the second message once", delivered)
	}
	if err := reopened.ProcessOne(context.Background(), clear.execute); err != nil {
		t.Fatal(err)
	}
	if lines := goblin.lines(t); len(lines) != 1 {
		t.Fatalf("the goblin received %d lines, want exactly one delivery", len(lines))
	}
}

// A message kept before a pause, resume or stop survives every one of those
// transitions while the gate owns the branch or its custody cannot be read: it
// stays queued past the ordinary wait and is never handed to the CFO, and it
// keeps waiting once the transition has ended but custody still refuses.
func TestAnOldQueuedMessageSurvivesEveryLifecycleTransitionWhileCustodyHolds(t *testing.T) {
	for _, walk := range []struct{ action, phase string }{
		{"pause", "pausing"},
		{"resume", "resuming"},
		{"stop", "stopping"},
	} {
		for _, held := range []struct {
			name string
			gate ProgressReader
		}{
			{"the gate owns the branch", &gateFake{err: pipeline.ErrCustodyHeld}},
			{"custody cannot be read", &gateFake{err: errors.New("the no-mistakes database cannot be read")}},
		} {
			t.Run(walk.phase+"/"+held.name, func(t *testing.T) {
				// Arrange: a message aged in the saved store, reopened as a
				// restart leaves it, and the transition under way.
				store, h := testStore(t)
				meta := gatedGoblin(t, h)
				queued, err := store.Queue(Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "kept across the " + walk.action})
				if err != nil {
					t.Fatal(err)
				}
				store.mu.Lock()
				for i := range store.db.Actions {
					store.db.Actions[i].CreatedAt = time.Now().Add(-2 * messageWait)
				}
				store.mu.Unlock()
				if err := store.save(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				atPhase(t, h.State, meta, walk.action, walk.phase)
				service := &Service{Store: reopened, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: held.gate}}

				// Act: the transition's own attempt runs, then the transition
				// ends while custody still refuses.
				for _, phase := range []string{walk.phase, "running"} {
					atPhase(t, h.State, meta, walk.action, phase)
					if err := reopened.ProcessOne(context.Background(), service.execute); err != nil && !errors.Is(err, ErrDeferred) {
						t.Fatal(err)
					}

					// Assert: it waits rather than expiring to the CFO.
					waiting := actionOf(reopened, queued.ID)
					if waiting.Status != "queued" {
						t.Fatalf("at %s an old message reads %+v, want it still queued", phase, waiting)
					}
					if strings.Contains(waiting.Message, "the CFO has it") {
						t.Fatalf("at %s an old message was handed to the CFO: %q", phase, waiting.Message)
					}
				}
			})
		}
	}
}

// Once the transition has ended and custody reads clear, the message the
// goblin kept arrives exactly once, and one withdrawn on the way never does.
func TestAMessageKeptThroughACustodyHoldIsDeliveredOnceAndAWithdrawnOneNever(t *testing.T) {
	// Arrange: two aged messages and the goblin's own terminal.
	store, h := testStore(t)
	meta := gatedGoblin(t, h)
	goblin := goblinTerminal(t, h.State, meta.ID)
	kept, err := store.Queue(Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "delivered once"})
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := store.Queue(Action{ID: "m2", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "never delivered"})
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	for i := range store.db.Actions {
		store.db.Actions[i].CreatedAt = time.Now().Add(-2 * messageWait)
	}
	store.mu.Unlock()
	atPhase(t, h.State, meta, "resume", "resuming")
	heldService := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{err: pipeline.ErrCustodyHeld}}}
	if err := store.ProcessOne(context.Background(), heldService.execute); err != nil && !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}

	// Act: he withdraws one, the transition ends, and custody reads clear.
	if _, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: withdrawn.Text}); err != nil {
		t.Fatalf("the withdrawal was refused: %v", err)
	}
	atPhase(t, h.State, meta, "resume", "running")
	clear := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}
	for pass := 0; pass < 6 && len(goblin.lines(t)) == 0; pass++ {
		if err := store.ProcessOne(context.Background(), clear.execute); err != nil && !errors.Is(err, ErrDeferred) {
			t.Fatal(err)
		}
		time.Sleep(1200 * time.Millisecond)
	}

	// Assert: the kept message arrived once, and the withdrawn one never did.
	delivered := goblin.waitForLines(t, 1)
	if len(delivered) != 1 || !strings.Contains(delivered[0], kept.Text) {
		t.Fatalf("the goblin received %q, want the kept message once", delivered)
	}
	for pass := 0; pass < 3; pass++ {
		if err := store.ProcessOne(context.Background(), clear.execute); err != nil && !errors.Is(err, ErrDeferred) {
			t.Fatal(err)
		}
		time.Sleep(1200 * time.Millisecond)
	}
	if lines := goblin.lines(t); len(lines) != 1 || strings.Contains(strings.Join(lines, " "), withdrawn.Text) {
		t.Fatalf("after clear the goblin received %q, want one delivery and no withdrawn message", lines)
	}
}

// The controls the hold must not weaken: a clear custody keeps the ordinary
// expiry to the CFO during a transition, and a stopped goblin's message is the
// CFO's at once.
func TestATransitionWithClearCustodyKeepsTheOrdinaryExpiryAndAStoppedTaskHandsOver(t *testing.T) {
	t.Run("an old message expires to the CFO during a transition with clear custody", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		meta := gatedGoblin(t, h)
		action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "too late", Status: "queued", CreatedAt: time.Now().Add(-2 * messageWait)}
		store.db.Actions = append(store.db.Actions, action)
		atPhase(t, h.State, meta, "resume", "resuming")
		service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{}}}

		// Act
		evaluation, err := service.deliverMessage(context.Background(), action)

		// Assert
		if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "the CFO has it") {
			t.Fatalf("a clear custody during a transition reads %v %v, want the ordinary expiry to the CFO", evaluation, err)
		}
	})

	t.Run("a stopped goblin's message is the CFO's at once", func(t *testing.T) {
		// Arrange
		store, h := testStore(t)
		meta := gatedGoblin(t, h)
		action := Action{ID: "m1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "nobody home", Status: "queued", CreatedAt: time.Now()}
		store.db.Actions = append(store.db.Actions, action)
		atPhase(t, h.State, meta, "stop", "stopped")
		service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: &gateFake{err: pipeline.ErrCustodyHeld}}}

		// Act
		_, err := service.deliverMessage(context.Background(), action)

		// Assert: stopped hands over even while the gate holds.
		if err == nil || !strings.Contains(err.Error(), "was stopped before it could take it") {
			t.Fatalf("a stopped goblin's message reads %v, want it handed to the CFO", err)
		}
	})
}

// Two queued messages may say the same words, so a deletion names the one he
// picked by its own identity: A, B, A withdrawing the last A keeps the first A
// and B in their order, and a target that is gone or belongs to another goblin
// is refused rather than taking a message with the same words in its place.
func TestAQueuedWithdrawalNamesTheActionItDeletesAndNothingElse(t *testing.T) {
	// Arrange: A, B, A in one goblin's queue.
	newQueue := func(t *testing.T) (*Store, home.Home, state.TaskMeta) {
		t.Helper()
		store, h := testStore(t)
		meta := gatedGoblin(t, h)
		for _, queued := range []struct{ id, text string }{{"a1", "same words"}, {"b1", "other words"}, {"a2", "same words"}} {
			if _, err := store.Queue(Action{ID: queued.id, Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: queued.text}); err != nil {
				t.Fatal(err)
			}
		}
		return store, h, meta
	}
	// order is the queue as the store holds it now, which a withdrawal made
	// under the lock changes before any save.
	order := func(store *Store) []string {
		store.mu.Lock()
		defer store.mu.Unlock()
		var words []string
		for _, action := range store.db.Actions {
			if action.Kind == "message" && action.Status == "queued" {
				words = append(words, action.ID+":"+action.Text)
			}
		}
		return words
	}

	t.Run("the last A is the one deleted, and the rest keep their order", func(t *testing.T) {
		// Arrange
		store, _, meta := newQueue(t)

		// Act: he picks the second A, which the board names by its own id.
		if _, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: "same words", Target: "a2"}); err != nil {
			t.Fatalf("the withdrawal was refused: %v", err)
		}

		// Assert: the first A and B survive, in that order.
		if got := strings.Join(order(store), ","); got != "a1:same words,b1:other words" {
			t.Fatalf("the queue reads %q, want the first A and B in their order", got)
		}
	})

	t.Run("a target already delivered is refused, not answered with another message", func(t *testing.T) {
		// Arrange: the last A has already left the queue.
		store, _, meta := newQueue(t)
		if _, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: "same words", Target: "a2"}); err != nil {
			t.Fatal(err)
		}

		// Act: the board asks again for the same target.
		_, err := store.withdrawMessage(Action{ID: "w2", Kind: "message_withdraw", TaskID: meta.ID, Text: "same words", Target: "a2"})

		// Assert: it is refused, and the first A is still there.
		if err == nil || !strings.Contains(err.Error(), "no longer waiting") {
			t.Fatalf("a stale target reads %v, want it refused", err)
		}
		if got := strings.Join(order(store), ","); got != "a1:same words,b1:other words" {
			t.Fatalf("a stale target took another message: %q", got)
		}
	})

	t.Run("a target of another goblin is refused", func(t *testing.T) {
		// Arrange: a second gated goblin with a message of its own.
		store, h, meta := newQueue(t)
		other := treeGoblin(t, h.State, t.TempDir(), "other", false)
		other.Mode = "no-mistakes"
		gitWorktree(t, other.Worktree)
		if err := state.WriteTaskMeta(h.State, other); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Queue(Action{ID: "o1", Kind: "message", TaskID: other.ID, Generation: other.SpawnGen, Text: "same words"}); err != nil {
			t.Fatal(err)
		}

		// Act: this goblin's board names the other goblin's action.
		_, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: "same words", Target: "o1"})

		// Assert: it is refused, and both queues are untouched.
		if err == nil || !strings.Contains(err.Error(), "no longer waiting") {
			t.Fatalf("a cross-task target reads %v, want it refused", err)
		}
		if got := strings.Join(order(store), ","); got != "a1:same words,b1:other words,a2:same words,o1:same words" {
			t.Fatalf("a cross-task target changed the queue: %q", got)
		}
	})

	t.Run("a keep-for-resume delete never takes a queued message with its words", func(t *testing.T) {
		// Arrange: a gate-held goblin has A queued, and a pause kept A for its
		// resume, so two rows read the same words: one waiting in the queue and
		// one in the note.
		store, h := testStore(t)
		meta := gatedGoblin(t, h)
		if _, err := store.Queue(Action{ID: "q1", Kind: "message", TaskID: meta.ID, Generation: meta.SpawnGen, Text: "same words"}); err != nil {
			t.Fatal(err)
		}
		atPhase(t, h.State, meta, "pause", "paused")
		record, err := state.ReadLifecycle(h.State, meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		record.ResumeNote = fromOverlord("same words")
		if err := state.WriteLifecycle(h.State, record); err != nil {
			t.Fatal(err)
		}

		// Act: he deletes the kept row, which carries no target because the
		// note is what holds it.
		if _, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: "same words"}); err != nil {
			t.Fatalf("the note's copy was not deleted: %v", err)
		}

		// Assert: the note lost it, and the queued message is still waiting.
		record, err = state.ReadLifecycle(h.State, meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(record.ResumeNote, "same words") {
			t.Fatalf("the resume note still carries it: %q", record.ResumeNote)
		}
		if got := strings.Join(order(store), ","); got != "q1:same words" {
			t.Fatalf("the queue reads %q, want the queued message untouched", got)
		}
	})

	t.Run("a message kept for a resume is still withdrawn by its words", func(t *testing.T) {
		// Arrange: a message in a paused goblin's resume note.
		store, h := testStore(t)
		meta := makeNative(t, h.State, "task-1")
		goblin := goblinTerminal(t, h.State, meta.ID)
		atPhase(t, h.State, meta, "pause", "paused")
		service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
		messageTo(t, store, meta, "m1", "kept for resume")
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		_ = goblin

		// Act: no target, as a resume note has no action of its own.
		if _, err := store.withdrawMessage(Action{ID: "w1", Kind: "message_withdraw", TaskID: meta.ID, Text: "kept for resume"}); err != nil {
			t.Fatalf("a resume-note withdrawal was refused: %v", err)
		}

		// Assert
		record, err := state.ReadLifecycle(h.State, meta.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(record.ResumeNote, "kept for resume") {
			t.Fatalf("the resume note still carries it: %q", record.ResumeNote)
		}
	})
}

// A line that starts with / or ! is the harness's own command, so the board
// types it as it was written rather than behind the Overlord's stamp, which
// would make it plain words.
func TestACommandKeepsItsLeadingSlashOrBang(t *testing.T) {
	for _, text := range []string{"/compact", "!git status", "  /model opus  "} {
		if got := fromOverlord(text); got != strings.TrimSpace(text) {
			t.Fatalf("a command reached the harness as %q, want %q", got, strings.TrimSpace(text))
		}
	}
	if got := fromOverlord("please look at this"); !strings.HasPrefix(got, routing.BoardMessagePrefix) {
		t.Fatalf("a message reached the harness as %q, want the Overlord's stamp", got)
	}
}

// inTurnGoblin stands in for a goblin's terminal: while *isWorking its harness
// is in a turn, so a delivery that refuses one types nothing and says so, as
// spawn's SendNative does, and every other delivery is typed, in order, those
// it was asked to send now among the second.
func inTurnGoblin(t *testing.T, isWorking *bool) (received, sentNow *[]string) {
	t.Helper()
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	received, sentNow = &[]string{}, &[]string{}
	typeIntoGoblin = func(_ context.Context, _ string, meta state.TaskMeta, text string, shouldRefuseTurn, shouldSendNow bool) error {
		if *isWorking && shouldRefuseTurn {
			return fmt.Errorf("spawn: native terminal %s: %w: %w", meta.ID, fleet.ErrNotTyped, spawn.ErrInTurn)
		}
		*received = append(*received, text)
		if shouldSendNow {
			*sentNow = append(*sentNow, text)
		}
		return nil
	}
	return received, sentNow
}

// A message to a goblin in a turn is not typed into it: it waits where he can
// edit, send now or cancel it, and is delivered once the turn ends (the
// Overlord, 2026-10-27: "queued should exist at the bottom of the
// conversation until sent").
func TestAMessageToAGoblinInATurnWaitsForTheTurnToEnd(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "check the flaky test first")
	isWorking := true
	received, _ := inTurnGoblin(t, &isWorking)

	// Act: the goblin is in a turn.
	err := store.ProcessOne(t.Context(), service.execute)

	// Assert: nothing is typed, and the message waits where he can send it now.
	held := actionOf(store, "message-1")
	if !errors.Is(err, ErrDeferred) || held.Status != "queued" || !held.CanSendNow || len(*received) != 0 {
		t.Fatalf("delivery = %v, action = %+v, typed %q; want it held, sendable now, untyped", err, held, *received)
	}

	// Act: its turn ends.
	isWorking = false
	store.deferredUntil["message-1"] = time.Now().Add(-time.Second)
	if err := store.ProcessOne(t.Context(), service.execute); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(*received) != 1 || !strings.Contains((*received)[0], "check the flaky test first") || actionOf(store, "message-1").Status != "succeeded" {
		t.Fatalf("delivered %q, action = %+v, want the message once after the turn", *received, actionOf(store, "message-1"))
	}
}

// Send now types a message held for a goblin's turn into that turn and has
// its harness take it at once, as its own send-now key does in its terminal
// (the Overlord, 2026-10-30: "when sent now it actually sends now same way
// termianl expereince down with ctrl x crtl s").
func TestSendNowTypesAHeldMessageIntoTheGoblinsTurnToBeTakenAtOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "stop and rebase")
	isWorking := true
	received, sentNow := inTurnGoblin(t, &isWorking)
	if err := store.ProcessOne(t.Context(), service.execute); !errors.Is(err, ErrDeferred) {
		t.Fatalf("delivery = %v, want it held for the turn", err)
	}

	// Act
	if _, err := store.SendQueuedMessageNow("message-1", meta.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(t.Context(), service.execute); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(*received) != 1 || !strings.Contains((*received)[0], "stop and rebase") || actionOf(store, "message-1").Status != "succeeded" {
		t.Fatalf("delivered %q, action = %+v, want it typed into the turn once", *received, actionOf(store, "message-1"))
	}
	if !slices.Equal(*sentNow, *received) {
		t.Fatalf("sent now %q of %q, want it sent now", *sentNow, *received)
	}
}

// A message held for a goblin's turn says what Send now does for its
// harness: one with a send-now key, as Claude Code's Ctrl+X Ctrl+S, has the
// goblin take it at once, and once sent now it says so; one with none, as pi,
// takes it at its next tool call, as it always said.
func TestAHeldGoblinMessageSaysWhatSendNowDoesForItsHarness(t *testing.T) {
	for _, test := range []struct {
		harness, wantHeld, wantSent string
	}{
		{harness: "claude", wantHeld: "Queued until task-1's turn ends. Send now has it take the message at once.", wantSent: "Typed while it was working and sent now."},
		{harness: "pi", wantHeld: "Queued until task-1's turn ends. Send now types it for the goblin to take at its next tool call.", wantSent: sentToGoblin},
	} {
		t.Run(test.harness, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta := makeNative(t, h.State, "task-1")
			meta.Harness = test.harness
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitWorktree(t, meta.Worktree)
			goblinTerminal(t, h.State, meta.ID)
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
			messageTo(t, store, meta, "message-1", "stop and rebase")
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			typeIntoGoblin = func(_ context.Context, _ string, meta state.TaskMeta, _ string, shouldRefuseTurn, _ bool) error {
				if shouldRefuseTurn {
					return fmt.Errorf("spawn: native terminal %s: %w: %w", meta.ID, fleet.ErrNotTyped, spawn.ErrInTurn)
				}
				return fmt.Errorf("spawn: native terminal %s has the text submitted: %w", meta.ID, fleet.ErrQueuedForToolCall)
			}

			// Act
			if err := store.ProcessOne(t.Context(), service.execute); !errors.Is(err, ErrDeferred) {
				t.Fatalf("delivery = %v, want it held for the turn", err)
			}
			held := actionOf(store, "message-1")
			if _, err := store.SendQueuedMessageNow("message-1", meta.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(t.Context(), service.execute); err != nil {
				t.Fatal(err)
			}
			sent := actionOf(store, "message-1")

			// Assert
			if held.Message != test.wantHeld {
				t.Errorf("held message reads %q, want %q", held.Message, test.wantHeld)
			}
			if sent.Status != "running" || sent.Awaiting == nil || sent.Message != test.wantSent || sent.CanSendNow {
				t.Errorf("sent now = %+v, want it on its way reading %q, with no Send now", sent, test.wantSent)
			}
		})
	}
}

// Messages he sends while a goblin works reach it alone and in the order he
// sent them, even when the later one's wait runs out first as its turn ends:
// each held message waits a second of its own between looks.
func TestMessagesHeldForAGoblinsTurnAreDeliveredInTheOrderSent(t *testing.T) {
	// Arrange: two messages held for its turn, the later one sendable now.
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	isWorking := true
	received, _ := inTurnGoblin(t, &isWorking)
	messageTo(t, store, meta, "message-1", "first, check the train")
	if err := store.ProcessOne(t.Context(), service.execute); !errors.Is(err, ErrDeferred) {
		t.Fatalf("delivery = %v, want the first message held for the turn", err)
	}
	messageTo(t, store, meta, "message-2", "second, merge it")
	if err := store.ProcessOne(t.Context(), service.execute); err != nil && !errors.Is(err, ErrDeferred) {
		t.Fatal(err)
	}
	if later := actionOf(store, "message-2"); later.Status != "queued" || !later.CanSendNow {
		t.Fatalf("second message = %+v, want it waiting and sendable now", later)
	}

	// Act: the turn ends while the later message's wait has run out and the
	// earlier one's has not.
	isWorking = false
	store.deferredUntil["message-1"] = time.Now().Add(time.Hour)
	store.deferredUntil["message-2"] = time.Now().Add(-time.Second)
	if err := store.ProcessOne(t.Context(), service.execute); err != nil {
		t.Fatal(err)
	}
	store.deferredUntil["message-1"] = time.Now().Add(-time.Second)
	for range 2 {
		if err := store.ProcessOne(t.Context(), service.execute); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if len(*received) != 2 || !strings.Contains((*received)[0], "first, check the train") || !strings.Contains((*received)[1], "second, merge it") {
		t.Fatalf("delivered %q, want the first message and then the second", *received)
	}
}

// A message to a goblin whose prompt line holds words that were never sent,
// such as ones he typed in its terminal, types nothing onto them: it waits in
// the queue saying why, and is handed over alone once the line is empty (the
// Overlord, 2026-10-28: "it's sending and appending messages together that
// were sent previously").
func TestAMessageWaitsWhileItsGoblinsPromptLineHoldsUnsentWords(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta := makeNative(t, h.State, "task-1")
	gitWorktree(t, meta.Worktree)
	goblin := goblinTerminal(t, h.State, meta.ID)
	goblin.drawsDraft(t, meta.Harness, "half a thought")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}, Gate: fakeProgress{err: pipeline.ErrNoProgress}}}
	messageTo(t, store, meta, "message-1", "check the flaky test first")

	// Act
	err := store.ProcessOne(t.Context(), service.execute)

	// Assert: nothing is typed, and the message waits saying why.
	held := actionOf(store, "message-1")
	if !errors.Is(err, ErrDeferred) || held.Status != "queued" || !strings.Contains(held.Message, "never sent") || len(goblin.lines(t)) != 0 {
		t.Fatalf("delivery = %v, action = %+v, typed %q; want it queued, saying why, with nothing typed", err, held, goblin.lines(t))
	}

	// Act: the prompt line is emptied.
	goblin.drawsComposer(t, meta.Harness)
	store.deferredUntil["message-1"] = time.Now().Add(-time.Second)
	err = store.ProcessOne(t.Context(), service.execute)

	// Assert
	typed := goblin.waitForLines(t, 1)
	if err != nil || len(typed) != 1 || !strings.Contains(typed[0], "check the flaky test first") || actionOf(store, "message-1").Status != "succeeded" {
		t.Fatalf("delivery = %v, typed %q, action = %+v; want the message alone, once the line is empty", err, typed, actionOf(store, "message-1"))
	}
}

// Cancel takes a message held for the CFO's turn back by its own identity, so
// it never reaches the CFO; a cancel that names no message held for the CFO
// is refused and takes nothing.
func TestCancelTakesBackAMessageHeldForTheCFO(t *testing.T) {
	queued := func(store *Store) []string {
		store.mu.Lock()
		defer store.mu.Unlock()
		var ids []string
		for _, action := range store.db.Actions {
			if action.Kind == "message" && action.Status == "queued" {
				ids = append(ids, action.ID)
			}
		}
		return ids
	}
	newQueue := func(t *testing.T) *Store {
		t.Helper()
		store, h := testStore(t)
		meta, err := state.ReadTaskMeta(h.State, "task-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Queue(Action{ID: "c1", Kind: "message", Text: "stop and wait for me"}); err != nil {
			t.Fatal(err)
		}
		messageTo(t, store, meta, "g1", "stop and wait for me")
		return store
	}

	t.Run("the held message is taken back", func(t *testing.T) {
		// Arrange
		store := newQueue(t)

		// Act
		withdrawn, err := store.Queue(Action{ID: "w1", Kind: "message_withdraw", Text: "stop and wait for me", Target: "c1"})

		// Assert
		if err != nil || withdrawn.Status != "succeeded" {
			t.Fatalf("the cancel = %+v, %v; want it taken back", withdrawn, err)
		}
		if got := queued(store); !slices.Equal(got, []string{"g1"}) {
			t.Fatalf("queued = %q, want only the goblin's message left", got)
		}
	})

	for _, refused := range []struct{ name, target string }{
		{"a cancel naming no message", ""},
		{"a cancel naming one already gone", "gone"},
		{"a cancel naming a goblin's message", "g1"},
	} {
		t.Run(refused.name+" is refused", func(t *testing.T) {
			// Arrange
			store := newQueue(t)

			// Act
			_, err := store.Queue(Action{ID: "w1", Kind: "message_withdraw", Text: "stop and wait for me", Target: refused.target})

			// Assert
			if err == nil {
				t.Fatal("the cancel was accepted, want it refused")
			}
			if got := queued(store); !slices.Equal(got, []string{"c1", "g1"}) {
				t.Fatalf("queued = %q, want both messages still waiting", got)
			}
		})
	}
}
