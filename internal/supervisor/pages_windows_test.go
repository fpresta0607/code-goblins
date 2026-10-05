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
	if err := PublishWait(store.Home, meta.ID, 7, "pick a plan", pageLink, page); err != nil {
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
	if err := PublishWait(store.Home, meta.ID, record.Seq+1, "pick a store on the page", pageLink, page); err != nil {
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
	if err := PublishWait(store.Home, meta.ID, record.Seq+1, "pick a store on the page", pageLink, page); err != nil {
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
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file string, _ time.Duration) (axi.PagePoll, error) {
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
		"an answer after a quiet poll":   {polls: []axi.PagePoll{{Status: "waiting"}, {Status: "feedback", Output: answer}}, want: "the Overlord answered on the page", closed: answered},
		"an answer that ends the review": {polls: []axi.PagePoll{{Status: "feedback", Ended: true, EndedBy: "user", Output: answer}}, want: "he ended the review", closed: answered},
		"the Overlord ended the review":  {polls: []axi.PagePoll{{Status: "ended", EndedBy: "user"}}, want: "the Overlord ended the review of", closed: Review{State: "cleared", Reason: "You ended the review on its page."}},
		"an agent ended the review":      {polls: []axi.PagePoll{{Status: "ended", EndedBy: "agent"}}, want: "an agent, not the Overlord, ended the review of", closed: Review{State: "withdrawn", Reason: "An agent ended the review on its page; the CFO was told."}},
		// A closed review window is not the end of the review: his answers
		// queue on the page, and reopening it resumes the same review.
		"an answer after the window disconnected": {polls: []axi.PagePoll{{Status: "browser_disconnected"}, {Status: "waiting"}, {Status: "feedback", Output: answer}}, want: "the Overlord answered on the page", closed: answered},
		"a page that cannot be polled":            {err: errors.New("No active Lavish Editor session for this file"), want: "cannot poll the page", closed: Review{State: "withdrawn", Reason: "The page could not be polled; the CFO was told."}},
	} {
		t.Run(name, func(t *testing.T) {
			store, h := testStore(t)
			task, page := waitOnAPage(t, store)
			polls := 0
			s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file string, timeout time.Duration) (axi.PagePoll, error) {
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
	polls := []axi.PagePoll{{Status: "browser_disconnected"}, {Status: "feedback", Output: answer}}
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file string, _ time.Duration) (axi.PagePoll, error) {
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
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _ string, _ time.Duration) (axi.PagePoll, error) {
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

// A wait that closes stops its poll, and nothing reaches the CFO for it.
func TestAClosedPageWaitStopsItsPoller(t *testing.T) {
	store, h := testStore(t)
	task, _ := waitOnAPage(t, store)
	polling := make(chan struct{})
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _ string, _ time.Duration) (axi.PagePoll, error) {
		close(polling)
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	s.watchPages(context.Background())
	<-polling

	if err := store.withdrawReview(store.Snapshot().Reviews[0].ID, "the goblin reported again"); err != nil {
		t.Fatal(err)
	}
	s.watchPages(context.Background())

	stopped := make(chan struct{})
	go func() {
		s.pageWork.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the poller of a closed wait is still running")
	}
	if wakes := reviewWakes(t, h.State, task); len(wakes) != 0 {
		t.Fatalf("review wakes = %+v, want none for a wait that closed", wakes)
	}
}

// Only an open item with a page is polled, and each has one poller.
func TestPagesArePolledOncePerOpenWait(t *testing.T) {
	store, _ := testStore(t)
	waitOnAPage(t, store)
	polls := make(chan struct{}, 4)
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _ string, _ time.Duration) (axi.PagePoll, error) {
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
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, time.Duration) (axi.PagePoll, error) {
		return axi.PagePoll{Status: "feedback", Output: "session:\n  status: feedback\n"}, nil
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
	s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, time.Duration) (axi.PagePoll, error) {
		return axi.PagePoll{Status: "feedback", Output: answer}, nil
	}}}

	s.watchPages(context.Background())
	s.pageWork.Wait()

	wakes := reviewWakes(t, h.State, task)
	if len(wakes) != 1 {
		t.Fatalf("review wakes = %+v, want the answer", wakes)
	}
	detail := wakes[0].Detail
	if !strings.Contains(detail, "could not be saved") || !strings.Contains(detail, "(cut to its end) ...") || !strings.HasSuffix(detail, " Ship option B\"\n") || len(detail) > pageFeedbackInline+500 || !utf8.ValidString(detail) {
		t.Fatalf("wake detail = %q (%d bytes), want the answer's end inline, bounded, and the cut said", detail, len(detail))
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredIn != "page" {
		t.Errorf("the wait = %+v, want it answered on its page once the CFO has it", got)
	}
}

// A native goblin's board answer that waits behind its turn closes the page
// that carries its question as soon as it is sent, so an answer on that page
// never reaches the goblin as a second answer. Neither the warning nor the
// late report that follows closes a page the goblin opened after it.
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
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file string, _ time.Duration) (axi.PagePoll, error) {
		polled <- file
		return axi.PagePoll{Status: "feedback", Output: "Go with Postgres"}, nil
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
	store.mu.Lock()
	store.db.Sessions["codex/worker-1"] = Session{ID: "codex/worker-1", NativeID: "worker-1", Harness: "codex", Role: "goblin", Phase: "active", TaskID: meta.ID, Generation: meta.SpawnGen, PromptAt: since.Add(deliveryQuiet + time.Minute)}
	store.mu.Unlock()
	delivered := store.settleDeliveries(since.Add(deliveryQuiet+2*time.Minute), idle)
	settled := store.Snapshot()

	// Assert
	if warned != nil || delivered != nil {
		t.Fatal(warned, delivered)
	}
	if a := sent.Actions[slices.IndexFunc(sent.Actions, func(a Action) bool { return a.ID == "board-answer" })]; a.Status != "running" || a.Awaiting == nil {
		t.Fatalf("the board answer = %s, want it sent and awaiting the goblin's hook", a.Status)
	}
	if r := sent.Reviews[0]; r.State != "answered" || r.AnsweredBy != "overlord" || r.AnsweredIn != "question" {
		t.Errorf("the page's item once the answer was sent = %+v, want it answered by the Overlord through its question", r)
	}
	if len(polled) != 0 || len(reviewWakes(t, h.State, meta.ID)) != 0 {
		t.Errorf("polled %d pages and woke the CFO %d times, want the closed page never relayed as a second answer", len(polled), len(reviewWakes(t, h.State, meta.ID)))
	}
	if a := settled.Actions[slices.IndexFunc(settled.Actions, func(a Action) bool { return a.ID == "board-answer" })]; a.Status != "succeeded" {
		t.Errorf("the board answer after the late report = %s, want it delivered", a.Status)
	}
	if i := slices.IndexFunc(settled.Reviews, func(r Review) bool { return r.ID == "plan-later" }); i < 0 || settled.Reviews[i].State != "open" {
		t.Errorf("a page the goblin opened after the answer was sent = %+v, want it still open", settled.Reviews)
	}
}
