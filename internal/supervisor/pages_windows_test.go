package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	meta, _, _, connection := goblinFixture(t, store)
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
	if err := PublishWait(context.Background(), store.Home, connection.Herdr, meta.ID, 7, "pick a plan", pageLink, page); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	return meta.ID, page
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
// and the wait closes: an answer is kept whole for the CFO to relay.
func TestAPageWaitHandsWhatBecameOfThePageToTheCFO(t *testing.T) {
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	answer := "session:\n  status: feedback\nprompts[1]{id,text}:\n  p1,Ship option B\n"
	for name, test := range map[string]struct {
		polls []axi.PagePoll
		err   error
		want  string
	}{
		"an answer after a quiet poll":   {polls: []axi.PagePoll{{Status: "waiting"}, {Status: "feedback", Output: answer}}, want: "the Overlord answered on the page"},
		"an answer that ends the review": {polls: []axi.PagePoll{{Status: "feedback", Ended: true, Output: answer}}, want: "he ended the review"},
		"the review ended":               {polls: []axi.PagePoll{{Status: "ended"}}, want: "ended the review of"},
		"the window disconnected":        {polls: []axi.PagePoll{{Status: "browser_disconnected"}}, want: "ask him whether to reopen it"},
		"a page that cannot be polled":   {err: errors.New("No active Lavish Editor session for this file"), want: "cannot poll the page"},
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
			if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" {
				t.Errorf("the wait = %+v, want it closed once the CFO has it", got)
			}
		})
	}
}

// The CFO's own page is polled too, so the CFO never holds its turn on a
// poll: the Overlord's feedback comes back as a wake keyed by the item, since
// there is no goblin, telling the CFO to act on it rather than relay it.
func TestTheCFOsOwnPageReachesItAsAWakeKeyedByTheItem(t *testing.T) {
	store, h := testStore(t)
	_, _, _, cfo := primaryFixture(t, store)
	t.Setenv("CFO_SESSION_ID", "actual-primary")
	t.Setenv("CFO_SESSION_HARNESS", "codex")
	page := filepath.Join(h.Root, ".lavish", "dispatch-options.html")
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(page, []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PublishReview(context.Background(), h, cfo.Herdr, "", "dispatch-review-1", "Pick the dispatch order", pageLink, page, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	answer := "session:\n  status: feedback\nprompts[1]{id,text}:\n  p1,Payments first\n"
	s := &Service{Store: store, Options: Options{PollPage: func(_ context.Context, file string, _ time.Duration) (axi.PagePoll, error) {
		if file != page {
			t.Errorf("polled %s, want the CFO's page %s", file, page)
		}
		return axi.PagePoll{Status: "feedback", Output: answer}, nil
	}}}

	s.watchPages(context.Background())
	s.pageWork.Wait()

	wakes := reviewWakes(t, h.State, "dispatch-review-1")
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "the Overlord answered on the page "+page) || !strings.HasSuffix(wakes[0].Detail, ", act on it") {
		t.Fatalf("review wakes = %+v, want one keyed by the item telling the CFO to act on its page's feedback", wakes)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" {
		t.Errorf("the CFO's item = %+v, want it closed once the CFO has the feedback", got)
	}
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
	if err := os.Remove(ack); err != nil {
		t.Fatal(err)
	}
	s.pageWork.Wait()

	if wakes := reviewWakes(t, h.State, task); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "his feedback is in ") {
		t.Fatalf("review wakes = %+v, want the answer once the queue takes it", wakes)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" {
		t.Errorf("the wait = %+v, want it closed once the CFO has it", got)
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
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" {
		t.Errorf("the wait = %+v, want it closed once the CFO has it", got)
	}
}
