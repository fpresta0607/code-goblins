package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// One card, one state: what the Overlord does to a card closes everything the
// card stood for, so nothing he acted on shows a second time.

// A page's card shows the question its goblin asked beside the page. Clearing
// the card dismisses that question with it, and the CFO, which holds the
// goblin's notify, is told; the question never comes back as a card of its own.
func TestClearingAPagesCardDismissesTheQuestionItCarries(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, cfo, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	askOnAPage(t, store)
	page := store.Snapshot().Reviews[0]
	s := &Service{Store: store, Options: Options{CFO: connection}}

	// Act
	_, queued := store.Queue(Action{ID: "clear-1", Kind: "review_clear", ReviewID: page.ID, Generation: page.Identity})
	if queued == nil {
		queued = store.ProcessOne(context.Background(), s.execute)
	}
	after := store.Snapshot()

	// Assert
	if queued != nil {
		t.Fatalf("clear = %v, want it taken", queued)
	}
	if got := after.Reviews[0]; got.State != "cleared" {
		t.Errorf("the page's item = %s, want it cleared", got.State)
	}
	if got := after.Questions[0]; got.Status != "cleared" || got.Message != "You cleared its page's card." {
		t.Errorf("the question its card carried = %s %q, want it dismissed with the card", got.Status, got.Message)
	}
	if typed := cfo.waitForLines(t, 1); len(typed) != 1 || !strings.Contains(typed[0], "task-1's question") || !strings.Contains(typed[0], "dismissed") {
		t.Errorf("the CFO got %q, want one message naming task-1's question as dismissed", typed)
	}
}

// The card and the question it carries close the moment the board takes the
// clear, so neither waits on screen for the clear's action to run; the CFO
// hears of the dismissed question once, when that action runs.
func TestClearingAPagesCardClosesItsQuestionBeforeItsActionRuns(t *testing.T) {
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
	toldBefore := len(cfo.lines(t))
	ran := store.ProcessOne(context.Background(), s.execute)

	// Assert
	if queued != nil || ran != nil {
		t.Fatalf("clear = %v, its action = %v, want both taken", queued, ran)
	}
	if got := taken.Reviews[0]; got.State != "cleared" {
		t.Errorf("the page's item = %s before its action ran, want it cleared", got.State)
	}
	if got := taken.Questions[0]; got.Status != "cleared" || got.Message != "You cleared its page's card." {
		t.Errorf("the question its card carried = %s %q before its action ran, want it dismissed with the card", got.Status, got.Message)
	}
	if typed := cfo.waitForLines(t, 1); toldBefore != 0 || len(typed) != 1 || !strings.Contains(typed[0], "task-1's question") {
		t.Errorf("the CFO was told %d times before the action ran and got %q after it, want nothing before and one message naming task-1's question", toldBefore, typed)
	}
}

// The Overlord's answer to a goblin's question on the board gives the goblin
// what it waited for, so its waits on him up to that question close with it,
// as they do when the CFO answers; a wait the goblin files later is its own.
func TestABoardAnswerClosesTheGoblinsWaitsUpToItsQuestion(t *testing.T) {
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
			earlier := openReview(fmt.Sprintf("waiting-%s-%d", meta.ID, q.Seq), meta.ID)
			later := openReview(fmt.Sprintf("waiting-%s-%d", meta.ID, q.Seq+5), meta.ID)
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
			reviews := map[string]Review{}
			for _, r := range store.Snapshot().Reviews {
				reviews[r.ID] = r
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := reviews[earlier.ID]; got.State != "answered" || got.AnsweredBy != "overlord" || got.AnsweredIn != "question" {
				t.Errorf("the wait up to the question = %s by %q in %q, want it answered by the Overlord through its question", got.State, got.AnsweredBy, got.AnsweredIn)
			}
			if got := reviews[later.ID]; got.State != "open" {
				t.Errorf("the wait filed after the question = %s, want it still open", got.State)
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
	if err := SurfaceNotify(h.State, meta.ID, again, again.Detail, nil); err != nil {
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
