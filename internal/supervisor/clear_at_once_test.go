package supervisor

import (
	"strings"
	"testing"
	"time"
)

// The Overlord, 2026-10-02: an item he closes "hangs around for a stale second
// or two". Measured on main-9f22183c in a quiet scratch home: a question he
// dismissed stayed pending for 2.9 s, until its queued action's turn came,
// while an answer closed its question at once. What he clears closes when the
// board takes it; only telling its asker waits for the action to run.
func TestAClearClosesItsItemWhenTheBoardTakesIt(t *testing.T) {
	identity := strings.Repeat("c", 64)
	for _, c := range []struct {
		name   string
		action Action
		closed func(Database) string
		want   string
	}{
		{"a question he dismisses", Action{ID: "dismiss-1", Kind: "question_clear", QuestionID: "merge-the-train", Generation: identity},
			func(d Database) string { return d.Questions[0].Status + ": " + d.Questions[0].Message }, "cleared: You dismissed it: answered elsewhere or no longer needed."},
		{"a review he clears", Action{ID: "clear-1", Kind: "review_clear", ReviewID: "mockups-review-1", Generation: strings.Repeat("a", 64)},
			func(d Database) string { return d.Reviews[0].State + ": " + d.Reviews[0].Reason }, "cleared: "},
		{"a document he opened", Action{ID: "open-1", Kind: "review_clear", ReviewID: "mockups-review-1", Generation: strings.Repeat("a", 64), Text: "Opened"},
			func(d Database) string { return d.Reviews[0].State + ": " + d.Reviews[0].Reason }, "cleared: Opened"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			if err := store.acceptReview(openReview("mockups-review-1", "task-1")); err != nil {
				t.Fatal(err)
			}
			if err := store.acceptQuestion(Question{ID: "merge-the-train", Identity: identity, Text: "Merge it?", Options: []string{"Yes", "No"}, CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}

			// Act: the board takes the clear; its action has not run.
			queued, err := store.Queue(c.action)

			// Assert
			if err != nil {
				t.Fatalf("the clear = %v, want it taken", err)
			}
			after := store.Snapshot()
			if got := c.closed(after); got != c.want {
				t.Errorf("the item reads %q before its action runs, want %q", got, c.want)
			}
			if queued.Status != "queued" || after.Actions[0].Status != "queued" {
				t.Errorf("the action is %s, want it still queued to tell the asker", after.Actions[0].Status)
			}
		})
	}
}
