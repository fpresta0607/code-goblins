package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// One card, one state: what the Overlord does to a card closes what that card
// is, and nothing else, so nothing he never saw closes with it.

// Clearing a page's card clears only the page: a question its goblin asked
// meanwhile stays pending for the CFO to answer, and the CFO is told of no
// dismissed question, at once or when the clear's action runs.
func TestClearingAPagesCardLeavesTheGoblinsQuestion(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, cfo, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	askOnAPage(t, store)
	page := store.Snapshot().Reviews[0]
	s := &Service{Store: store, Options: Options{CFO: connection}}

	// Act
	_, queued := store.Queue(Action{ID: "clear-1", Kind: "review_clear", ReviewID: page.ID, Generation: page.Identity})
	taken := store.Snapshot()
	ran := store.ProcessOne(context.Background(), s.execute)
	after := store.Snapshot()

	// Assert
	if queued != nil || ran != nil {
		t.Fatalf("clear = %v, its action = %v, want both taken", queued, ran)
	}
	if got := taken.Reviews[0]; got.State != "cleared" {
		t.Errorf("the page's item = %s, want it cleared", got.State)
	}
	for when, snapshot := range map[string]Database{"when the board took the clear": taken, "after its action ran": after} {
		if got := snapshot.Questions[0]; got.Status != "pending" {
			t.Errorf("the goblin's question %s = %s %q, want it still pending", when, got.Status, got.Message)
		}
	}
	for _, line := range cfo.lines(t) {
		if strings.Contains(line, "question") && strings.Contains(line, "dismissed") {
			t.Errorf("the CFO got %q, want no question named dismissed", line)
		}
	}
}

// The Overlord's answer to a goblin's question on the board closes that
// question and only the waits that follow it: the goblin's waits that do not,
// one raised before the question and one that opens a link, stay open, since
// such a wait, a sign-in only he can clear for one, is never settled by an
// answer to a different question.
func TestABoardAnswerLeavesOpenTheWaitsThatDoNotFollowIt(t *testing.T) {
	for _, c := range []struct {
		name    string
		deliver Evaluation
	}{
		{"delivered at once", Evaluation{Reason: "Delivered."}},
		{"sent behind the goblin's turn", Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: "task-1", Generation: "g1", Since: time.Now().UTC()}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			meta, record, _, connection := goblinFixture(t, store)
			q := surfaced(t, store, meta, record, connection)
			earlier := openReview(fmt.Sprintf("waiting-%s-%d", meta.ID, q.Seq-1), meta.ID)
			earlier.CreatedAt = q.CreatedAt.Add(-time.Minute)
			later := openReview(fmt.Sprintf("waiting-%s-%d", meta.ID, q.Seq+5), meta.ID)
			later.Link = "https://dashboard.stripe.com/login"
			for _, r := range []Review{earlier, later} {
				if err := store.acceptReview(r); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err != nil {
				t.Fatal(err)
			}

			// Act
			err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) { return c.deliver, nil })

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range store.Snapshot().Reviews {
				if r.State != "open" {
					t.Errorf("wait %s = %s by %q in %q, want it still open", r.ID, r.State, r.AnsweredBy, r.AnsweredIn)
				}
			}
		})
	}
}

// A goblin that asks again has moved past its earlier question: the newer
// question replaces it, so the Command Center shows one card, not two.
func TestAGoblinsNewerQuestionReplacesItsOlderOne(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, record, _, connection := goblinFixture(t, store)
	first := surfaced(t, store, meta, record, connection)
	again, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which port? options: Port 8080 | Port 9090")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if err := SurfaceNotify(h.State, meta.ID, again, again.Detail, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	questions := map[string]Question{}
	for _, q := range store.Snapshot().Questions {
		questions[q.ID] = q
	}

	// Assert
	if len(questions) != 2 {
		t.Fatalf("questions = %+v, want the two the goblin asked", questions)
	}
	if got := questions[first.ID]; got.Status != "superseded" || got.Message != "The goblin asked again, so its newer question replaces this one." {
		t.Errorf("the earlier question = %s %q, want it replaced by the newer one", got.Status, got.Message)
	}
	for id, q := range questions {
		if id != first.ID && q.Status != "pending" {
			t.Errorf("the newer question = %s, want it waiting on him", q.Status)
		}
	}
}
