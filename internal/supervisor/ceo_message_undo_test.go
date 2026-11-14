package supervisor

import (
	"slices"
	"testing"
	"time"
)

// The CEO, 2026-11-02: his message to the CEO waits on the board for a short
// undo window, where Edit and Cancel work, and is then typed into the turn;
// Send now skips the window, and a command acts at once. A goblin's message
// has no window and goes into its goblin's turn at once.
func TestHisMessageToTheCEOWaitsOutItsUndoWindowOnTheBoard(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name     string
		action   Action
		isPicked bool
	}{
		{name: "just sent", action: Action{Text: "stop and rebase", CreatedAt: now}},
		{name: "sent a window ago", action: Action{Text: "stop and rebase", CreatedAt: now.Add(-ceoMessageUndo)}, isPicked: true},
		{name: "sent now", action: Action{Text: "stop and rebase", CreatedAt: now, IsSentNow: true}, isPicked: true},
		{name: "a slash command", action: Action{Text: "/compact", CreatedAt: now}, isPicked: true},
		{name: "a bang command", action: Action{Text: "!git status", CreatedAt: now}, isPicked: true},
		{name: "a goblin's", action: Action{Text: "stop and rebase", TaskID: "task-1", CreatedAt: now}, isPicked: true},
		{name: "after the clock stepped back", action: Action{Text: "stop and rebase", CreatedAt: now.Add(time.Hour)}, isPicked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			test.action.ID, test.action.Kind, test.action.Status = "m1", "message", "queued"
			store.db.Actions = append(store.db.Actions, test.action)

			// Act
			store.mu.Lock()
			picked := store.nextQueued(true)
			store.mu.Unlock()
			due := store.heldDueIn()

			// Assert
			if isPicked := picked == 0; isPicked != test.isPicked {
				t.Errorf("picked = %v, want %v", isPicked, test.isPicked)
			}
			if isHeld := due > 0; isHeld == test.isPicked || isHeld && (due > ceoMessageUndo || due < ceoMessageUndo-time.Minute) {
				t.Errorf("held for another %v; want it held for the rest of its window only while it is not picked", due)
			}
		})
	}
}

// His message to the CEO is queued with Send now offered, since Send now is
// what skips its undo window, and a command, which never waits in one, with
// none.
func TestHisMessageToTheCEOIsQueuedWithSendNowOffered(t *testing.T) {
	for _, test := range []struct {
		text           string
		wantCanSendNow bool
	}{
		{text: "stop and rebase", wantCanSendNow: true},
		{text: "/compact"},
	} {
		t.Run(test.text, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)

			// Act
			queued, err := store.Queue(Action{ID: "m1", Kind: "message", Text: test.text})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if queued.Status != "queued" || queued.CanSendNow != test.wantCanSendNow {
				t.Errorf("queued = %+v; want it queued with Send now %v", queued, test.wantCanSendNow)
			}
		})
	}
}

// Send now and Cancel act on his message to the CEO while it waits out its
// undo window: Send now has the worker type it at once, and Cancel takes it
// off the board, so it never reaches the CEO.
func TestSendNowAndCancelActOnHisMessageInItsUndoWindow(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	for _, id := range []string{"m1", "m2"} {
		if _, err := store.Queue(Action{ID: id, Kind: "message", Text: "from " + id}); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	_, sendErr := store.SendQueuedMessageNow("m1", "")
	_, cancelErr := store.Queue(Action{ID: "withdraw-m2", Kind: "message_withdraw", Target: "m2", Text: "from m2"})
	store.mu.Lock()
	picked := store.nextQueued(true)
	store.mu.Unlock()

	// Assert
	if sendErr != nil || cancelErr != nil {
		t.Fatalf("Send now = %v, Cancel = %v; want both to act", sendErr, cancelErr)
	}
	actions := store.Snapshot().Actions
	if picked < 0 || actions[picked].ID != "m1" {
		t.Errorf("picked = %d of %+v; want the message sent now picked at once", picked, actions)
	}
	if slices.ContainsFunc(actions, func(a Action) bool { return a.ID == "m2" }) {
		t.Errorf("actions = %+v; want the cancelled message gone", actions)
	}
}

// A Send now, Edit or Cancel that comes after the board let his message go
// says at once, in plain words, where it went, rather than that nothing was
// done (the Overlord, 2026-11-02: "this question mark is stuck and lost").
func TestAPressOnAMessageTheBoardNoLongerHoldsSaysWhereItWent(t *testing.T) {
	since := time.Now().UTC()
	for _, test := range []struct {
		name          string
		action        *Action
		want          string
		isSendNowOnly bool
	}{
		{name: "being tried", action: &Action{TaskID: "", Status: "running"}, want: "The board is trying it with the CEO right now. Press again in a moment."},
		{name: "typed into the CEO's turn", action: &Action{Status: "running", Awaiting: &Awaiting{Host: "ceo", Since: since, QuietSince: since}}, want: "It was already typed into the CEO's turn."},
		{name: "typed into a goblin's turn", action: &Action{TaskID: "task-1", Status: "running", Awaiting: &Awaiting{Task: "task-1", Since: since, QuietSince: since}}, want: "It was already typed into task-1's turn."},
		{name: "taken by the CEO", action: &Action{Status: "succeeded"}, want: "The CEO already took it."},
		// His Cancel takes one never seen taken off the board (#2541).
		{name: "never seen taken", action: &Action{Status: "uncertain"}, want: "It already left the board.", isSendNowOnly: true},
		{name: "gone", want: "That message is no longer on the board."},
	} {
		for _, press := range []string{"Send now", "Cancel"} {
			if test.isSendNowOnly && press == "Cancel" {
				continue
			}
			t.Run(test.name+" "+press, func(t *testing.T) {
				// Arrange
				store, _ := testStore(t)
				task := ""
				if test.action != nil {
					test.action.ID, test.action.Kind, test.action.Text, test.action.CreatedAt = "m1", "message", "stop and rebase", since
					task = test.action.TaskID
					store.db.Actions = append(store.db.Actions, *test.action)
				}

				// Act
				var err error
				if press == "Send now" {
					_, err = store.SendQueuedMessageNow("m1", task)
				} else {
					_, err = store.Queue(Action{ID: "withdraw-m1", Kind: "message_withdraw", TaskID: task, Target: "m1", Text: "stop and rebase"})
				}

				// Assert
				if err == nil || err.Error() != test.want {
					t.Errorf("%s answered %v; want %q", press, err, test.want)
				}
			})
		}
	}
}

// Every delivery goes ahead of the board's own evaluations, which reconciles
// queue in batches: on 2026-11-02 his message waited 93 s behind one.
func TestADeliveryGoesAheadOfTheBoardsEvaluations(t *testing.T) {
	for _, test := range []struct {
		name    string
		actions []Action
		want    string
	}{
		{name: "a message behind evaluations", actions: []Action{{ID: "e1", Kind: "evaluate", TaskID: "task-1"}, {ID: "e2", Kind: "evaluate", TaskID: "task-2"}, {ID: "m1", Kind: "message", Text: "stop and rebase"}}, want: "m1"},
		{name: "evaluations alone", actions: []Action{{ID: "e1", Kind: "evaluate", TaskID: "task-1"}, {ID: "e2", Kind: "evaluate", TaskID: "task-2"}}, want: "e1"},
		{name: "a message in its undo window", actions: []Action{{ID: "e1", Kind: "evaluate", TaskID: "task-1"}, {ID: "m1", Kind: "message", Text: "stop and rebase", CreatedAt: time.Now().UTC()}}, want: "e1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			for _, a := range test.actions {
				a.Status = "queued"
				if a.CreatedAt.IsZero() {
					a.CreatedAt = time.Now().UTC().Add(-ceoMessageUndo)
				}
				store.db.Actions = append(store.db.Actions, a)
			}

			// Act
			store.mu.Lock()
			picked := store.nextQueued(true)
			store.mu.Unlock()

			// Assert
			if picked < 0 || store.db.Actions[picked].ID != test.want {
				t.Errorf("picked = %d; want %s", picked, test.want)
			}
		})
	}
}
