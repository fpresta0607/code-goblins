package supervisor

import (
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A goblin's question and its command each carry its message from its notify
// through the supervisor's inboxes onto the card the board shows, so he copies
// the email there instead of opening its file.
func TestAGoblinsQuestionAndCommandCarryItsMessageToTheBoard(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, record, _, _ := goblinFixture(t, store)
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: Send the refund email"); err != nil {
		t.Fatal(err)
	}
	mail := Mail{To: "support@iproyal.com", Subject: "Refund for order 4412", Body: "Hello,\n\nPlease refund order 4412.\n\nThanks"}

	// Act
	if err := SurfaceNotify(store.Home.State, meta.ID, record, record.Detail, "", "", nil, &mail); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	if err := PublishGoblinRun(store.Home, meta.ID, record.Seq+1, "Open the refund page", "powershell", "Start-Process https://iproyal.com\n", &mail); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestGoblinRuns(); err != nil {
		t.Fatal(err)
	}
	items := boardItems(store.Snapshot())

	// Assert
	if len(items.Questions) != 1 || items.Questions[0].Mail == nil || *items.Questions[0].Mail != mail {
		t.Fatalf("questions = %+v, want the goblin's question carrying its message", items.Questions)
	}
	if len(items.Runs) != 1 || items.Runs[0].Mail == nil || *items.Runs[0].Mail != mail {
		t.Fatalf("runs = %+v, issues %q, want the goblin's command carrying its message", items.Runs, store.Snapshot().Issues)
	}
}
