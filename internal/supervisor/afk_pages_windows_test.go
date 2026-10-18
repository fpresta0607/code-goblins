package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
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

// awayPage turns AFK mode on at his ask and has task-1 put page, at link, in
// a review item, which AFK mode makes the CFO's decision, and returns its
// question as the board holds it and the notify that woke the CFO.
func awayPage(t *testing.T, s *Service, h home.Home, link, page string) (Question, wake.Record) {
	t.Helper()
	if err := SwitchAFKAtHisAsk(h, true, hisAskOn); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(h, "task-1", "task-1-mockups", "Approve the three mockups", link, page, nil); err != nil {
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
// was away, the goblin's items still open on that same page close with it,
// its open item on another page stays open, and the AFK log keeps the answer
// with the page, the question and the CFO's choice.
func TestTheCFOsAnswerToAGoblinsPageReachesItOnceAndClosesItsItems(t *testing.T) {
	// Arrange
	s, h, meta, goblin, connection := pageFixture(t)
	page := pageFile(t, mockupsChoices)
	before := time.Now().UTC()
	earlier := Review{ID: "waiting-task-1-1", Identity: goblinIdentity(meta), Task: meta.ID, Title: "Waiting on you: the first mockups (page " + mockupsPage + ")", Lavish: mockupsPage, LavishPage: page, State: "open", CreatedAt: before, UpdatedAt: before}
	other := earlier
	other.ID, other.Title, other.Lavish = "waiting-task-1-2", "Waiting on you: the harness cards (page http://127.0.0.1:4387/session/c4d5)", "http://127.0.0.1:4387/session/c4d5"
	for _, r := range []Review{earlier, other} {
		if err := s.Store.acceptReview(r); err != nil {
			t.Fatal(err)
		}
	}
	q, record := awayPage(t, s, h, mockupsPage, page)

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
	if len(snapshot.Reviews) != 2 || snapshot.Reviews[0].State == "open" || snapshot.Reviews[1].State != "open" {
		t.Errorf("reviews = %+v, want the goblin's earlier item on the same page closed and the one on another page open", snapshot.Reviews)
	}
	entries := afkEntries(t, h.State)
	logged := entries[len(entries)-1]
	if logged.Kind != afk.KindAnswer || logged.What != q.ID || logged.Task != meta.ID || logged.Link != mockupsPage || !strings.Contains(logged.Evidence, "asked: "+q.Text+" answered: Build it as shown. the grid reads") {
		t.Errorf("logged = %+v, want the answer with its page, its question and the CFO's choice", logged)
	}
}

// Only the page AFK mode asked as the question closes with the CFO's answer,
// and only the goblin's items on it published before the question, whether
// the CFO answered with cfo answer or answered another way and acked: its
// item on another page, an item of another generation of the goblin on the
// same page and a version of the page published after the question stay open.
func TestOnlyThePageAFKModeAskedClosesWithTheCFOsAnswer(t *testing.T) {
	for name, answer := range map[string]func(t *testing.T, s *Service, h home.Home, connection *CFOConnection, record wake.Record){
		"cfo answer": func(t *testing.T, s *Service, _ home.Home, connection *CFOConnection, record wake.Record) {
			if _, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Build it as shown", ""); err != nil {
				t.Fatal(err)
			}
			if err := s.Store.ingestAnswers(); err != nil {
				t.Fatal(err)
			}
		},
		"an answer the CFO acked": func(t *testing.T, s *Service, h home.Home, _ *CFOConnection, record wake.Record) {
			if err := wake.AckThrough(h.State, record.Seq); err != nil {
				t.Fatal(err)
			}
			if err := s.Store.supersedeQuestions(); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, meta, _, connection := pageFixture(t)
			page := pageFile(t, mockupsChoices)
			before := time.Now().UTC().Add(-time.Minute)
			item := func(id, link, identity string, at time.Time) Review {
				return Review{ID: id, Identity: identity, Task: meta.ID, Title: "Waiting on you: " + id + " (page " + link + ")", Lavish: link, LavishPage: page, State: "open", CreatedAt: at, UpdatedAt: at}
			}
			s.Store.mu.Lock()
			s.Store.db.Reviews = append(s.Store.db.Reviews,
				item("same-page", mockupsPage, goblinIdentity(meta), before),
				item("other-page", "http://127.0.0.1:4387/session/c4d5", goblinIdentity(meta), before),
				item("other-generation", mockupsPage, strings.Repeat("b", 64), before))
			err := s.Store.save()
			s.Store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			q, record := awayPage(t, s, h, mockupsPage, page)
			s.Store.mu.Lock()
			s.Store.db.Reviews = append(s.Store.db.Reviews, item("later-version", mockupsPage, goblinIdentity(meta), q.CreatedAt.Add(time.Second)))
			err = s.Store.save()
			s.Store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			// Act
			answer(t, s, h, connection, record)

			// Assert
			want := map[string]string{"same-page": "answered", "other-page": "open", "other-generation": "open", "later-version": "open"}
			for _, r := range s.Store.Snapshot().Reviews {
				if state, named := want[r.ID]; named && r.State != state {
					t.Errorf("item %s = %s (%q), want %s", r.ID, r.State, r.Reason, state)
				}
			}
		})
	}
}

// A goblin's ordinary question is never a page, even when its text ends
// naming one as AFK mode's do: the CFO's answer to it leaves the goblin's
// open item on that page open.
func TestAQuestionThatOnlyNamesAPageClosesNoPage(t *testing.T) {
	// Arrange
	s, h, meta, _, _ := pageFixture(t)
	if err := s.Store.acceptReview(Review{ID: "waiting-task-1-1", Identity: goblinIdentity(meta), Task: meta.ID, Title: "Waiting on you: the mockups (page " + mockupsPage + ")", Lavish: mockupsPage, LavishPage: pageFile(t, mockupsChoices), State: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	record, err := wake.Append(h.State, "notify", meta.ID, "blocked: Which layout? (page "+mockupsPage+") options: Grid | List")
	if err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, "", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	q := s.Store.Snapshot().Questions[0]

	// Act
	err = s.Store.recordCFOAnswer(cfoAnswer{QuestionID: q.ID, Option: "Grid", Answer: "Grid", At: time.Now().UTC()})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Store.Snapshot().Reviews[0]; r.State != "open" {
		t.Errorf("the goblin's page item = %s (%q), want it open", r.State, r.Reason)
	}
}

// A page the CFO answered in his place is settled once AFK mode turns off, as
// the Overlord asked on 2026-10-12: it stays in History as the goblin's
// question with the CFO's answer, given while he was away, where he can
// change it, and it is never asked of him again, nor listed under For you.
// The report recaps it under For the goblins.
func TestAPageTheCFOAnsweredIsSettledInHistoryAndNeverAsked(t *testing.T) {
	// Arrange
	s, h, _, _, connection := pageFixture(t)
	q, record := awayPage(t, s, h, mockupsPage, pageFile(t, mockupsChoices))
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
	i := slices.IndexFunc(report.Sections(), func(section afk.Section) bool { return section.Title == "For the goblins" })
	if i < 0 || len(report.Sections()[i].Entries) != 1 || report.Sections()[i].Entries[0].What != q.ID {
		t.Errorf("the report's sections = %+v, want the page's answer under For the goblins", report.Sections())
	}
}

// A page that declares no choices still reaches the CFO as a decision, which
// it answers in its own words, and History keeps that answer as the goblin's
// question's, written. A goblin's question without choices and without a
// page is answered with cfo send, as before.
func TestAPageThatDeclaresNoChoicesTakesTheCFOsOwnWords(t *testing.T) {
	// Arrange
	s, h, meta, goblin, connection := pageFixture(t)
	_, record := awayPage(t, s, h, mockupsPage, pageFile(t, ""))
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

// pickedAndEnded is what lavish-axi's poll prints of his pick of the option a
// page declared, sent with Send and end.
const pickedAndEnded = "session:\n" +
	"  status: feedback\n" +
	"  session_ended: true\n" +
	"  ended_by: user\n" +
	"prompts[1]{uid,prompt,selector,tag,text}:\n" +
	"  \"1\",Build it as shown,\"script[data-lavish-choices]\",choice,Build it as shown?\n" +
	"next_step: \"This was the last feedback before the user ended the session.\"\n"

// servedAt lists page in lavish-axi's state as session key, which lavish-axi
// serves on both of its addresses, local host and the tailnet.
func servedAt(t *testing.T, key, page string) {
	t.Helper()
	lavish := t.TempDir()
	t.Setenv("LAVISH_AXI_STATE_DIR", lavish)
	stored, err := json.Marshal(map[string]any{"sessions": map[string]any{key: map[string]string{"key": key, "file": page, "status": "opened"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lavish, "state.json"), stored, 0o600); err != nil {
		t.Fatal(err)
	}
}

// pageWatcher is a supervisor that watches the pages of s's board with poll
// standing in for lavish-axi's, and tells its own subscribers what changed.
func pageWatcher(s *Service, poll func(context.Context, string, string, time.Duration) (axi.PagePoll, error)) *Service {
	return &Service{Store: s.Store, Options: Options{CFO: s.Options.CFO, PollPage: poll}, subscribers: map[chan struct{}]struct{}{}}
}

// His Send and end on a goblin's page that AFK mode asked as its question,
// on local host or the tailnet, answers that question: the link names the
// page lavish-axi serves there, which is polled while the question waits, so
// his send is taken, and the page acknowledges it, at once; the question leaves Waiting on you within a second, its notify
// reads answered, the goblin gets his pick once, and the page, its review
// ended, is watched no more. On 2026-10-18 nothing polled Kenny's page at
// http://127.0.0.1:4387/session/030284fb3915be1f: his Send and end showed
// nothing, and the question still waited on the board.
func TestSendAndEndOnAQuestionsPageAnswersItWithinASecond(t *testing.T) {
	for name, link := range map[string]string{
		"local host": "http://127.0.0.1:4387/session/030284fb3915be1f",
		"tailnet":    "https://pc.example.ts.net:4388/session/030284fb3915be1f",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, _, goblin, _ := pageFixture(t)
			page := pageFile(t, mockupsChoices)
			servedAt(t, "030284fb3915be1f", page)
			q, record := awayPage(t, s, h, link, "")
			if q.AsksPageFile != page {
				t.Fatalf("the question = %+v, want it to ask the page lavish-axi serves at %s, %s", q, link, page)
			}
			scrawl := newScrawlPage(0)
			watcher := pageWatcher(s, scrawl.poll)
			board, unsubscribe := watcher.subscribe()
			defer unsubscribe()
			ctx, cancel := context.WithCancel(context.Background())
			defer func() {
				cancel()
				watcher.pageWork.Wait()
			}()
			watcher.watchPages(ctx)
			select {
			case <-scrawl.listening:
			case <-time.After(30 * time.Second):
				t.Fatal("nothing polled the page while its question waited")
			}

			// Act
			sent := time.Now()
			scrawl.send(pickedAndEnded)
			var answered Question
			for expired := time.After(time.Minute); answered.Status != "succeeded"; {
				select {
				case <-board:
					items, _ := watcher.Items()
					if i := slices.IndexFunc(items.Questions, func(shown Question) bool { return shown.ID == q.ID }); i >= 0 {
						answered = items.Questions[i]
					}
				case <-expired:
					t.Fatalf("the boards never showed the question answered, last = %+v", answered)
				}
			}
			shown := time.Since(sent)
			told := goblin.waitForLines(t, 1)
			watcher.pageWork.Wait()

			// Assert
			// stall-accepted: it times the board against his send, as his own test of a page's item does, and nothing it waits on is longer than a poll's return.
			if shown > time.Second {
				t.Errorf("the boards showed the question answered %s after he sent it, want under a second", shown.Round(time.Millisecond))
			}
			if answered.AnsweredBy != "overlord" || answered.AnsweredIn != "page" || answered.AnsweredOption != "Build it as shown" {
				t.Errorf("the question = %+v, want his pick, answered on its page", answered)
			}
			pending, err := wake.Pending(h.State)
			if err != nil || !slices.ContainsFunc(pending, func(r wake.Record) bool {
				return r.Seq == record.Seq && r.Answered == "Build it as shown" && r.AnsweredBy == wake.AnsweredByOverlord
			}) {
				t.Errorf("wakes = %+v (%v), want notify %d read as answered by him", pending, err, record.Seq)
			}
			if len(told) != 1 || !strings.Contains(told[0], "Build it as shown") {
				t.Errorf("the goblin got %q, want his pick once", told)
			}
			if got := s.Store.Snapshot().Questions[0]; got.PageSettled == nil || len(s.Store.watchedPages()) != 0 {
				t.Errorf("the question = %+v, watched pages = %+v, want its page settled and watched no more", got, s.Store.watchedPages())
			}
		})
	}
}

// His pick of the option a question's page declared answers the question
// though he does not end the review, and the page stays watched until he
// ends it: the page says his pick was received, and his end adds nothing.
func TestAPickOnAQuestionsPageAnswersItAndThePageStaysWatched(t *testing.T) {
	// Arrange
	s, h, _, goblin, _ := pageFixture(t)
	_, record := awayPage(t, s, h, mockupsPage, pageFile(t, mockupsChoices))
	scrawl := newScrawlPage(0)
	scrawl.send(pickedOnThePage)
	watcher := pageWatcher(s, scrawl.poll)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	watcher.watchPages(ctx)
	told := goblin.waitForLines(t, 1)
	for deadline := time.Now().Add(time.Minute); !slices.Contains(scrawl.pageReplies(), "Received."); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the page told him %q, want his pick received", scrawl.pageReplies())
		}
	}
	answered := s.Store.Snapshot().Questions[0]
	scrawl.send(scrawlEnded)
	watcher.pageWork.Wait()

	// Assert
	if answered.Status != "succeeded" || answered.AnsweredIn != "page" || answered.AnsweredOption != "Build it as shown" {
		t.Errorf("the question after his pick = %+v, want his pick, answered on its page", answered)
	}
	if pending, err := wake.Pending(h.State); err != nil || !slices.ContainsFunc(pending, func(r wake.Record) bool { return r.Seq == record.Seq && r.Answered == "Build it as shown" }) {
		t.Errorf("wakes = %+v (%v), want notify %d read as answered", pending, err, record.Seq)
	}
	if len(told) != 1 || !strings.Contains(told[0], "Build it as shown") {
		t.Errorf("the goblin got %q, want his pick", told)
	}
	if typed := goblin.lines(t); len(typed) != 1 {
		t.Errorf("the goblin got %q, want his pick once and nothing for his end", typed)
	}
	if got := s.Store.Snapshot().Questions[0]; got.PageSettled == nil || got.AnsweredOption != "Build it as shown" {
		t.Errorf("the question after he ended the review = %+v, want his pick, its page settled", got)
	}
}

// Once the CFO has answered a goblin's page, what he then sends on the page
// is a note the goblin gets, and the CFO's answer stands, also when the CFO
// answered while a poll waited on the page.
func TestWhatHeSendsOnAPageTheCFOAnsweredReachesTheGoblinAsANote(t *testing.T) {
	for name, isDuringThePoll := range map[string]bool{"before the page is polled": false, "while a poll waits on the page": true} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, _, goblin, connection := pageFixture(t)
			_, record := awayPage(t, s, h, mockupsPage, pageFile(t, mockupsChoices))
			answerIt := func() error {
				if _, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "Change it", ""); err != nil {
					return err
				}
				return s.Store.ingestAnswers()
			}
			scrawl := newScrawlPage(0)
			var answered sync.Once
			var answerErr error
			poll := func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
				if isDuringThePoll {
					answered.Do(func() {
						answerErr = answerIt()
						scrawl.send(pickedAndEnded)
					})
				}
				// stall-accepted: timeout is the supervisor's own bound on a poll, which the stand-in waits out as lavish-axi's poll would; the test times nothing with it.
				return scrawl.poll(ctx, file, reply, timeout)
			}
			if !isDuringThePoll {
				if err := answerIt(); err != nil {
					t.Fatal(err)
				}
				scrawl.send(pickedAndEnded)
			}
			watcher := pageWatcher(s, poll)

			// Act
			watcher.watchPages(context.Background())
			watcher.pageWork.Wait()
			told := goblin.waitForLines(t, 2)

			// Assert
			if answerErr != nil {
				t.Fatal(answerErr)
			}
			if q := s.Store.Snapshot().Questions[0]; q.AnsweredBy != "cfo" || q.AnsweredOption != "Change it" || q.PageSettled == nil {
				t.Errorf("the question = %+v, want the CFO's answer standing and its page settled", q)
			}
			if len(told) != 2 || !strings.Contains(told[0], "Change it") || !strings.Contains(told[1], "wrote on your review page") || !strings.Contains(told[1], "Build it as shown") {
				t.Errorf("the goblin got %q, want the CFO's answer, then his words as a note", told)
			}
			if pending, err := wake.Pending(h.State); err != nil || !slices.ContainsFunc(pending, func(r wake.Record) bool {
				return r.Seq == record.Seq && r.Answered == "Change it" && r.AnsweredBy == wake.AnsweredByCFO
			}) {
				t.Errorf("wakes = %+v (%v), want notify %d read as the CFO's answer", pending, err, record.Seq)
			}
		})
	}
}

// A page another reporter's open item already shows stays that reporter's:
// the question AFK mode made of a goblin's same page does not take it, as a
// second item for it would be refused.
func TestAPageAnotherReportersItemShowsStaysThatReporters(t *testing.T) {
	// Arrange
	s, h, _, _, _ := pageFixture(t)
	page := pageFile(t, mockupsChoices)
	at := time.Now().UTC().Add(-time.Minute)
	held := Review{ID: "cfo-mockups", Identity: strings.Repeat("c", 64), Title: "The CFO's look at the mockups", Lavish: mockupsPage, LavishPage: page, State: "open", CreatedAt: at, UpdatedAt: at}
	if err := s.Store.acceptReview(held); err != nil {
		t.Fatal(err)
	}
	q, _ := awayPage(t, s, h, mockupsPage, page)

	// Act
	watched := s.Store.watchedPages()

	// Assert
	if q.AsksPageFile != page {
		t.Fatalf("the question = %+v, want it to ask %s", q, page)
	}
	if got := watched[pageKey(page)]; got.ID != held.ID {
		t.Errorf("the page is watched for %+v, want the CFO's item that showed it first", got)
	}
}

// A question's page file is the absolute HTML file of the page it asks, and
// only a question that asks a page has one.
func TestAQuestionsPageFileIsTheHTMLFileOfThePageItAsks(t *testing.T) {
	page := filepath.Join(t.TempDir(), "mockups.html")
	asked := Question{ID: "notify-task-1-1", Identity: strings.Repeat("g", 64), Text: "Approve the three mockups (page " + mockupsPage + ")", CreatedAt: time.Now().UTC(), Task: "task-1", Generation: "g1", Seq: 1, AsksPage: mockupsPage, AsksPageFile: page}
	if err := validQuestion(asked); err != nil {
		t.Fatal(err)
	}
	separator := string(filepath.Separator)
	for name, change := range map[string]func(q *Question){
		"a relative file":        func(q *Question) { q.AsksPageFile = "mockups.html" },
		"a file that is no page": func(q *Question) { q.AsksPageFile = filepath.Join(filepath.Dir(page), "notes.txt") },
		"an unclean path":        func(q *Question) { q.AsksPageFile = page + separator + ".." + separator + "mockups.html" },
		"no page it asks":        func(q *Question) { q.AsksPage = "" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			q := asked
			change(&q)

			// Act
			err := validQuestion(q)

			// Assert
			if err == nil {
				t.Errorf("a question with %s was accepted", name)
			}
		})
	}
}
