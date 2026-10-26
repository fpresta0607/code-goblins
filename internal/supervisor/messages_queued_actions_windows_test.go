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

func TestAQueuedMessageIsEditedWhereItWaits(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	queueMessageFor(t, store, "m1", "task-1", "the old words", false, "Its gate holds the branch.")

	// Act
	edited, err := store.EditQueuedMessage("m1", "task-1", "the new words")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if edited.Text != "the new words" {
		t.Fatalf("the message reads %q", edited.Text)
	}
	for _, action := range store.db.Actions {
		if action.ID == "m1" && action.Text != "the new words" {
			t.Fatalf("the stored message reads %q", action.Text)
		}
	}
	if _, err := store.EditQueuedMessage("m1", "another-task", "no"); err == nil {
		t.Fatal("another task's message was edited")
	}
	if _, err := store.EditQueuedMessage("m1", "task-1", "  "); err == nil {
		t.Fatal("an empty message was accepted")
	}
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
