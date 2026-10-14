package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// The 13:18Z report of 2026-10-14 left eight steps only the Overlord can take,
// and none became a card: the CFO filed each by hand. When AFK mode turns off,
// each step still open becomes one Command Center card: a run item when the
// step is a command he runs, otherwise a question with its plain steps, its
// link and the choices Done and Later.

func hisPlainStep() afk.Entry {
	return afk.Entry{Kind: afk.KindHis, What: "Sign in to pocketpiggies.app and check that your dashboard loads", Link: "https://pocketpiggies.app/assistant", Evidence: "PR 140 is live and only his own session can check it. Backlog row 2026-10-13"}
}

func hisCommandStep() afk.Entry {
	return afk.Entry{Kind: afk.KindHis, What: "Let the fleet read what GitHub Actions bills you", Evidence: "the fleet token lacks the user scope", Command: "gh auth refresh -h github.com -s user\n", Shell: "powershell"}
}

func cardsOf(s *Service) (questions []Question, runs []Run) {
	for _, q := range s.Store.Snapshot().Questions {
		if strings.HasPrefix(q.ID, "afk-his-") {
			questions = append(questions, q)
		}
	}
	for _, r := range s.Store.Snapshot().Runs {
		if strings.HasPrefix(r.ID, "afk-his-") {
			runs = append(runs, r)
		}
	}
	return questions, runs
}

func TestEachOpenStepOnlyHeCanTakeBecomesOneCommandCenterCardWhenAFKTurnsOff(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	plain, command := hisPlainStep(), hisCommandStep()

	// Act
	logged := awayAndBack(t, s, h, plain, command)
	questions, runs := cardsOf(s)

	// Assert
	if len(questions) != 1 || len(runs) != 1 {
		t.Fatalf("cards = %d questions and %d runs, want one question for the plain step and one run for the command", len(questions), len(runs))
	}
	q := questions[0]
	if q.ID != afk.HisCardID(logged[0]) || q.Status != "pending" || q.Task != "" || !slices.Equal(q.Options, []string{"Done", "Later"}) {
		t.Errorf("question = %+v, want the CFO's own pending card with the choices Done and Later", q)
	}
	if !strings.HasPrefix(q.Text, plain.What+"\n") || !strings.Contains(q.Text, "`"+plain.Link+"`") {
		t.Errorf("question text = %q, want the step in plain words and its link", q.Text)
	}
	r := runs[0]
	if r.ID != afk.HisCardID(logged[1]) || r.State != "ready" || r.Title != command.What || r.Command != command.Command || r.Shell != "powershell" || r.By != "cfo" {
		t.Errorf("run = %+v, want the command as a ready run item titled with the step", r)
	}
	report, found, err := ReadAFKReport(h)
	if err != nil || !found {
		t.Fatalf("ReadAFKReport = %v, %v", found, err)
	}
	var waiting []string
	for _, held := range report.Held {
		if held.Waiting {
			waiting = append(waiting, held.Item)
		}
	}
	slices.Sort(waiting)
	if want := []string{afk.HisCardItem(logged[0]), afk.HisCardItem(logged[1])}; !slices.Equal(waiting, want) {
		t.Errorf("held = %q, want each card held for him", waiting)
	}
	for _, section := range report.Sections() {
		if section.Group == afk.GroupNeeds {
			t.Errorf("the report still lists %q as lines, want the cards to tell them", section.Title)
		}
	}
}

func TestAStepIsOneCardHoweverOftenAFKTurnsOffOverIt(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	awayAndBack(t, s, h, hisPlainStep(), hisCommandStep())
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	again, failed := s.cardHisSteps(switched.Session, time.Now().UTC())
	questions, runs := cardsOf(s)

	// Assert
	if len(failed) != 0 || len(again) != 2 || len(questions) != 1 || len(runs) != 1 {
		t.Errorf("again = %d, failed = %q, cards = %d questions and %d runs, want the same two cards", len(again), failed, len(questions), len(runs))
	}
}

func TestAStepThatWasSettledOrStruckBecomesNoCard(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	for _, step := range []afk.Entry{hisPlainStep(), hisCommandStep(), {Kind: afk.KindHis, What: "A step logged twice", Evidence: "his own sign-in"}} {
		if err := LogAFKDecision(h, step); err != nil {
			t.Fatal(err)
		}
	}
	switched, _ := afk.Read(h.State)
	entries, _, _ := afk.Entries(h.State, switched.Session)
	steps := entries[1:]
	if err := SettleAFKLine(h, steps[0].At, "its backlog row is done"); err != nil {
		t.Fatal(err)
	}
	if err := StrikeAFKLine(h, steps[1].At, "logged by mistake"); err != nil {
		t.Fatal(err)
	}

	// Act
	s.runRequests.Lock()
	err := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()
	questions, runs := cardsOf(s)

	// Assert
	if err != nil || len(questions) != 1 || len(runs) != 0 || questions[0].ID != afk.HisCardID(steps[2]) {
		t.Errorf("off = %v, cards = %d questions and %d runs, want only the step still open as a card", err, len(questions), len(runs))
	}
}

func TestACardTheOverlordClosesSettlesItsStepInTheLogOnce(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	_, identity, err := readPrimary(h.State)
	if err != nil {
		t.Fatal(err)
	}
	logged := awayAndBack(t, s, h, hisPlainStep())
	card := afk.HisCardID(logged[0])
	if _, err := s.Store.Queue(Action{ID: "board-answer", Kind: "cfo_answer", Generation: identity, QuestionID: card, Text: "Done", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	runAll(t, s.Store, s)

	// Act
	first := s.settleHisCards(time.Now().UTC())
	second := s.settleHisCards(time.Now().UTC())
	entries, _, err := afk.Entries(h.State, "")
	report, found, readErr := ReadAFKReport(h)

	// Assert
	if first != nil || second != nil || err != nil || readErr != nil || !found {
		t.Fatalf("settle = %v and %v, log = %v, report = %v %v", first, second, err, found, readErr)
	}
	settles := 0
	for _, entry := range entries {
		if entry.Kind == afk.KindSettle {
			settles++
		}
	}
	if settles != 1 {
		t.Errorf("the log holds %d settle lines, want one: a card settles its step once", settles)
	}
	if afk.NeedsHim(report.Decisions[0]) || report.Decisions[0].Settled == "" {
		t.Errorf("the step = %+v, want it settled by its card", report.Decisions[0])
	}
	if len(report.Held) != 1 || report.Held[0].Waiting || !strings.Contains(report.Held[0].Now, "Done") {
		t.Errorf("held = %+v, want its card closed with his answer Done", report.Held)
	}
}

func TestACardStillWaitingSettlesNothing(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	awayAndBack(t, s, h, hisPlainStep())

	// Act
	err := s.settleHisCards(time.Now().UTC())
	entries, _, _ := afk.Entries(h.State, "")

	// Assert
	for _, entry := range entries {
		if entry.Kind == afk.KindSettle {
			t.Errorf("the log holds a settle %+v for a card that still waits", entry)
		}
	}
	if err != nil {
		t.Error(err)
	}
}
