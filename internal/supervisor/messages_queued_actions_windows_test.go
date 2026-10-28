//go:build windows

package supervisor

import (
	"strings"
	"testing"
	"time"
)

// queueMessageFor puts one queued message on the store, as a message that
// waits for its goblin does.
func queueMessageFor(t *testing.T, store *Store, id, task, text string, canSendNow bool, why string) {
	t.Helper()
	store.db.Actions = append(store.db.Actions, Action{ID: id, Kind: "message", TaskID: task, Generation: "g1", Text: text, Status: "queued", CreatedAt: time.Now().UTC(), CanSendNow: canSendNow, SendNowWhy: why})
}

func TestSendNowTakesAQueuedMessageThatCanBeTypedNow(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	queueMessageFor(t, store, "m1", "task-1", "take this", true, "")
	store.deferredUntil = map[string]time.Time{"m1": time.Now().Add(time.Minute)}

	// Act
	sent, err := store.SendQueuedMessageNow("m1", "task-1")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if sent.ID != "m1" || store.deferredUntil["m1"].After(time.Now()) {
		t.Fatalf("the message reads %+v and still waits until %v", sent, store.deferredUntil["m1"])
	}
}

func TestSendNowIsRefusedWithTheReasonItCannotBeTyped(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	queueMessageFor(t, store, "m1", "task-1", "take this", false, "Its gate holds the branch.")

	// Act
	_, err := store.SendQueuedMessageNow("m1", "task-1")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Its gate holds the branch.") {
		t.Fatalf("a gated message came back as %v, want its own reason", err)
	}
	if _, err := store.SendQueuedMessageNow("m1", "another-task"); err == nil {
		t.Fatal("another task's message was sent")
	}
}

// The Overlord, 2026-10-28: "lack of indmepotent click on teh queue". A
// second Send now of a message he already sent now is the same ask, whether
// it still waits or has gone on its way since, and never an error.
func TestASecondSendNowOfAMessageAlreadySentNowIsNoError(t *testing.T) {
	for _, status := range []string{"queued", "running", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			queueMessageFor(t, store, "m1", "task-1", "take this", true, "")
			if _, err := store.SendQueuedMessageNow("m1", "task-1"); err != nil {
				t.Fatal(err)
			}
			for at := range store.db.Actions {
				if store.db.Actions[at].ID == "m1" {
					store.db.Actions[at].Status = status
				}
			}

			// Act
			again, err := store.SendQueuedMessageNow("m1", "task-1")

			// Assert
			if err != nil || again.ID != "m1" || !again.IsSentNow {
				t.Errorf("the second Send now came back as %+v, %v; want the same message and no error", again, err)
			}
		})
	}
}
