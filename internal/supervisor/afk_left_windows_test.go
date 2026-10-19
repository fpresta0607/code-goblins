package supervisor

import (
	"slices"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// The CFO strikes a line it logged by mistake over the supervisor's pipe, and
// the report shows it struck with the reason.
func TestTheCFOStrikesALineItLoggedByMistakeOverThePipe(t *testing.T) {
	// Arrange
	_, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	if err := LogAFKDecision(h, afk.Entry{Kind: afk.KindOther, What: "nothing: CFO note", Evidence: "test"}); err != nil {
		t.Fatal(err)
	}
	switched, _ := afk.Read(h.State)
	entries, _, _ := afk.Entries(h.State, switched.Session)

	// Act
	err := StrikeAFKLine(h, entries[1].At, "a test line the CFO wrote by mistake")

	// Assert
	if err != nil {
		t.Fatalf("StrikeAFKLine = %v, want the line struck", err)
	}
	entries, _, _ = afk.Entries(h.State, switched.Session)
	if decisions := afk.Decisions(entries); len(entries) != 3 || len(decisions) != 1 || decisions[0].Struck != "a test line the CFO wrote by mistake" {
		t.Errorf("log = %+v, want the line kept and struck with the reason", entries)
	}
	if err := StrikeAFKLine(h, time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), "no such line"); err == nil {
		t.Error("a strike of a line the log does not hold was made")
	}
}

// leftLine is a decision the CFO would have asked the Overlord, with what is
// wrong, what was tried and its choices, and no answer yet.
func leftLine(what string) afk.Entry {
	return afk.Entry{
		Kind: afk.KindLeft, What: what, Evidence: "his own sign-in. Backlog row cg-fly-token-durable",
		Diagnosis: "the stored token reads unauthorized", Tried: "cfo auth --fix",
		Options: []string{"I will sign in", "Leave it parked"}, Recommendation: "I will sign in",
	}
}

// stepsAndSettle turns AFK mode on at his ask, logs each of what as a step
// only he can take and settles the step at each index of settled with what
// became of it.
func stepsAndSettle(t *testing.T, h home.Home, what []string, settled map[int]string) []afk.Entry {
	t.Helper()
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	for _, one := range what {
		if err := LogAFKDecision(h, afk.Entry{Kind: afk.KindHis, What: one, Link: "https://fly.io/app/sign-in", Evidence: "his own sign-in. Backlog row cg-fly-token-durable"}); err != nil {
			t.Fatal(err)
		}
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := afk.Entries(h.State, switched.Session)
	steps := entries[1:]
	for i, how := range settled {
		if err := SettleAFKLine(h, steps[i].At, how); err != nil {
			t.Fatalf("the CFO's settle = %v, want it made", err)
		}
	}
	return steps
}

// A step only he could take that the CFO saw to later in the stretch is
// settled: his recap lists it with what became of it under what was done, and
// only the step still open needs him.
func TestAStepTheCFOSettledNoLongerNeedsHim(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	steps := stepsAndSettle(t, h, []string{"Sign in to Fly again", "Finish or abort your parked gate run"}, map[int]string{1: "the run finished on its own"})

	// Act
	s.runRequests.Lock()
	err := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()

	// Assert
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	report, found, err := ReadAFKReport(h)
	if err != nil || !found {
		t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
	}
	under := map[string][]string{}
	for _, section := range report.Sections() {
		for _, entry := range section.Entries {
			under[section.Title] = append(under[section.Title], entry.What+" / "+entry.Settled)
		}
	}
	if len(under["Needs you"]) != 0 {
		t.Errorf("Needs you = %q, want no line: the step still open is a card", under["Needs you"])
	}
	if len(report.Held) != 1 || report.Held[0].Item != afk.HisCardItem(steps[0]) || !report.Held[0].Waiting {
		t.Errorf("held = %+v, want the step still open held as its card", report.Held)
	}
	if want := []string{steps[1].What + " / the run finished on its own"}; !slices.Equal(under["Settled by the CFO"], want) {
		t.Errorf("Settled by the CFO = %q, want the settled step with what became of it %q", under["Settled by the CFO"], want)
	}
	if questions, runs := cardsOf(s); len(questions) != 1 || len(runs) != 0 || questions[0].ID != afk.HisCardID(steps[0]) {
		t.Errorf("the board holds %d questions and %d runs, want one card, for the step still open and none for the settled one", len(questions), len(runs))
	}
}

// What waited on him in the Command Center while he was away, such as a
// question asked of him before AFK mode turned on, is in the report as it
// stands now, those that still wait on him first and each in the order it
// came to him.
func TestTheReportListsWhatWaitedOnHimInTheCommandCenterAsItStandsNow(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	_, identity, err := readPrimary(h.State)
	if err != nil {
		t.Fatal(err)
	}
	asked := time.Now().UTC()
	for i, q := range []Question{
		{ID: "keep-branch", Text: "Keep the branch fix/tax-rounding-v1?", Options: []string{"Keep it", "Delete it"}, Recommended: "Keep it"},
		{ID: "drop-legacy", Text: "Migration 0042 drops legacy_invoices. Apply it?", Options: []string{"Apply it", "Keep it held"}, Recommended: "Keep it held"},
	} {
		q.Identity, q.CreatedAt = identity, asked.Add(time.Duration(i)*time.Second)
		if err := s.Store.acceptQuestion(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	s.runRequests.Lock()
	err = s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	read := func() []string {
		t.Helper()
		report, found, err := ReadAFKReport(h)
		if err != nil || !found {
			t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
		}
		var stands []string
		for _, one := range report.Held {
			stands = append(stands, one.Item+": "+one.Now)
		}
		return stands
	}

	// Act
	before := read()
	s.Store.mu.Lock()
	i := slices.IndexFunc(s.Store.db.Questions, func(q Question) bool { return q.ID == "keep-branch" })
	answered := time.Now().UTC()
	s.Store.db.Questions[i].Status, s.Store.db.Questions[i].AnsweredBy, s.Store.db.Questions[i].Answer, s.Store.db.Questions[i].AnsweredAt = "succeeded", "overlord", "Keep it", &answered
	err = s.Store.save()
	s.Store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after := read()

	// Assert
	if want := []string{"question:keep-branch: still waiting on you", "question:drop-legacy: still waiting on you"}; !slices.Equal(before, want) {
		t.Errorf("before he answered, the report lists %q, want %q", before, want)
	}
	if want := []string{"question:drop-legacy: still waiting on you", "question:keep-branch: you answered it: Keep it"}; !slices.Equal(after, want) {
		t.Errorf("after he answered, the report lists %q, want %q", after, want)
	}
}
