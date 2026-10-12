package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// mockupsPage is the link of a goblin's Scrawl page, as lavish-axi gives it.
const mockupsPage = "http://127.0.0.1:4387/session/f26e"

// mockupsChoices is the data-lavish-choices block of a page that asks for his
// yes on its pictures.
const mockupsChoices = `<script type="application/json" data-lavish-choices>
  {"id": "mockups", "asker": "task-1", "question": "Build all three as shown?", "options": ["Build it as shown", "Change it"], "recommended": "Build it as shown"}
</script>`

// pageFile writes a goblin's Scrawl page holding body and returns its file.
func pageFile(t *testing.T, body string) string {
	t.Helper()
	page := filepath.Join(t.TempDir(), "mockups.html")
	if err := os.WriteFile(page, []byte("<html><body><main class=\"page\"><h1>Three mockups</h1></main>"+body+"</body></html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	return page
}

// pageFixture is a home with a registered CFO, its pipe served, and native
// goblin task-1, whose terminal this test process stands in for.
func pageFixture(t *testing.T) (*Service, home.Home, state.TaskMeta, hostedTerminal, *CFOConnection) {
	t.Helper()
	s, h, _ := asTheCFO(t)
	meta := makeNative(t, h.State, "task-1")
	meta.Harness = "claude"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return s, h, meta, goblinTerminal(t, h.State, meta.ID), &CFOConnection{State: h.State}
}

// awayPage turns AFK mode on at his ask and has task-1 put page in a review
// item, which AFK mode makes the CFO's decision, and returns its question as
// the board holds it and the notify that woke the CFO.
func awayPage(t *testing.T, s *Service, h home.Home, page string) (Question, wake.Record) {
	t.Helper()
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(h, "task-1", "task-1-mockups", "Approve the three mockups", mockupsPage, page, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	questions := s.Store.Snapshot().Questions
	records, err := wake.Pending(h.State)
	if err != nil || len(questions) != 1 || len(records) != 2 {
		t.Fatalf("questions = %+v, wakes = %+v (%v), want the page as one question and its decision after the switch's notice", questions, records, err)
	}
	return questions[0], records[1]
}

// While AFK mode is on a goblin's page reaches the CFO as a decision: a notify
// that asks the CFO with the page's link and its choices, and the goblin's
// question on the board, which is the CFO's to answer. Nothing of it waits in
// his Command Center, and a retry of the same item asks nothing twice. While
// AFK mode is off the page is his item, as before.
func TestWhileAFKModeIsOnAGoblinsPageIsTheCFOsDecision(t *testing.T) {
	for name, isAFKOn := range map[string]bool{"AFK mode on": true, "AFK mode off": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, meta, _, _ := pageFixture(t)
			page := pageFile(t, mockupsChoices)
			if isAFKOn {
				if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			first := PublishReview(h, meta.ID, "task-1-mockups", "Approve the three mockups", mockupsPage, page, nil)
			again := PublishReview(h, meta.ID, "task-1-mockups", "Approve the three mockups", mockupsPage, page, nil)
			ingestErr := errors.Join(s.Store.ingestReviews(), s.Store.ingestQuestions())

			// Assert
			if first != nil || again != nil || ingestErr != nil {
				t.Fatalf("publish = %v, again = %v, ingest = %v", first, again, ingestErr)
			}
			snapshot := s.Store.Snapshot()
			records, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			decisions := slices.DeleteFunc(records, func(r wake.Record) bool { return r.Kind != "notify" })
			if !isAFKOn {
				if len(snapshot.Reviews) != 1 || snapshot.Reviews[0].State != "open" || snapshot.Reviews[0].LavishPage != page || len(snapshot.Questions) != 0 || len(decisions) != 0 {
					t.Fatalf("reviews = %+v, questions = %+v, decisions = %+v, want his open item with its page and nothing for the CFO", snapshot.Reviews, snapshot.Questions, decisions)
				}
				return
			}
			want := "blocked: Approve the three mockups (page " + mockupsPage + ") options: Build it as shown (Recommended) | Change it"
			if len(decisions) != 1 || decisions[0].Key != meta.ID || decisions[0].Detail != want {
				t.Fatalf("decisions = %+v, want one notify from %s: %q", decisions, meta.ID, want)
			}
			if question, options, asked := wake.Question(decisions[0]); !asked || question != "Approve the three mockups (page "+mockupsPage+")" || len(options) != 2 {
				t.Errorf("the decision reads as %q %q (%v), want the question with its page and both choices", question, options, asked)
			}
			if len(snapshot.Reviews) != 0 {
				t.Errorf("reviews = %+v, want no item in his Command Center", snapshot.Reviews)
			}
			if len(snapshot.Questions) != 1 {
				t.Fatalf("questions = %+v, want the goblin's one question", snapshot.Questions)
			}
			q := snapshot.Questions[0]
			if q.ID != fmt.Sprintf("notify-%s-%d", meta.ID, decisions[0].Seq) || q.Task != meta.ID || !slices.Equal(q.Options, []string{"Build it as shown", "Change it"}) || q.Recommended != "Build it as shown" {
				t.Errorf("the question = %+v, want the goblin's, with the page's choices", q)
			}
			if waiting := waitingOnOverlord(snapshot); len(waiting) != 0 {
				t.Errorf("waiting on him = %+v, want nothing", waiting)
			}
		})
	}
}

// The CFO answers the page with cfo answer: the goblin gets the answer once,
// the notify reads answered, the question closes as the CFO's answer while he
// was away, the goblin's page items still open close with it, and the AFK log
// keeps the answer with the page, the question and the CFO's choice.
func TestTheCFOsAnswerToAGoblinsPageReachesItOnceAndClosesItsItems(t *testing.T) {
	// Arrange
	s, h, meta, goblin, connection := pageFixture(t)
	page := pageFile(t, mockupsChoices)
	before := time.Now().UTC()
	earlier := Review{ID: "waiting-task-1-1", Identity: goblinIdentity(meta), Task: meta.ID, Title: "Waiting on you: the first mockups (page " + mockupsPage + ")", Lavish: mockupsPage, LavishPage: page, State: "open", CreatedAt: before, UpdatedAt: before}
	if err := s.Store.acceptReview(earlier); err != nil {
		t.Fatal(err)
	}
	q, record := awayPage(t, s, h, page)

	// Act
	chosen, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Build it as shown", "the grid reads at his window size")
	_, _, againErr := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Change it", "")

	// Assert
	if err != nil || chosen != "Build it as shown" {
		t.Fatalf("AnswerGoblin = %q, %v, want the choice delivered", chosen, err)
	}
	if againErr == nil {
		t.Error("a second answer to the same page was taken, want it refused")
	}
	if typed := goblin.lines(t); len(typed) != 1 || !strings.Contains(typed[0], fmt.Sprintf("decision %d: Build it as shown. the grid reads at his window size", record.Seq)) {
		t.Errorf("the goblin received %q, want the CFO's answer once", typed)
	}
	if pending, err := wake.Pending(h.State); err != nil || !slices.ContainsFunc(pending, func(r wake.Record) bool { return r.Seq == record.Seq && r.Answered != "" }) {
		t.Errorf("wakes = %+v (%v), want the decision read as answered", pending, err)
	}
	if err := s.Store.ingestAnswers(); err != nil {
		t.Fatal(err)
	}
	snapshot := s.Store.Snapshot()
	if got := snapshot.Questions[0]; got.ID != q.ID || got.Status != "succeeded" || got.AnsweredBy != "cfo" || !got.AnsweredAway || got.AnsweredOption != "Build it as shown" {
		t.Errorf("the question = %+v, want it closed as the CFO's answer while he was away", got)
	}
	if len(snapshot.Reviews) != 1 || snapshot.Reviews[0].State == "open" {
		t.Errorf("reviews = %+v, want the goblin's earlier page item closed", snapshot.Reviews)
	}
	entries := afkEntries(t, h.State)
	logged := entries[len(entries)-1]
	if logged.Kind != afk.KindAnswer || logged.What != q.ID || logged.Task != meta.ID || logged.Link != mockupsPage || !strings.Contains(logged.Evidence, "asked: "+q.Text+" answered: Build it as shown. the grid reads") {
		t.Errorf("logged = %+v, want the answer with its page, its question and the CFO's choice", logged)
	}
}

// A page the CFO answered in his place is settled once AFK mode turns off, as
// the Overlord asked on 2026-10-12: it stays in History as the goblin's
// question with the CFO's answer, given while he was away, where he can
// change it, and it is never asked of him again, nor listed under For you.
// The report recaps it under Answered for goblins.
func TestAPageTheCFOAnsweredIsSettledInHistoryAndNeverAsked(t *testing.T) {
	// Arrange
	s, h, _, _, connection := pageFixture(t)
	q, record := awayPage(t, s, h, pageFile(t, mockupsChoices))
	if _, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Build it as shown", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ingestAnswers(); err != nil {
		t.Fatal(err)
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
	questions := s.Store.Snapshot().Questions
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want only the goblin's, asked nothing more", questions)
	}
	if got := questions[0]; got.ID != q.ID || got.Status != "succeeded" || got.AnsweredBy != "cfo" || !got.AnsweredAway || got.AnsweredOption != "Build it as shown" {
		t.Errorf("the goblin's question = %+v, want it in History as the CFO's answer while he was away", got)
	}
	if len(report.Held) != 0 {
		t.Errorf("For you = %+v, want nothing: the CFO answered the page", report.Held)
	}
	i := slices.IndexFunc(report.Sections(), func(section afk.Section) bool { return section.Title == "Answered for goblins" })
	if i < 0 || len(report.Sections()[i].Entries) != 1 || report.Sections()[i].Entries[0].What != q.ID {
		t.Errorf("the report's sections = %+v, want the page's answer under Answered for goblins", report.Sections())
	}
}

// A page that declares no choices still reaches the CFO as a decision, which
// it answers in its own words, and History keeps that answer as the goblin's
// question's, written. A goblin's question without choices and without a
// page is answered with cfo send, as before.
func TestAPageThatDeclaresNoChoicesTakesTheCFOsOwnWords(t *testing.T) {
	// Arrange
	s, h, meta, goblin, connection := pageFixture(t)
	_, record := awayPage(t, s, h, pageFile(t, ""))
	plain, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which store should the cache use?")
	if err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Ship the three mockups as shown", "")
	_, _, plainErr := connection.AnswerGoblin(context.Background(), fmt.Sprint(plain.Seq), "Postgres", "")
	if err := s.Store.ingestAnswers(); err != nil {
		t.Fatal(err)
	}
	s.runRequests.Lock()
	offErr := s.switchAFKAs("his own board (goblins-window.exe pid 4242)", "", false)
	s.runRequests.Unlock()

	// Assert
	if err != nil || chosen != "Ship the three mockups as shown" {
		t.Fatalf("AnswerGoblin = %q, %v, want the CFO's words delivered", chosen, err)
	}
	if plainErr == nil || !strings.Contains(plainErr.Error(), "cfo send") {
		t.Errorf("an answer to a question with no choices and no page = %v, want it sent with cfo send", plainErr)
	}
	if typed := goblin.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Ship the three mockups as shown") {
		t.Errorf("the goblin received %q, want the CFO's words once", typed)
	}
	if offErr != nil {
		t.Fatal(offErr)
	}
	questions := s.Store.Snapshot().Questions
	if len(questions) != 1 {
		t.Fatalf("questions = %+v, want only the page's, asked nothing more", questions)
	}
	if q := questions[0]; q.Status != "succeeded" || q.AnswerKind != "other" || q.AnsweredOption != "" || q.Answer != "Ship the three mockups as shown" || !q.AnsweredAway {
		t.Errorf("the page's question = %+v, want the CFO's written answer, given while he was away", q)
	}
}
