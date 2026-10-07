package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const pageLink = "http://127.0.0.1:4387/session/f26e"

// waitOnAPage makes a goblin wait on the Overlord with a Lavish page, and
// returns the task ID and the page.
func waitOnAPage(t *testing.T, store *Store) (string, string) {
	t.Helper()
	meta, _, _, _ := goblinFixture(t, store)
	page := filepath.Join(meta.Worktree, ".lavish", "plan.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: pick a plan"); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(store.Home, meta.ID, 7, "pick a plan", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	return meta.ID, page
}

// askOnAPage makes a goblin ask a question on the board and wait on the
// Overlord with a Lavish page about it, as cg-board-polish did on
// 2026-09-30, and returns the goblin, its question's notify and the page.
func askOnAPage(t *testing.T, store *Store) (state.TaskMeta, wake.Record, string) {
	t.Helper()
	meta, record, _, connection := goblinFixture(t, store)
	surfaced(t, store, meta, record, connection)
	page := filepath.Join(meta.Worktree, ".lavish", "plan.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: pick a store on the page"); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(store.Home, meta.ID, record.Seq+1, "pick a store on the page", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	return meta, record, page
}

// A goblin's question asked while its review page is open is that page's
// item, never a second question: the snapshot ties each to the other. A
// question from a goblin generation that has no page open stays a question
// of its own.
func TestAQuestionWithAPageIsShownAsThatPagesItem(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	askOnAPage(t, store)
	store.mu.Lock()
	other := store.db.Questions[0]
	other.ID, other.Identity, other.Seq = "notify-task-1-99", strings.Repeat("b", 64), 99
	store.db.Questions = append(store.db.Questions, other)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()

	// Act
	snapshot, err := (&Service{Store: store}).Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Questions) != 2 || len(snapshot.Reviews) != 1 {
		t.Fatalf("snapshot = %d questions and %d reviews, want 2 and 1", len(snapshot.Questions), len(snapshot.Reviews))
	}
	asked, unrelated, page := snapshot.Questions[0], snapshot.Questions[1], snapshot.Reviews[0]
	if asked.Page != page.ID || page.Question != asked.ID {
		t.Errorf("question %s names page %q and page %s names question %q, want each the other", asked.ID, asked.Page, page.ID, page.Question)
	}
	if unrelated.Page != "" {
		t.Errorf("a question from another generation of the goblin names page %q, want none", unrelated.Page)
	}
}

// A goblin that already waits on the Overlord with a page and then asks a
// question keeps its page: the question is a report newer than the wait,
// which once withdrew it, so the two never showed as one item (found by
// the live proof, 2026-10-01). The wait stands beside the question, and
// the question is the page's item.
func TestAQuestionAskedAfterItsPageWaitIsStillThatPagesItem(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, record, _, connection := goblinFixture(t, store)
	page := filepath.Join(meta.Worktree, ".lavish", "plan.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: pick a store on the page"); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(store.Home, meta.ID, record.Seq+1, "pick a store on the page", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	surfaced(t, store, meta, record, connection)
	if err := state.AppendStatus(store.Home.State, meta.ID, record.Detail); err != nil {
		t.Fatal(err)
	}

	// Act
	retired := store.retireItems()
	snapshot, err := (&Service{Store: store}).Snapshot()

	// Assert
	if retired != nil || err != nil {
		t.Fatal(retired, err)
	}
	if len(snapshot.Questions) != 1 || len(snapshot.Reviews) != 1 {
		t.Fatalf("snapshot = %d questions and %d reviews, want 1 and 1", len(snapshot.Questions), len(snapshot.Reviews))
	}
	asked, item := snapshot.Questions[0], snapshot.Reviews[0]
	if item.State != "open" {
		t.Fatalf("the page's wait after its goblin asked = %s (%s), want it still open", item.State, item.Reason)
	}
	if asked.Page != item.ID || item.Question != asked.ID {
		t.Errorf("question %s names page %q and page %s names question %q, want each the other", asked.ID, asked.Page, item.ID, item.Question)
	}
}

// An answer on the page is the answer to the question it carries: the
// question closes as answered by the Overlord on the page, and the goblin's
// notify reads answered, so the CFO's drain retires it.
func TestAnAnswerOnThePageClosesTheQuestionItCarries(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, record, page := askOnAPage(t, store)
	answer := "session:\n  status: feedback\nprompts[1]{uid,prompt,selector,tag,text}:\n  \"\",Go with SQLite,\"\",message,Freeform message\n"
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file, _ string, _ time.Duration) (axi.PagePoll, error) {
		if file != page {
			t.Errorf("polled %s, want %s", file, page)
		}
		return axi.PagePoll{Status: "feedback", Output: answer}, nil
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	q := store.Snapshot().Questions[0]
	if q.Status != "succeeded" || q.AnsweredBy != "overlord" || q.AnsweredIn != "page" || q.AnsweredAt == nil {
		t.Errorf("the question = %+v, want it answered by the Overlord on the page", q)
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(pending, func(r wake.Record) bool { return r.Seq == record.Seq }); i < 0 || pending[i].Answered == "" || pending[i].AnsweredBy != wake.AnsweredByOverlord {
		t.Errorf("the question's notify = %+v, want it read answered by the Overlord", pending)
	}
}

// The CFO's answer to a question closes the page that carries it, so the
// Command Center never keeps a page whose question is settled.
func TestTheCFOsAnswerToAQuestionClosesItsPage(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	askOnAPage(t, store)
	q := store.Snapshot().Questions[0]

	// Act
	err := store.recordCFOAnswer(cfoAnswer{QuestionID: q.ID, Option: "SQLite", Answer: "SQLite", At: time.Now().UTC()})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if r := store.Snapshot().Reviews[0]; r.State != "answered" || r.AnsweredBy != "cfo" || r.AnsweredIn != "question" {
		t.Errorf("the page's item = %+v, want it answered by the CFO through its question", r)
	}
}

// reviewWakes returns the review records the CFO has for task.
func reviewWakes(t *testing.T, stateDir, task string) []wake.Record {
	t.Helper()
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	var found []wake.Record
	for _, record := range records {
		if record.Kind == "review" && record.Key == task {
			found = append(found, record)
		}
	}
	return found
}

// Whatever becomes of a waited-on page reaches the CFO as one review wake,
// and the wait closes: an answer is kept whole for the CFO to relay, and the
// item says the Overlord answered on its page, as an answer on the board
// does. Only an item closed without his word is withdrawn.
func TestAPageWaitHandsWhatBecameOfThePageToTheCFO(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	answer := "session:\n  status: feedback\nprompts[1]{id,text}:\n  p1,Ship option B\n"
	answered := Review{State: "answered", AnsweredBy: "overlord", AnsweredIn: "page", Reason: "You answered on its page; the CFO relays it to the goblin."}
	for name, test := range map[string]struct {
		polls  []axi.PagePoll
		err    error
		want   string
		closed Review
	}{
		"an answer after a quiet poll":   {polls: []axi.PagePoll{{Status: "waiting"}, {Status: "feedback", Ended: true, EndedBy: "user", Output: answer}}, want: "the Overlord answered on the page", closed: answered},
		"an answer that ends the review": {polls: []axi.PagePoll{{Status: "feedback", Ended: true, EndedBy: "user", Output: answer}}, want: "and ended the review", closed: answered},
		"the Overlord ended the review":  {polls: []axi.PagePoll{{Status: "ended", EndedBy: "user"}}, want: "the Overlord ended the review of", closed: Review{State: "cleared", Reason: "You ended the review on its page."}},
		"an agent ended the review":      {polls: []axi.PagePoll{{Status: "ended", EndedBy: "agent"}}, want: "an agent, not the Overlord, ended the review of", closed: Review{State: "withdrawn", Reason: "An agent ended the review on its page; the CFO was told."}},
		// A closed review window is not the end of the review: his answers
		// queue on the page, and reopening it resumes the same review.
		"an answer after the window disconnected": {polls: []axi.PagePoll{{Status: "browser_disconnected"}, {Status: "waiting"}, {Status: "feedback", Ended: true, EndedBy: "user", Output: answer}}, want: "the Overlord answered on the page", closed: answered},
		"a page that cannot be polled":            {err: errors.New("No active Lavish Editor session for this file"), want: "cannot poll the page", closed: Review{State: "withdrawn", Reason: "The page could not be polled; the CFO was told."}},
	} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			task, page := waitOnAPage(t, store)
			polls := 0
			s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file, _ string, timeout time.Duration) (axi.PagePoll, error) {
				polls++
				if file != page || timeout != pagePollTimeout {
					t.Errorf("polled %s for %s, want %s for %s", file, timeout, page, pagePollTimeout)
				}
				if test.err != nil {
					return axi.PagePoll{}, test.err
				}
				return test.polls[polls-1], nil
			}}}

			s.watchPages(context.Background())
			s.pageWork.Wait()

			wakes := reviewWakes(t, h.State, task)
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, test.want) || !strings.Contains(wakes[0].Detail, page) {
				t.Fatalf("review wakes = %+v, want one naming the page and saying %q", wakes, test.want)
			}
			if test.err != nil && polls != pagePollAttempts {
				t.Errorf("polled %d times before giving up, want %d", polls, pagePollAttempts)
			}
			if strings.Contains(test.want, "answered") {
				_, rest, _ := strings.Cut(wakes[0].Detail, "his feedback is in ")
				saved, _, _ := strings.Cut(rest, ",")
				if data, err := os.ReadFile(saved); err != nil || string(data) != answer {
					t.Errorf("saved feedback %s = %q, %v; want the poll's whole output", saved, data, err)
				}
			}
			got := store.Snapshot().Reviews[0]
			if got.State != test.closed.State || got.AnsweredBy != test.closed.AnsweredBy || got.AnsweredIn != test.closed.AnsweredIn || got.Reason != test.closed.Reason {
				t.Errorf("the wait = %+v, want it closed once the CFO has it as %+v", got, test.closed)
			}
		})
	}
}

// The CFO's own page is polled too, so the CFO never holds its turn on a
// poll: the Overlord's feedback comes back as a wake keyed by the item, since
// there is no goblin, telling the CFO to act on it rather than relay it. A
// closed review window ends its review no more than a goblin's does.
func TestTheCFOsOwnPageReachesItAsAWakeKeyedByTheItem(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	servePipe(t, store, cfo)
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	page := filepath.Join(h.Root, ".lavish", "dispatch-options.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(h, "", "dispatch-review-1", "Pick the dispatch order", pageLink, page, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	answer := "session:\n  status: feedback\nprompts[1]{id,text}:\n  p1,Payments first\n"
	polls := []axi.PagePoll{{Status: "browser_disconnected"}, {Status: "feedback", Ended: true, EndedBy: "user", Output: answer}}
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file, _ string, _ time.Duration) (axi.PagePoll, error) {
		if file != page {
			t.Errorf("polled %s, want the CFO's page %s", file, page)
		}
		poll := polls[0]
		polls = polls[1:]
		return poll, nil
	}}}

	s.watchPages(context.Background())
	s.pageWork.Wait()

	wakes := reviewWakes(t, h.State, "dispatch-review-1")
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "the Overlord answered on the page "+page) || !strings.HasSuffix(wakes[0].Detail, ", act on it") {
		t.Fatalf("review wakes = %+v, want one keyed by the item telling the CFO to act on its page's feedback", wakes)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredBy != "overlord" || got.AnsweredIn != "page" || got.Reason != "You answered on its page; the CFO has it." {
		t.Errorf("the CFO's item = %+v, want it answered by the Overlord on its page, saying the CFO has the feedback, with nobody to relay it to", got)
	}
}

// A disconnected review window leaves the item open and polled, and the item
// says when the window closed, so the board can tell the Overlord to reopen
// it.
func TestADisconnectedWindowLeavesItsItemOpenSayingWhenItClosed(t *testing.T) {
	// Arrange
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	task, _ := waitOnAPage(t, store)
	polls := make(chan struct{}, 4)
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _, _ string, _ time.Duration) (axi.PagePoll, error) {
		polls <- struct{}{}
		if len(polls) == 1 {
			return axi.PagePoll{Status: "browser_disconnected"}, nil
		}
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := time.Now().UTC()

	// Act
	s.watchPages(ctx)
	for len(polls) < 2 {
		time.Sleep(time.Millisecond)
	}

	// Assert
	got := store.Snapshot().Reviews[0]
	if got.State != "open" || got.WindowClosedAt == nil || got.WindowClosedAt.Before(before) {
		t.Errorf("the wait = %+v, want it open, saying when its window closed", got)
	}
	if wakes := reviewWakes(t, h.State, task); len(wakes) != 0 {
		t.Errorf("review wakes = %+v, want none for a window that closed", wakes)
	}
	cancel()
	s.pageWork.Wait()
}

// The Overlord's answer on a goblin's page reaches the goblin whatever the
// goblin did after it presented the page. On 2026-10-06 cg-fleet-tree waited
// on him with its page and then asked the CFO a question the CFO answered,
// and his notes and Send & End on the page at 21:57Z reached nobody: the
// page read "Your agent is not listening". cg-afk-mode's later notifies had
// withdrawn its page's watch the same way.
func TestAnAnswerOnAPageReachesTheGoblinWhateverItReportedSince(t *testing.T) {
	for name, since := range map[string]func(t *testing.T, store *Store, meta state.TaskMeta, asked wake.Record, connection *CFOConnection){
		"a later status": func(t *testing.T, store *Store, meta state.TaskMeta, _ wake.Record, _ *CFOConnection) {
			if err := state.AppendStatus(store.Home.State, meta.ID, "working: the backend work continues meanwhile"); err != nil {
				t.Fatal(err)
			}
		},
		"a question the CFO answered": func(t *testing.T, store *Store, meta state.TaskMeta, asked wake.Record, connection *CFOConnection) {
			if err := state.AppendStatus(store.Home.State, meta.ID, asked.Detail); err != nil {
				t.Fatal(err)
			}
			q := surfaced(t, store, meta, asked, connection)
			if err := store.recordCFOAnswer(cfoAnswer{QuestionID: q.ID, Option: "SQLite", Answer: "SQLite", At: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
		},
		"a pull request it finished": func(t *testing.T, store *Store, meta state.TaskMeta, _ wake.Record, _ *CFOConnection) {
			if err := state.AppendStatus(store.Home.State, meta.ID, "done: PR https://github.com/example/repo/pull/1"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, asked, goblin, connection := goblinFixture(t, store)
			page := presentAPage(t, store, meta, 7, "plan.html")
			nextSecond()
			since(t, store, meta, asked, connection)
			if err := store.retireItems(); err != nil {
				t.Fatal(err)
			}
			if r := store.Snapshot().Reviews[0]; r.State == "open" {
				t.Fatalf("premise: the page's item after %s = %+v, want it closed", name, r)
			}
			var replies []string
			s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(_ context.Context, file, reply string, _ time.Duration) (axi.PagePoll, error) {
				if file != page {
					t.Errorf("polled %s, want %s", file, page)
				}
				if replies = append(replies, reply); len(replies) > 1 {
					return axi.PagePoll{Status: "ended", EndedBy: "user"}, nil
				}
				return axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Prompts: []string{"I prefer goblin heads for each type, not icons"}, Output: "session:\n  status: feedback\n"}, nil
			}}}

			// Act
			s.watchPages(context.Background())
			s.pageWork.Wait()

			// Assert
			if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "on your review page "+page+": I prefer goblin heads for each type, not icons") {
				t.Fatalf("the goblin got %q, want his answer on its page", told)
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, page) || !strings.Contains(wakes[0].Detail, meta.ID+" has it: I prefer goblin heads") {
				t.Fatalf("review wakes = %+v, want the CFO told once that the goblin has his answer", wakes)
			}
			if len(replies) != 2 || replies[1] != "Received. "+meta.ID+" has it." {
				t.Errorf("replies on the page = %q, want it told the goblin has his answer", replies)
			}
			if r := store.Snapshot().Reviews[0]; r.PageSettled == nil {
				t.Errorf("the page's item = %+v, want its page settled once he ended the review", r)
			}
		})
	}
}

// presentAPage makes goblin meta wait on the Overlord with a new Lavish page
// named name under its worktree, as notify --waiting-on overlord --lavish
// does with wake sequence seq, and returns the page.
func presentAPage(t *testing.T, store *Store, meta state.TaskMeta, seq int, name string) string {
	t.Helper()
	page := filepath.Join(meta.Worktree, ".lavish", name)
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: look at "+name); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(store.Home, meta.ID, seq, "look at "+name, pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	return page
}

// nextSecond waits for the next whole second, so a report made after it is
// later than an item made before it: a status line keeps whole seconds.
func nextSecond() {
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))
}

// A goblin that presents a second page while its first is unanswered has
// both watched: the later wait withdraws the first page's card, never its
// watch, and each page has one poller.
func TestALaterPageIsWatchedBesideTheFirst(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, _, _, _ := goblinFixture(t, store)
	first := presentAPage(t, store, meta, 7, "plan.html")
	nextSecond()
	second := presentAPage(t, store, meta, 9, "next.html")
	if err := store.retireItems(); err != nil {
		t.Fatal(err)
	}
	polled := make(chan string, 4)
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, file, _ string, _ time.Duration) (axi.PagePoll, error) {
		polled <- file
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	ctx, cancel := context.WithCancel(context.Background())

	// Act
	for range 3 {
		s.watchPages(ctx)
	}
	var got []string
	for deadline := time.After(5 * time.Second); len(got) < 2; {
		select {
		case file := <-polled:
			got = append(got, file)
		case <-deadline:
			cancel()
			s.pageWork.Wait()
			t.Fatalf("polled only %q, want both pages", got)
		}
	}
	cancel()
	s.pageWork.Wait()

	// Assert
	slices.Sort(got)
	want := []string{second, first}
	slices.Sort(want)
	if !slices.Equal(got, want) || len(polled) != 0 {
		t.Fatalf("polled %q and %d more, want each page once: %q", got, len(polled), want)
	}
	if reviews := store.Snapshot().Reviews; reviews[0].State != "withdrawn" || reviews[1].State != "open" {
		t.Errorf("reviews = %+v, want the first page's card withdrawn by the later wait and the second open", reviews)
	}
}

// A goblin retired while its page is watched stops its poller once the poll
// under way ends, never in the middle of one, which could take his feedback
// and drop it, and nothing reaches the CFO for a page he did not write on: the
// retired goblin's page is the sweep's to end.
func TestARetiredGoblinsPageStopsItsPollerAfterThePollUnderWay(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	task, _ := waitOnAPage(t, store)
	polled, release := make(chan struct{}, 4), make(chan struct{})
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		polled <- struct{}{}
		<-release
		return axi.PagePoll{Status: "waiting"}, nil
	}}}
	s.watchPages(context.Background())
	<-polled

	// Act
	if err := state.RemoveTaskMeta(store.Home.State, task); err != nil {
		t.Fatal(err)
	}
	close(release)
	stopped := make(chan struct{})
	go func() {
		s.pageWork.Wait()
		close(stopped)
	}()

	// Assert
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the poller of a retired goblin's page is still running")
	}
	if len(polled) != 0 {
		t.Errorf("polled %d more times after the goblin was retired, want none", len(polled))
	}
	if wakes := reviewWakes(t, h.State, task); len(wakes) != 0 {
		t.Fatalf("review wakes = %+v, want none for a page nobody wrote on", wakes)
	}
}

// Only an open item with a page is polled, and each has one poller.
func TestPagesArePolledOncePerOpenWait(t *testing.T) {
	store, _ := testStore(t)
	waitOnAPage(t, store)
	polls := make(chan struct{}, 4)
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _, _ string, _ time.Duration) (axi.PagePoll, error) {
		polls <- struct{}{}
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	for range 3 {
		s.watchPages(ctx)
	}
	<-polls
	cancel()
	s.pageWork.Wait()
	if extra := len(polls); extra != 0 {
		t.Fatalf("%d more polls started for one wait, want one poller", extra)
	}
}

// published counts the errors the service has reported.
func published(s *Service) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// The poll consumed the Overlord's answer, so a wake queue that refuses the
// CFO's wake for a while only delays it, and the wait stays open until then.
func TestAPageAnswerReachesTheCFOOnceTheWakeQueueTakesIt(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	task, _ := waitOnAPage(t, store)
	ack := filepath.Join(h.State, ".wake-ack")
	if err := os.Mkdir(ack, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		return axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "session:\n  status: feedback\n"}, nil
	}}}

	s.watchPages(context.Background())
	for deadline := time.Now().Add(10 * time.Second); published(s) < 3; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the wake queue was never asked again after refusing the CFO's wake")
		}
	}
	if got := store.Snapshot().Reviews[0]; got.State != "open" {
		t.Fatalf("the wait = %+v while the CFO lacks the answer, want it open", got)
	}
	// The poller keeps reading the refused queue, and Windows refuses to
	// remove a directory another handle has open, so the removal is retried.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		err := os.Remove(ack)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	s.pageWork.Wait()

	if wakes := reviewWakes(t, h.State, task); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "his feedback is in ") {
		t.Fatalf("review wakes = %+v, want the answer once the queue takes it", wakes)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredIn != "page" {
		t.Errorf("the wait = %+v, want it answered on its page once the CFO has it", got)
	}
}

// An answer that cannot be saved still reaches the CFO, carried in the wake
// itself: its end is kept, and the cut is said.
func TestAPageAnswerThatCannotBeSavedTravelsInTheWake(t *testing.T) {
	store, h := testStore(t)
	task, _ := waitOnAPage(t, store)
	if err := os.MkdirAll(filepath.Join(h.State, "reviews"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "reviews", "feedback"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	answer := "session:\n  status: feedback\nprompts[1]{id,text}:\n  p1,\"" + strings.Repeat("é", pageFeedbackInline) + " Ship option B\"\n"
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		return axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: answer}, nil
	}}}

	s.watchPages(context.Background())
	s.pageWork.Wait()

	wakes := reviewWakes(t, h.State, task)
	if len(wakes) != 1 {
		t.Fatalf("review wakes = %+v, want the answer", wakes)
	}
	// The bound is on the answer carried inline; the page's path and the
	// save error before it are as long as this machine's paths.
	detail := wakes[0].Detail
	_, inline, isCut := strings.Cut(detail, "(cut to its end) ...")
	if !strings.Contains(detail, "could not be saved") || !isCut || !strings.HasSuffix(inline, " Ship option B\"\n") || len(inline) > pageFeedbackInline || !utf8.ValidString(detail) {
		t.Fatalf("wake detail = %q (%d bytes, %d inline), want the answer's end inline, bounded, and the cut said", detail, len(detail), len(inline))
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredIn != "page" {
		t.Errorf("the wait = %+v, want it answered on its page once the CFO has it", got)
	}
}

// A native goblin's board answer typed during its turn closes the page
// that carries its question as soon as it is sent, so what he writes on that
// page afterwards is never taken as a second answer to the question: the page
// stays watched, and his words there reach the goblin as a note on its page,
// relayed by the CFO here, where the goblin cannot be reached. Neither the
// warning nor the late take that follows closes a page the goblin opened
// after it.
func TestABoardAnswerBehindAGoblinsTurnClosesItsPageOnceWhenSent(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, _ := askOnAPage(t, store)
	q := store.Snapshot().Questions[0]
	since := time.Now().UTC().Add(-time.Hour)
	if _, err := store.Queue(Action{ID: "board-answer", Kind: "goblin_answer", Generation: q.Identity, QuestionID: q.ID, Text: "SQLite"}); err != nil {
		t.Fatal(err)
	}
	deliver := func(context.Context, Action) (Evaluation, error) {
		return Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: meta.ID, Generation: meta.SpawnGen, Since: since}}, nil
	}
	polled := make(chan string, 4)
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file, _ string, _ time.Duration) (axi.PagePoll, error) {
		polled <- file
		if len(polled) == 1 {
			return axi.PagePoll{Status: "feedback", Prompts: []string{"Go with Postgres"}, Output: "Go with Postgres"}, nil
		}
		return axi.PagePoll{Status: "ended", EndedBy: "user"}, nil
	}}}

	// Act
	if err := store.ProcessOne(context.Background(), deliver); err != nil {
		t.Fatal(err)
	}
	sent := store.Snapshot()
	s.watchPages(context.Background())
	s.pageWork.Wait()
	store.mu.Lock()
	later := store.db.Reviews[0]
	later.ID, later.State, later.AnsweredBy, later.AnsweredIn, later.Reason = "plan-later", "open", "", "", ""
	store.db.Reviews = append(store.db.Reviews, later)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()
	warned := store.settleDeliveries(since.Add(deliveryQuiet+time.Second), idle)
	delivered := store.settleDeliveries(since.Add(deliveryQuiet+2*time.Minute), took)
	settled := store.Snapshot()

	// Assert
	if warned != nil || delivered != nil {
		t.Fatal(warned, delivered)
	}
	if a := sent.Actions[slices.IndexFunc(sent.Actions, func(a Action) bool { return a.ID == "board-answer" })]; a.Status != "running" || a.Awaiting == nil {
		t.Fatalf("the board answer = %s, want it sent and awaiting the goblin's record", a.Status)
	}
	if r := sent.Reviews[0]; r.State != "answered" || r.AnsweredBy != "overlord" || r.AnsweredIn != "question" {
		t.Errorf("the page's item once the answer was sent = %+v, want it answered by the Overlord through its question", r)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "the Overlord wrote on the page") || !strings.Contains(wakes[0].Detail, "relay it to "+meta.ID) {
		t.Errorf("review wakes = %+v, want his later words on the page passed on once as a note", wakes)
	}
	if r := settled.Reviews[0]; r.AnsweredIn != "question" || r.PageSettled == nil {
		t.Errorf("the page's item after he ended its review = %+v, want it still answered through its question, its page settled", r)
	}
	if q := settled.Questions[0]; q.AnsweredIn == "page" {
		t.Errorf("the question = %+v, want it answered once, on the board, never again by the page", q)
	}
	if a := settled.Actions[slices.IndexFunc(settled.Actions, func(a Action) bool { return a.ID == "board-answer" })]; a.Status != "succeeded" {
		t.Errorf("the board answer after the late take = %s, want it delivered", a.Status)
	}
	if i := slices.IndexFunc(settled.Reviews, func(r Review) bool { return r.ID == "plan-later" }); i < 0 || settled.Reviews[i].State != "open" {
		t.Errorf("a page the goblin opened after the answer was sent = %+v, want it still open", settled.Reviews)
	}
}

// pageOfAGoblin makes the fixture's goblin wait on the Overlord with a Lavish
// page, as waitOnAPage does, and returns the goblin, its terminal and
// connection, and the page.
func pageOfAGoblin(t *testing.T, store *Store) (state.TaskMeta, hostedTerminal, *CFOConnection, string) {
	t.Helper()
	meta, _, goblin, connection := goblinFixture(t, store)
	page := filepath.Join(meta.Worktree, ".lavish", "plan.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(store.Home.State, meta.ID, "waiting on overlord: pick a plan"); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(store.Home, meta.ID, 7, "pick a plan", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	return meta, goblin, connection, page
}

// Item 3 of the review-flow brief: revisions sent from the editor update the
// editor. What he sends on a goblin's page without ending the review is a
// revision: the goblin gets it in its own terminal, the CFO is told, the item
// waits on the goblin's next version, and the next poll tells him on the page
// that it was received and what happens next. The review goes on.
func TestARevisionOnAPageReachesTheGoblinAndSaysWhatHappensNext(t *testing.T) {
	// Arrange
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	meta, goblin, connection, page := pageOfAGoblin(t, store)
	revision := "session:\n  status: feedback\nprompts[1]{uid,prompt,selector,tag,text}:\n  \"\",Make the cards bigger,\"\",message,Freeform message\n"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var replies []string
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(_ context.Context, file, reply string, _ time.Duration) (axi.PagePoll, error) {
		replies = append(replies, reply)
		if len(replies) == 1 {
			return axi.PagePoll{Status: "feedback", Prompts: []string{"Make the cards bigger"}, Output: revision}, nil
		}
		cancel()
		return axi.PagePoll{Status: "waiting"}, nil
	}}}

	// Act
	s.watchPages(ctx)
	s.pageWork.Wait()

	// Assert
	if len(replies) != 2 || replies[0] != "" || replies[1] != "Revision received. "+meta.ID+" makes the next version, which replaces this page." {
		t.Fatalf("replies = %q, want none, then that the revision was received and what happens next", replies)
	}
	if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "asked for a revision on your review page "+page+": Make the cards bigger") || !strings.Contains(told[0], "--lavish "+page) {
		t.Fatalf("the goblin got %q, want the revision and how to send its next version", told)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "open" || got.RevisingSince == nil {
		t.Fatalf("the wait = %+v, want it open, waiting on the goblin's next version", got)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "asked for a revision on the page "+page+", and "+meta.ID+" has it") {
		t.Fatalf("review wakes = %+v, want the CFO told the goblin has the revision", wakes)
	}
}

// The goblin's next version of the page replaces the page's item, the same
// item, waiting on the Overlord again, never a second item.
func TestTheNextVersionOfAPageReplacesItsItem(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, _, _, page := pageOfAGoblin(t, store)
	if err := store.reviseReview("waiting-"+meta.ID+"-7", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Act
	err := PublishWait(store.Home, meta.ID, 9, "pick a plan, now with bigger cards", pageLink, page, "")
	if err == nil {
		err = store.ingestReviews()
	}

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot().Reviews
	if len(got) != 1 || got[0].ID != "waiting-"+meta.ID+"-7" || got[0].Title != "Waiting on you: pick a plan, now with bigger cards" || got[0].State != "open" || got[0].RevisingSince != nil {
		t.Fatalf("reviews = %+v, want the one item, its next version waiting on the Overlord", got)
	}
}

// His answer on a goblin's page, sent with Send & End, reaches the goblin in
// its own terminal, and the CFO is told the goblin has it, so nobody has to
// relay it (0e: it reaches the goblin automatically).
func TestAnAnswerOnAPageReachesTheGoblinItself(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, goblin, connection, page := pageOfAGoblin(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		return axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Prompts: []string{"Approved as shown."}, Output: "session:\n  status: feedback\n"}, nil
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "answered on your review page "+page+": Approved as shown.") {
		t.Fatalf("the goblin got %q, want his answer", told)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.Reason != "You answered on its page; the goblin has it." {
		t.Fatalf("the wait = %+v, want it answered, saying the goblin has it", got)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "the goblin has it: Approved as shown.") {
		t.Fatalf("review wakes = %+v, want the CFO told the goblin has the answer", wakes)
	}
}

// The board never shows a page as waiting on him once its review has ended:
// when he ends it, every open item naming the page closes, not only the one
// its poller answered for, and the page is no longer watched.
func TestAnEndedReviewClosesEveryOpenItemOfItsPage(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	task, page := waitOnAPage(t, store)
	store.mu.Lock()
	older := store.db.Reviews[0]
	older.ID, older.Title, older.CreatedAt = "plan-review-1", "Look at the plan", older.CreatedAt.Add(-time.Minute)
	store.db.Reviews = append(store.db.Reviews, older)
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	store.mu.Unlock()
	polls := 0
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		polls++
		return axi.PagePoll{Status: "ended", EndedBy: "user"}, nil
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	for _, r := range store.Snapshot().Reviews {
		if r.State != "cleared" || r.Reason != "You ended the review on its page." || r.PageSettled == nil {
			t.Errorf("item %s = %+v, want it cleared as he ended its page's review, the page settled", r.ID, r)
		}
	}
	if polls != 1 {
		t.Errorf("polled the page %d times, want once: one poller for the page, none once it settled", polls)
	}
	if wakes := reviewWakes(t, h.State, task); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "ended the review of "+page) {
		t.Errorf("review wakes = %+v, want one saying he ended the review", wakes)
	}
}
