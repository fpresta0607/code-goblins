package supervisor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
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
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
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
	goblin := goblinTerminal(t, h.State, meta.ID)
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
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
			goblin := goblinTerminal(t, h.State, meta.ID)
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: h.State}}}
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
			err = HandOverReplacedNote(h.State, current, record)

			// Assert
			told := toldCFO(t, h.State, meta.ID)
			if err != nil || len(told) != len(tc.want) {
				t.Fatalf("hand-off = %v, the CFO was told %q, want each of %q once", err, told, tc.want)
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
