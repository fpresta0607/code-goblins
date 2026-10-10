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
// answered them. Their words and his choices stand in for the real ones.
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

// wantTheSittingAsOneMessage fails unless messages is the one message that
// says how many answers the sitting has and lists each once, numbered in the
// order he gave them.
func wantTheSittingAsOneMessage(t *testing.T, messages []string) {
	t.Helper()
	want := fmt.Sprintf("Overlord: %d user answers to CFO questions, in the order he gave them.", len(sitting))
	for i, q := range sitting {
		want += fmt.Sprintf(" [%d/%d] %s", i+1, len(sitting), q.line())
	}
	if len(messages) != 1 || messages[0] != want {
		t.Errorf("the CFO got %d messages, %q, want his %d answers as the one message %q", len(messages), messages, len(sitting), want)
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

		// Act
		given := time.Now()
		heAnswers(t, store, identity, sitting[0])
		s.process(context.Background())
		held := overlordMessages(terminal.lines(t))
		// A wake that comes a moment early is followed by another, as the
		// worker asks again for the time that is left.
		var woken time.Time
		for len(overlordMessages(terminal.lines(t))) == 0 {
			select {
			case <-s.work:
				if woken.IsZero() {
					woken = time.Now()
				}
			case <-time.After(answerBatchWindow + 10*time.Second):
				t.Fatal("the worker was not woken as the window ended, so the answer waits for whichever cycle comes next")
			}
			s.process(context.Background())
		}

		// Assert
		if len(held) != 0 {
			t.Errorf("the CFO got %q inside the window, want the answer held for his next one", held)
		}
		if ends := given.Add(answerBatchWindow); woken.Before(ends.Add(-20 * time.Millisecond)) {
			t.Errorf("the worker was woken %s before the window ended, want it woken as the window ends", ends.Sub(woken))
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

// runsWhatCanRun runs every action that can run now, as the supervisor's
// worker does, and returns what each run was handed: its kind, its question
// and the questions of the answers that went with it.
func runsWhatCanRun(t *testing.T, store *Store) []string {
	t.Helper()
	var ran []string
	for range maxActions {
		if !store.HasRunnable() {
			break
		}
		err := store.ProcessOne(context.Background(), func(_ context.Context, a Action) (Evaluation, error) {
			handed := strings.TrimSpace(a.Kind + " " + a.QuestionID)
			for _, with := range a.With {
				handed += " + " + with.QuestionID
			}
			ran = append(ran, handed)
			return Evaluation{Reason: "Taken by the CFO."}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return ran
}

// What he sends the CFO keeps the order he sent it in: an answer never waits
// behind a message of his own that he sent after it, and an answer he gave
// after that message never goes ahead of it.
func TestHisMessageToTheCFOKeepsItsPlaceAmongHisAnswers(t *testing.T) {
	// Arrange: the CFO runs, since a delivery to a closed one waits, and a
	// third question still waits on him, so his answers wait for his next.
	store, home := testStore(t)
	writeRegistration(t, home.State, thisProcess(t))
	identity := registrationIdentity(t, home.State)
	cfoAsks(t, store, identity, sitting[0], sitting[1], sitting[2])

	// Act
	heAnswers(t, store, identity, sitting[0])
	if _, err := store.Queue(Action{ID: "message-1", Kind: "message", Text: "Hold the merge until I say."}); err != nil {
		t.Fatal(err)
	}
	heAnswers(t, store, identity, sitting[1])
	atOnce := runsWhatCanRun(t, store)
	passes(store, answerBatchWindow)
	later := runsWhatCanRun(t, store)

	// Assert
	if want := []string{"cfo_answer " + sitting[0].id, "message"}; !slices.Equal(atOnce, want) {
		t.Errorf("at once the CFO was handed %q, want %q: his first answer, which nothing can join ahead of his message, then the message", atOnce, want)
	}
	if want := []string{"cfo_answer " + sitting[1].id}; !slices.Equal(later, want) {
		t.Errorf("once the window ended the CFO was handed %q, want %q: the answer he gave after his message", later, want)
	}
}

// Answers typed as one message were all typed or none was, so a supervisor
// that stops while they are typed leaves each one uncertain, and none is
// typed again.
func TestAnswersGoingAsOneMessageAllReadUncertainAfterASupervisorThatStoppedMidSend(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	writeRegistration(t, home.State, thisProcess(t))
	identity := registrationIdentity(t, home.State)
	cfoAsks(t, store, identity, sitting[0], sitting[1])
	heAnswers(t, store, identity, sitting[0])
	heAnswers(t, store, identity, sitting[1])

	// Act: the supervisor that starts again reads what the first had saved
	// by the time it typed.
	var restarted *Store
	err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		var err error
		restarted, err = Open(home)
		return Evaluation{Reason: "Taken by the CFO."}, err
	})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range restarted.Snapshot().Actions {
		if a.Status != "uncertain" {
			t.Errorf("after the restart the answer to %s reads %s (%s), want it uncertain", a.QuestionID, a.Status, a.Message)
		}
	}
	if restarted.HasRunnable() {
		t.Error("after the restart an answer can run again, want none typed twice")
	}
}
