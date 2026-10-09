package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// Under AFK mode the CFO answers a decision itself and acts on its answer.
// When AFK mode turns off the decision is asked in the Command Center with
// the CFO's answer checked, for him to keep or change, and the report says
// so. What only he can do is asked with nothing checked.
func TestADecisionTheCFOAnsweredIsAskedWithItsAnswerChecked(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	decided := leftLine("Which fallback does the gate policy take?")
	decided.Options, decided.Recommendation, decided.Answer = []string{"Drop it from the machine config", "Take it into the policy"}, "", "Drop it from the machine config"
	for _, entry := range []afk.Entry{decided, leftLine("Sign in to Fly again?")} {
		if err := LogAFKDecision(h, entry); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	s.runRequests.Lock()
	err := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()
	report, found, readErr := ReadAFKReport(h)

	// Assert
	if err != nil || readErr != nil || !found {
		t.Fatalf("his off = %v, report = %v, %v", err, found, readErr)
	}
	answers, texts := map[string]string{}, map[string]string{}
	for _, q := range s.Store.Snapshot().Questions {
		if strings.HasPrefix(q.ID, "afk-left-") && q.Status == "pending" {
			what := q.Text[:strings.Index(q.Text, "\n")]
			answers[what], texts[what] = q.Decided, q.Text
		}
	}
	if !strings.Contains(texts["Which fallback does the gate policy take?"], "\n- Evidence: ") || !strings.Contains(texts["Sign in to Fly again?"], "\n- Why it is yours: ") {
		t.Errorf("question texts = %q, want the decision's evidence and why his sign-in is his", texts)
	}
	if len(answers) != 2 || answers["Which fallback does the gate policy take?"] != "Drop it from the machine config" || answers["Sign in to Fly again?"] != "" {
		t.Errorf("questions asked of him = %q, want the decision with the CFO's answer checked and his sign-in with nothing checked", answers)
	}
	says := map[string]string{}
	for _, one := range report.Held {
		says[one.What] = one.Now
	}
	if now := says["Which fallback does the gate policy take?"]; now != "the CFO answered it: Drop it from the machine config, yours to keep or change" {
		t.Errorf("the report says the decision is %q, want the CFO's answer, his to keep or change", now)
	}
	if now := says["Sign in to Fly again?"]; now != "still waiting on you" {
		t.Errorf("the report says his sign-in is %q, want it still waiting on him", now)
	}
}

// His answer to a decision the CFO answered under AFK mode reaches the CFO
// with what it answered: kept, or changed, which the CFO undoes or redoes.
func TestHisAnswerToADecisionTheCFOAnsweredTellsTheCFOWhetherHeKeptIt(t *testing.T) {
	for name, c := range map[string]struct{ answer, want string }{
		"kept":    {"Board", "Answer: Board. You answered Board while AFK mode was on, and he kept it."},
		"changed": {"Tree", "Answer: Tree. You answered Board while AFK mode was on, and he changed it: undo or redo what your answer started."},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			primary, closed, cfo, connection := primaryFixture(t, store)
			s := &Service{Store: store, Options: Options{CFO: connection}}
			question := Question{ID: "afk-left-20261009T032741.118000000Z", Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, Decided: "Board", CreatedAt: time.Now().UTC(), Status: "pending"}
			if err := store.acceptQuestion(question); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queue(Action{ID: "answer-" + name, Kind: "cfo_answer", QuestionID: question.ID, Generation: closed, Text: c.answer, AnswerKind: "option"}); err != nil {
				t.Fatal(err)
			}
			reregisterCFO(t, store, primary)

			// Act
			err := store.ProcessOne(context.Background(), s.execute)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], c.want) {
				t.Errorf("the CFO received %q, want %q", typed, c.want)
			}
		})
	}
}
