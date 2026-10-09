package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// What only the Overlord can do is never handed to him as a list in the
// CFO's chat: when AFK mode turns off, each line left for him reaches the
// Command Center as the CFO's question, with what is wrong, what was tried
// and his choices. A line the CFO struck as written by mistake asks nothing.
func TestEachLineLeftForHimIsAskedInTheCommandCenterWhenAFKModeTurnsOff(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	left := afk.Entry{
		Kind: afk.KindLeft, What: "Store a new Fly token for PrecisionDocs?", Evidence: "Fly tokens are his. Backlog row cg-fly-token-durable",
		Diagnosis: "the stored token was overwritten at 04:00:21Z by an old .env line, which PR 490 stops",
		Tried:     "cfo auth --fix, which finds no other token to adopt",
		Options:   []string{"Create the token now", "Leave PrecisionDocs parked"}, Recommendation: "Create the token now",
	}
	bogus := afk.Entry{Kind: afk.KindLeft, What: "nothing: CFO note", Evidence: "test", Diagnosis: "none", Tried: "none", Options: []string{"a one", "a two"}}
	for _, entry := range []afk.Entry{left, bogus} {
		if err := LogAFKDecision(h, entry); err != nil {
			t.Fatal(err)
		}
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := afk.Entries(h.State, switched.Session)
	if err := StrikeAFKLine(h, entries[2].At, "a test line the CFO wrote by mistake"); err != nil {
		t.Fatalf("the CFO's strike = %v, want it made", err)
	}
	_, identity, err := readPrimary(h.State)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	s.runRequests.Lock()
	err = s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()

	// Assert
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	var asked []Question
	for _, q := range s.Store.Snapshot().Questions {
		if strings.HasPrefix(q.ID, "afk-left-") {
			asked = append(asked, q)
		}
	}
	if len(asked) != 1 {
		t.Fatalf("questions asked of him = %+v, want the one line left for him and not the struck one", asked)
	}
	q := asked[0]
	if q.Status != "pending" || q.Task != "" || q.Identity != identity || !slices.Equal(q.Options, left.Options) || q.Recommended != left.Recommendation {
		t.Errorf("question = %+v, want the CFO's own pending question with his choices", q)
	}
	for _, want := range []string{left.What, "\n- Why it is yours: " + left.Evidence, "\n- Found: " + left.Diagnosis, "\n- Tried: " + left.Tried} {
		if !strings.Contains(q.Text, want) {
			t.Errorf("question text %q lacks %q", q.Text, want)
		}
	}
}

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

// leftLine is a line left for the Overlord that names what only he can do.
func leftLine(what string) afk.Entry {
	return afk.Entry{
		Kind: afk.KindLeft, What: what, Evidence: "his own sign-in. Backlog row cg-fly-token-durable",
		Diagnosis: "the stored token reads unauthorized", Tried: "cfo auth --fix",
		Options: []string{"I will sign in", "Leave it parked"}, Recommendation: "I will sign in",
	}
}

// leaveAndSettle turns AFK mode on at his ask, leaves each of what for him and
// settles the line at each index of settled with what became of it.
func leaveAndSettle(t *testing.T, h home.Home, what []string, settled map[int]string) []afk.Entry {
	t.Helper()
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	for _, one := range what {
		if err := LogAFKDecision(h, leftLine(one)); err != nil {
			t.Fatal(err)
		}
	}
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, _ := afk.Entries(h.State, switched.Session)
	left := entries[1:]
	for i, how := range settled {
		if err := SettleAFKLine(h, left[i].At, how); err != nil {
			t.Fatalf("the CFO's settle = %v, want it made", err)
		}
	}
	return left
}

// A line left for him that the CFO saw to later in the stretch is settled,
// and is never asked in the Command Center when AFK mode turns off.
func TestASettledLineLeftForHimIsNeverAskedInTheCommandCenter(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	left := leaveAndSettle(t, h, []string{"Sign in to Fly again?", "Finish or abort your parked gate run?"}, map[int]string{1: "the run finished on its own"})

	// Act
	s.runRequests.Lock()
	err := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()

	// Assert
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	var asked []string
	for _, q := range s.Store.Snapshot().Questions {
		if strings.HasPrefix(q.ID, "afk-left-") {
			asked = append(asked, q.ID)
		}
	}
	if want := []string{"afk-left-" + left[0].At.UTC().Format("20060102T150405.000000000Z")}; !slices.Equal(asked, want) {
		t.Errorf("questions asked of him = %q, want only %q: the settled line asks nothing", asked, want)
	}
}

// The report lists what was held for him and what was left for him as one
// list, each as it stands now, open ones first and each in the order it came
// to him: a line left open waits as
// his question in the Command Center until he answers it, and a settled one
// says what became of it.
func TestTheReportListsHeldAndLeftItemsTogetherOpenFirstAsTheyStandNow(t *testing.T) {
	// Arrange
	s, h, _ := asTheCFO(t)
	_, identity, err := readPrimary(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Store.acceptQuestion(Question{ID: "drop-legacy", Identity: identity, Text: "Migration 0042 drops legacy_invoices. Apply it?", Options: []string{"Apply it", "Keep it held"}, Recommended: "Keep it held", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	left := leaveAndSettle(t, h, []string{"Finish or abort your parked gate run?", "Sign in to Fly again?"}, map[int]string{0: "the run finished on its own"})
	if err := s.holdForOverlord(time.Now()); err != nil {
		t.Fatal(err)
	}
	s.runRequests.Lock()
	err = s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()
	if err != nil {
		t.Fatalf("his off = %v", err)
	}
	asked := "question:afk-left-" + left[1].At.UTC().Format("20060102T150405.000000000Z")
	read := func() []afk.Held {
		t.Helper()
		report, found, err := ReadAFKReport(h)
		if err != nil || !found {
			t.Fatalf("ReadAFKReport = %v, %v, want the report", found, err)
		}
		return report.Held
	}
	type stands struct {
		item    string
		waiting bool
		now     string
	}
	got := func(held []afk.Held) []stands {
		var all []stands
		for _, one := range held {
			all = append(all, stands{one.Item, one.Waiting, one.Now})
		}
		return all
	}

	// Act
	before := read()
	s.Store.mu.Lock()
	i := slices.IndexFunc(s.Store.db.Questions, func(q Question) bool { return "question:"+q.ID == asked })
	answered := time.Now().UTC()
	s.Store.db.Questions[i].Status, s.Store.db.Questions[i].AnsweredBy, s.Store.db.Questions[i].Answer, s.Store.db.Questions[i].AnsweredAt = "succeeded", "overlord", "I will sign in", &answered
	err = s.Store.save()
	s.Store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after := read()

	// Assert
	settled := stands{"question:afk-left-" + left[0].At.UTC().Format("20060102T150405.000000000Z"), false, "settled by the CFO: the run finished on its own"}
	if want := []stands{{asked, true, "still waiting on you"}, {"question:drop-legacy", true, "still waiting on you"}, settled}; !slices.Equal(got(before), want) {
		t.Errorf("before he answered, the report lists %+v, want %+v", got(before), want)
	}
	if want := []stands{{"question:drop-legacy", true, "still waiting on you"}, settled, {asked, false, "you answered it: I will sign in"}}; !slices.Equal(got(after), want) {
		t.Errorf("after he answered, the report lists %+v, want %+v", got(after), want)
	}
	if one := before[0]; one.What != "Sign in to Fly again?" || one.Task != "" || one.Recommendation != "I will sign in" {
		t.Errorf("the line left open = %+v, want the CFO's, with what only he can do and its recommendation", one)
	}
}
