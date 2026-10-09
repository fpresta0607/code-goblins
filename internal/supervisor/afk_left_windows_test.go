package supervisor

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
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
