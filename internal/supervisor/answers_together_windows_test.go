package supervisor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// The Overlord, 2026-10-09: "CFO got overloaded with a bunch of these overlord
// messages". Between 13:55:58Z and 13:56:05Z he answered four of the CFO's
// questions from the Command Center, and each was typed into the CFO as a
// message of its own, in the middle of a turn, two minutes after a CFO
// restart. sitting is those four questions by their ids, in the order he
// answered them; their words and his choices stand in for the real ones.
var sitting = []sittingQuestion{
	{"whats-new-drop", "Drop the old page?", "Drop it"},
	{"whats-new-captures", "Retake the captures?", "Retake them"},
	{"whats-new-apps", "List the apps too?", "List them"},
	{"hh-pr38-box-check", "Merge the box check?", "Merge it"},
}

// sittingGaps is how long after the answer before it he gave each answer of
// the sitting: four answers within seven seconds.
var sittingGaps = []time.Duration{0, 2300 * time.Millisecond, 2400 * time.Millisecond, 2300 * time.Millisecond}

// sittingQuestion is a question of the CFO's own and the choice the Overlord
// answers it with.
type sittingQuestion struct{ id, text, answer string }

// line is the answer as the CFO reads it, alone or as one of several.
func (q sittingQuestion) line() string {
	return fmt.Sprintf("User answer to CFO question %s. Question: %s Answer: %s", q.id, q.text, q.answer)
}

// cfoAsks publishes questions as the CFO registered as identity asked them,
// each waiting on the Overlord.
func cfoAsks(t *testing.T, store *Store, identity string, questions ...sittingQuestion) {
	t.Helper()
	for _, q := range questions {
		if err := store.acceptQuestion(Question{ID: q.id, Identity: identity, Text: q.text, Options: []string{q.answer, "Not now"}, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
}

// heAnswers queues the Overlord's answer to q, given now from the Command
// Center.
func heAnswers(t *testing.T, store *Store, identity string, q sittingQuestion) {
	t.Helper()
	if _, err := store.Queue(Action{ID: "answer-" + q.id, Kind: "cfo_answer", Generation: identity, QuestionID: q.id, Text: q.answer, AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
}

// passes makes every answer still queued one he gave d earlier, as it reads
// once d has passed.
func passes(store *Store, d time.Duration) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for i := range store.db.Actions {
		if a := &store.db.Actions[i]; a.Kind == "cfo_answer" && a.Status == "queued" {
			a.CreatedAt = a.CreatedAt.Add(-d)
		}
	}
}

// answerBatchWindow stands here until the fix declares it in the store.
const answerBatchWindow = 5 * time.Second

// heAnswersTheSitting replays the sitting: each answer of it queued its gap
// after the one before, with the supervisor's worker run between them as a
// queued answer runs it, and returns how many messages the CFO's terminal had
// taken by the time his last answer was in.
func heAnswersTheSitting(t *testing.T, s *Service, identity string, typed func() []string) int {
	t.Helper()
	for i, q := range sitting {
		passes(s.Store, sittingGaps[i])
		heAnswers(t, s.Store, identity, q)
		if i < len(sitting)-1 {
			s.process(context.Background())
		}
	}
	return len(overlordMessages(typed()))
}

// overlordMessages is each message of the board's that lines, which a CFO's
// terminal recorded, show it took, in order and as typed.
func overlordMessages(lines []string) []string {
	var messages []string
	for _, line := range lines {
		if i := strings.Index(line, "Overlord: "); i >= 0 {
			messages = append(messages, line[i:])
		}
	}
	return messages
}

// wantTheSittingAsOneMessage fails unless messages is one message that lists
// every answer of the sitting once, in the order he gave them.
func wantTheSittingAsOneMessage(t *testing.T, messages []string) {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("the CFO got %d messages, want his %d answers as one: %q", len(messages), len(sitting), messages)
	}
	from := 0
	for _, q := range sitting {
		at := strings.Index(messages[0], q.line())
		if strings.Count(messages[0], q.line()) != 1 || at < from {
			t.Errorf("the message is %q, want %q in it once, after the answers he gave before it", messages[0], q.line())
		}
		from = max(from, at)
	}
}

// wantTheSittingAnswered fails unless every answer of the sitting reads
// delivered and its question answered by the Overlord.
func wantTheSittingAnswered(t *testing.T, store *Store) {
	t.Helper()
	snapshot := store.Snapshot()
	for _, q := range sitting {
		a := snapshot.Actions[slices.IndexFunc(snapshot.Actions, func(a Action) bool { return a.QuestionID == q.id })]
		asked := snapshot.Questions[slices.IndexFunc(snapshot.Questions, func(asked Question) bool { return asked.ID == q.id })]
		if a.Status != "succeeded" || a.Awaiting != nil || asked.Status != "succeeded" || asked.AnsweredBy != "overlord" || asked.AnsweredOption != q.answer {
			t.Errorf("the answer to %s reads %s (%s) and its question %s by %q with %q, want it delivered and the question answered by the Overlord with %q", q.id, a.Status, a.Message, asked.Status, asked.AnsweredBy, asked.AnsweredOption, q.answer)
		}
	}
}

func TestFourAnswersGivenWithinSevenSecondsReachTheCFOAsOneMessage(t *testing.T) {
	tests := []struct {
		name string
		// another is a question of the CFO's he leaves unanswered.
		another bool
		// atOnce is how many messages the CFO has the moment his last answer
		// is in.
		atOnce int
	}{
		{"nothing else waits on him, so they go with his last answer", false, 1},
		{"another question still waits on him, so they go once he stops answering", true, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			terminal := nativeCFOComposer(t, home.State, "claude-ready")
			identity := registrationIdentity(t, home.State)
			s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}, work: make(chan struct{}, 1)}
			cfoAsks(t, store, identity, sitting...)
			if test.another {
				cfoAsks(t, store, identity, sittingQuestion{"merge-words-today", "Merge on your word today?", "Merge"})
			}

			// Act
			during := heAnswersTheSitting(t, s, identity, func() []string { return terminal.lines(t) })
			s.process(context.Background())
			atOnce := len(overlordMessages(terminal.lines(t)))
			passes(store, answerBatchWindow)
			s.process(context.Background())

			// Assert
			if during != 0 {
				t.Errorf("the CFO had %d messages while he was still answering, want none before his last answer", during)
			}
			if atOnce != test.atOnce {
				t.Errorf("the CFO had %d messages the moment his last answer was in, want %d", atOnce, test.atOnce)
			}
			wantTheSittingAsOneMessage(t, overlordMessages(terminal.lines(t)))
			wantTheSittingAnswered(t, store)
		})
	}
}

func TestAnAnswerAloneIsNotDelayedPastTheBatchingWindow(t *testing.T) {
	t.Run("an answer to the only question goes at once", func(t *testing.T) {
		// Arrange
		store, home := testStore(t)
		terminal := nativeCFOComposer(t, home.State, "claude-ready")
		identity := registrationIdentity(t, home.State)
		s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}, work: make(chan struct{}, 1)}
		cfoAsks(t, store, identity, sitting[0])

		// Act
		heAnswers(t, store, identity, sitting[0])
		s.process(context.Background())

		// Assert
		if typed := overlordMessages(terminal.lines(t)); len(typed) != 1 || typed[0] != "Overlord: "+sitting[0].line() {
			t.Errorf("the CFO got %q, want %q at once, as an answer alone always read", typed, "Overlord: "+sitting[0].line())
		}
	})
	t.Run("with another question still waiting on him it goes as the window ends", func(t *testing.T) {
		// Arrange
		store, home := testStore(t)
		terminal := nativeCFOComposer(t, home.State, "claude-ready")
		identity := registrationIdentity(t, home.State)
		s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}, work: make(chan struct{}, 1)}
		cfoAsks(t, store, identity, sitting[0], sitting[1])
		const left = 400 * time.Millisecond

		// Act
		heAnswers(t, store, identity, sitting[0])
		s.process(context.Background())
		held := overlordMessages(terminal.lines(t))
		passes(store, answerBatchWindow-left)
		began := time.Now()
		s.process(context.Background())
		early := overlordMessages(terminal.lines(t))
		var waited time.Duration
		select {
		case <-s.work:
			waited = time.Since(began)
		case <-time.After(left + 5*time.Second):
			t.Fatal("the worker was not woken as the window ended, so the answer waits for whichever cycle comes next")
		}
		s.process(context.Background())

		// Assert
		if len(held) != 0 || len(early) != 0 {
			t.Errorf("the CFO got %q and then %q inside the window, want the answer held for his next one", held, early)
		}
		if waited < left-100*time.Millisecond {
			t.Errorf("the worker was woken %s after the window had %s left, want it woken as the window ends", waited, left)
		}
		if typed := overlordMessages(terminal.lines(t)); len(typed) != 1 || typed[0] != "Overlord: "+sitting[0].line() {
			t.Errorf("the CFO got %q, want %q as the window ended, as an answer alone always read", typed, "Overlord: "+sitting[0].line())
		}
		if waiting := store.Snapshot().Questions[1]; waiting.Status != "pending" {
			t.Errorf("the question he left reads %s, want it still waiting on him", waiting.Status)
		}
	})
}

func TestACFOThatIsBusyOrRestartingGetsTheAnswersOnceWhenItsInputIsReady(t *testing.T) {
	t.Run("in a turn", func(t *testing.T) {
		// Arrange
		records := useConversations(t)
		store, home := testStore(t)
		terminal := nativeCFOComposer(t, home.State, "claude-busy")
		identity := registrationIdentity(t, home.State)
		s := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}, work: make(chan struct{}, 1)}
		cfoAsks(t, store, identity, sitting...)

		// Act
		heAnswersTheSitting(t, s, identity, func() []string { return terminal.lines(t) })
		s.process(context.Background())
		typed := overlordMessages(terminal.lines(t))
		sent := store.Snapshot().Actions
		if len(typed) > 0 {
			takenInRecord(t, records, "claude", "session-1", "", typed[0])
		}
		settled := store.settleDeliveries(time.Now().UTC(), s.lookAtTerminal)

		// Assert
		if settled != nil {
			t.Fatal(settled)
		}
		wantTheSittingAsOneMessage(t, typed)
		awaited := map[string]bool{}
		for _, a := range sent {
			if a.Status != "running" || a.Awaiting == nil || a.Message != sentToCFO {
				t.Errorf("behind the CFO's turn the answer to %s reads %s (%s), want it sent and awaiting the CFO", a.QuestionID, a.Status, a.Message)
				continue
			}
			awaited[a.Awaiting.Digest] = true
		}
		if len(awaited) != 1 {
			t.Errorf("the answers await %d typed messages, want the one message they went in", len(awaited))
		}
		wantTheSittingAnswered(t, store)
	})
	t.Run("with a draft in its input", func(t *testing.T) {
		// Arrange
		store, home := testStore(t)
		terminal := nativeCFOComposer(t, home.State, "claude-ready")
		identity := registrationIdentity(t, home.State)
		rule := strings.Repeat("─", 80)
		connection := &CFOConnection{State: home.State, ReadScreen: func(host.Record) ([]string, error) {
			return []string{rule, "❯ unfinished thought", rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
		}}
		s := &Service{Store: store, Options: Options{CFO: connection}, work: make(chan struct{}, 1)}
		cfoAsks(t, store, identity, sitting...)

		// Act
		heAnswersTheSitting(t, s, identity, func() []string { return terminal.lines(t) })
		s.process(context.Background())
		overDraft := overlordMessages(terminal.lines(t))
		// A supervisor that restarts meanwhile still holds every answer.
		reopened, err := Open(home)
		if err != nil {
			t.Fatal(err)
		}
		waiting := reopened.Snapshot().Actions
		s.Store, connection.ReadScreen = reopened, nil
		s.process(context.Background())

		// Assert
		if len(overDraft) != 0 {
			t.Errorf("the CFO got %q over the draft in its input, want nothing typed", overDraft)
		}
		for _, a := range waiting {
			if a.Status != "queued" || a.Awaiting != nil {
				t.Errorf("while the CFO's input held a draft the answer to %s reads %s, want it queued and unsent", a.QuestionID, a.Status)
			}
		}
		wantTheSittingAsOneMessage(t, overlordMessages(terminal.lines(t)))
		wantTheSittingAnswered(t, reopened)
	})
	t.Run("restarting", func(t *testing.T) {
		// Arrange
		s, stateDir, closed := cfoBoard(t)
		cfoAsks(t, s.Store, closed, sitting...)

		// Act
		heAnswersTheSitting(t, s, closed, func() []string { return nil })
		deliver(t, s)
		waiting := s.Store.Snapshot().Actions
		cfo := reopenCFO(t, stateDir)
		deliver(t, s)

		// Assert
		for _, a := range waiting {
			if a.Status != "queued" {
				t.Errorf("with the CFO closed the answer to %s reads %s (%s), want it waiting as queued", a.QuestionID, a.Status, a.Message)
			}
		}
		wantTheSittingAsOneMessage(t, overlordMessages(cfo.waitForLines(t, 2)))
		wantTheSittingAnswered(t, s.Store)
	})
}
