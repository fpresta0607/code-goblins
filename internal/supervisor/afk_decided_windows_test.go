package supervisor

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The Overlord, 2026-10-12 about 11:55Z: "Anything the CFO has already
// answered should not appear as a [line] for you in the AFK report. It should
// just be something that was already ... settled in history, the command
// center". Under AFK mode the CFO answers a decision itself and acts on its
// answer. When AFK mode turns off that decision is settled: it is in the
// Command Center's History with the CFO's answer, who gave it and when, and
// never in his queue or under For you in the report, which gives it a recap
// of its own. What only he can do is still asked, with nothing checked.

// decidedLine is a decision the CFO answered itself under AFK mode.
func decidedLine() afk.Entry {
	decided := leftLine("Which fallback does the gate policy take?")
	decided.Options, decided.Recommendation, decided.Answer = []string{"Drop it from the machine config", "Take it into the policy"}, "", "Drop it from the machine config"
	return decided
}

// awayAndBack turns AFK mode on at his ask, logs each of lines, and turns it
// off from his own board. It returns the lines as the log holds them.
func awayAndBack(t *testing.T, s *Service, h home.Home, lines ...afk.Entry) []afk.Entry {
	t.Helper()
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if err := LogAFKDecision(h, line); err != nil {
			t.Fatal(err)
		}
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := afk.Entries(h.State, switched.Session)
	if err != nil {
		t.Fatal(err)
	}
	s.runRequests.Lock()
	err = s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	return entries[1:]
}

// questionAbout is the board's question whose first line is what.
func questionAbout(t *testing.T, s *Service, what string) Question {
	t.Helper()
	i := slices.IndexFunc(s.Store.Snapshot().Questions, func(q Question) bool { return strings.HasPrefix(q.Text, what+"\n") })
	if i < 0 {
		t.Fatalf("the board holds no question about %q: %+v", what, s.Store.Snapshot().Questions)
	}
	return s.Store.Snapshot().Questions[i]
}

func TestADecisionTheCFOAnsweredIsSettledInHistoryAndWhatIsHisIsStillAsked(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	_, identity, err := readPrimary(h.State)
	if err != nil {
		t.Fatal(err)
	}
	decided := decidedLine()

	// Act
	logged := awayAndBack(t, s, h, decided, leftLine("Sign in to Fly again?"))

	// Assert
	settled := questionAbout(t, s, decided.What)
	if settled.ID != afk.LeftQuestionID(logged[0]) || settled.Status != "succeeded" || settled.Identity != identity || settled.Task != "" || !slices.Equal(settled.Options, decided.Options) {
		t.Errorf("the decision on the board = %+v, want the CFO's own question %s, closed", settled, afk.LeftQuestionID(logged[0]))
	}
	if settled.AnsweredBy != "cfo" || !settled.AnsweredAway || settled.AnsweredOption != decided.Answer || settled.Answer != decided.Answer || settled.AnswerKind != "option" || settled.AnsweredAt == nil || !settled.AnsweredAt.Equal(logged[0].At) {
		t.Errorf("the decision's answer = %+v, want the CFO's answer, given while he was away when it was logged at %s", settled, logged[0].At)
	}
	if !strings.Contains(settled.Text, "\n- Evidence: ") {
		t.Errorf("the decision reads %q, want what the CFO's answer stood on", settled.Text)
	}
	asked := questionAbout(t, s, "Sign in to Fly again?")
	if asked.Status != "pending" || asked.AnsweredBy != "" || !strings.Contains(asked.Text, "\n- Why it is yours: ") {
		t.Errorf("his sign-in = %+v, want it asked of him with nothing answered", asked)
	}
	report, found, err := ReadAFKReport(h)
	if err != nil || !found {
		t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
	}
	var forYou []string
	for _, one := range report.Held {
		forYou = append(forYou, one.What)
	}
	if !slices.Equal(forYou, []string{"Sign in to Fly again?"}) {
		t.Errorf("For you = %q, want only his sign-in", forYou)
	}
	sections := report.Sections()
	i := slices.IndexFunc(sections, func(section afk.Section) bool { return section.Title == "Answered for you" })
	if i < 0 || len(sections[i].Entries) != 1 || sections[i].Entries[0].What != decided.What || sections[i].Entries[0].Answer != decided.Answer {
		t.Errorf("the report's sections = %+v, want the decision recapped under Answered for you with the CFO's answer", sections)
	}
	var text bytes.Buffer
	if err := afk.Render(&text, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"For you (1), each as it stands now\n- question:afk-left-", "\nAnswered for you (1)\n- Which fallback does the gate policy take?\n  The CFO answered: Drop it from the machine config. You can change it in History.\n"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the CFO's text of the report lacks %q:\n%s", want, text.String())
		}
	}
}

// His change to a decision the CFO settled, made from History, reaches the
// CFO once, as a change from the CFO's answer to his, and choosing it again
// sends nothing more.
func TestHisChangeToASettledDecisionReachesTheCFOOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, cfo, connection := primaryFixture(t, store)
	for _, name := range []string{"CFO_ROLE", "NO_MISTAKES_GATE"} {
		t.Setenv(name, "")
	}
	s := &Service{Store: store, Options: Options{CFO: connection}}
	runPipe(t, s)
	decided := decidedLine()
	awayAndBack(t, s, h, decided)
	settled := questionAbout(t, s, decided.What)

	// Act
	_, err := change(store, "change-1", settled, "Take it into the policy")
	if err == nil {
		runAll(t, store, s)
		_, err = change(store, "change-2", questionAbout(t, s, decided.What), "Take it into the policy")
		runAll(t, store, s)
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	told := changeLines(cfo.waitForLines(t, 1))
	if len(told) != 1 || !strings.Contains(told[0], settled.ID) {
		t.Fatalf("the CFO received %q, want one change to its question %s", told, settled.ID)
	}
	wantTheChange(t, told[0], decided.Answer, "Take it into the policy")
	if strings.Contains(told[0], "his answer") {
		t.Errorf("the change reads %q, but the answer it replaces was the CFO's", told[0])
	}
	wantHisAnswer(t, questionAbout(t, s, decided.What), "Take it into the policy", decided.Answer)
}
