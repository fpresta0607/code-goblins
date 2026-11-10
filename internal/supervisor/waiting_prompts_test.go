package supervisor

import (
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
)

func TestNamedWaitingNamesTheBoardMessageThatTypedEachPrompt(t *testing.T) {
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	message := func(id, task, text, status string, created time.Time) Action {
		return Action{ID: id, Kind: "message", TaskID: task, Text: text, Status: status, CreatedAt: created, UpdatedAt: created}
	}
	prompt := func(id, source, text string) fleettree.WaitingPrompt {
		return fleettree.WaitingPrompt{ID: id, At: at, Source: source, Text: text}
	}
	for _, test := range []struct {
		name    string
		task    string
		actions []Action
		waiting []fleettree.WaitingPrompt
		want    []string
	}{
		{name: "a message being typed", task: "task-1",
			actions: []Action{message("a1", "task-1", "check the phone", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{"a1"}},
		{name: "the CEO's message, which names no task", task: "",
			actions: []Action{message("a1", "", "check the phone", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{"a1"}},
		{name: "words the harness wrapped", task: "task-1",
			actions: []Action{message("a1", "task-1", "check\n the  phone ", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{"a1"}},
		{name: "a ! command, which Claude Code queues by the words after its !", task: "",
			actions: []Action{message("a1", "", "! claude doctor", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "claude doctor")}, want: []string{"a1"}},
		{name: "a message the board still holds", task: "task-1",
			actions: []Action{message("a1", "task-1", "check the phone", "queued", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{""}},
		{name: "a message taken after the prompt was recorded", task: "task-1",
			actions: []Action{message("a1", "task-1", "check the phone", "running", at.Add(time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{""}},
		{name: "another session's message", task: "task-1",
			actions: []Action{message("a1", "task-2", "check the phone", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "check the phone")}, want: []string{""}},
		{name: "the CEO's own prompt", task: "task-1",
			actions: []Action{message("a1", "task-1", "check the phone", "running", at.Add(-time.Second))},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromCEO, "check the phone")}, want: []string{""}},
		{name: "two of the same words, oldest first, each named once", task: "task-1",
			actions: []Action{
				message("a2", "task-1", "continue", "running", at.Add(-time.Second)),
				message("a1", "task-1", "continue", "running", at.Add(-2*time.Second)),
			},
			waiting: []fleettree.WaitingPrompt{prompt("q1", fleettree.FromOverlord, "continue"), prompt("q2", fleettree.FromOverlord, "continue"), prompt("q3", fleettree.FromOverlord, "continue")},
			want:    []string{"a1", "a2", ""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			store.mu.Lock()
			store.db.Actions = test.actions
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			// Act
			named := store.namedWaiting(test.task, test.waiting)

			// Assert
			got := make([]string, len(named))
			for i, each := range named {
				got[i] = each.Action
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("named actions = %q, want %q", got, test.want)
			}
			for _, each := range test.waiting {
				if each.Action != "" {
					t.Fatal("the reader's prompts were changed")
				}
			}
		})
	}
}
