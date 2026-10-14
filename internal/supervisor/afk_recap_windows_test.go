package supervisor

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
)

// The Overlord, 2026-10-12 about 19:08Z, after the seven lines left for him
// under AFK mode all came back to him as questions already answered by their
// recommendations: "in AFK mode, you should just answer everything and then
// just tell me what you've done". Under AFK mode the CFO answers every
// decision itself with its recommendation and acts on it, and nothing it logs
// becomes a Command Center question. A step only he can take, his own sign-in
// or a step a tool refuses, is one card in his Command Center once he is back.

// hisStep is a step only the Overlord can take, which a tool refused the CFO.
func hisStep() afk.Entry {
	return afk.Entry{
		Kind: afk.KindHis, What: "Merge code-goblins #800: it is a teammate's pull request, which only you merge",
		Evidence: "cfo pr merge refused it: merging a teammate's pull request stays the Overlord's alone. Backlog row cg-teammate-800",
		Link:     "https://github.com/fpresta0607/code-goblins/pull/800",
	}
}

func TestADecisionLoggedUnderAFKNeverBecomesACommandCenterQuestion(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	decided := decidedLine()
	unanswered := leftLine("Sign in to Fly again?")

	// Act
	decidedErr := LogAFKDecision(h, decided)
	unansweredErr := LogAFKDecision(h, unanswered)
	stepErr := LogAFKDecision(h, hisStep())
	s.runRequests.Lock()
	offErr := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()

	// Assert
	if decidedErr != nil || stepErr != nil || offErr != nil {
		t.Fatalf("the decision = %v, his step = %v, his off = %v, want each taken", decidedErr, stepErr, offErr)
	}
	if unansweredErr == nil || !strings.Contains(unansweredErr.Error(), "nothing is left for him as a question") {
		t.Errorf("a decision logged without the CFO's answer = %v, want it refused: nothing is left for him as a question", unansweredErr)
	}
	var pending []string
	for _, q := range s.Store.Snapshot().Questions {
		if q.Status == "pending" {
			pending = append(pending, q.ID)
		}
	}
	if len(pending) != 1 || !strings.HasPrefix(pending[0], "afk-his-") {
		t.Errorf("questions waiting on him once AFK mode turned off = %q, want only the card of his step, none for a decision", pending)
	}
	if settled := questionAbout(t, s, decided.What); settled.Status != "succeeded" || settled.AnsweredBy != "cfo" || !settled.AnsweredAway || settled.AnsweredOption != decided.Answer {
		t.Errorf("the decision on the board = %+v, want it in History with the CFO's answer, given while he was away", settled)
	}
}

func TestARefusedStepIsOneCardInTheCommandCenterAndTheRecapPointsToIt(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	step := hisStep()

	// Act
	logged := awayAndBack(t, s, h, decidedLine(), step)

	// Assert
	report, found, err := ReadAFKReport(h)
	if err != nil || !found {
		t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
	}
	if len(report.Held) != 1 || report.Held[0].Item != afk.HisCardItem(logged[1]) || !report.Held[0].Waiting || report.Held[0].What != step.What {
		t.Errorf("held for him = %+v, want his step as one card, waiting", report.Held)
	}
	if n := slices.IndexFunc(report.Sections(), func(section afk.Section) bool { return section.Group == afk.GroupNeeds }); n >= 0 {
		t.Errorf("sections = %+v, want no Needs you line: its card tells the step", report.Sections())
	}
	var text bytes.Buffer
	if err := afk.Render(&text, report); err != nil {
		t.Fatal(err)
	}
	if want := "\nNEEDS YOU\n\nWaiting on you in the Command Center (1), each as it stands now\n- " + afk.HisCardItem(logged[1]) + ", the CFO's: " + step.What + "\n  Now: still waiting on you.\n"; !strings.HasSuffix(text.String(), want) {
		t.Errorf("the CFO's text of the report ends:\n%s\nwant it to end with his one card:\n%s", text.String(), want)
	}
}
