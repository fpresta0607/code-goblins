package supervisor

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// What lavish-axi's poll printed of the Overlord's answer to the mockups
// cg-afk-left-in-command-center put on a Scrawl page on 2026-10-09
// (state\reviews\feedback\waiting-cg-afk-left-in-command-center-1000090-*):
// his pick of the option the page declared, sent with Send decision, then
// Send & End with nothing written, 760 ms later.
const (
	pickedOnThePage = "session:\n" +
		"  status: feedback\n" +
		"prompts[1]{uid,prompt,selector,tag,text}:\n" +
		"  \"1\",Build it as shown,\"script[data-lavish-choices]\",choice,Build it as shown?\n" +
		"next_step: \"Apply the requested changes.\"\n"
	approvedAsShown = "session:\n" +
		"  status: feedback\n" +
		"  session_ended: true\n" +
		"  ended_by: user\n" +
		"prompts[1]{uid,prompt,selector,tag,text}:\n" +
		"  \"\",Approved as shown.,\"\",message,Freeform message\n" +
		"next_step: \"This was the last feedback before the user ended the session.\"\n"
	// A note he wrote on an element of the page, which Scrawl sends as soon
	// as he presses Enter in its card.
	notedOnThePage = "session:\n" +
		"  status: feedback\n" +
		"prompts[1]{uid,prompt,selector,tag,text}:\n" +
		"  u7,Make the header bigger,h1,element,Heading\n" +
		"next_step: \"Apply the requested changes.\"\n"
	scrawlWaiting        = "session:\n  status: waiting\n"
	scrawlEnded          = "session:\n  status: ended\n  ended_by: user\n"
	scrawlEndedByAnAgent = "session:\n  status: ended\n  ended_by: agent\n"
)

// betweenHisSends is how long after his pick his Send & End reached Scrawl
// on 2026-10-09 (12:46:26.673Z, then 12:46:27.430Z).
const betweenHisSends = 757 * time.Millisecond

// scrawlPage stands in for lavish-axi's poll of one page: what he sends waits
// on the page until a poll takes it, a poll that finds nothing waits for his
// next send until its timeout, and once a send ended the review every later
// poll says so. Each poll's process takes start to begin listening, as
// lavish-axi's own does. What a poll prints is read as lavish-axi's is.
type scrawlPage struct {
	start     time.Duration
	mu        sync.Mutex
	sent      []string
	ended     bool
	replies   []string
	arrived   chan struct{}
	listening chan struct{}
}

func newScrawlPage(start time.Duration) *scrawlPage {
	return &scrawlPage{start: start, arrived: make(chan struct{}, 1), listening: make(chan struct{}, 64)}
}

// send puts what he sent on the page, as lavish-axi prints it, in the page's
// queue.
func (p *scrawlPage) send(output string) {
	p.mu.Lock()
	p.sent = append(p.sent, output)
	p.mu.Unlock()
	select {
	case p.arrived <- struct{}{}:
	default:
	}
}

func (p *scrawlPage) poll(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
	select {
	case <-ctx.Done():
		return axi.PagePoll{}, ctx.Err()
	case <-time.After(p.start):
	}
	p.mu.Lock()
	p.replies = append(p.replies, reply)
	p.mu.Unlock()
	select {
	case p.listening <- struct{}{}:
	default:
	}
	expired := time.After(timeout)
	for {
		p.mu.Lock()
		output := ""
		switch {
		case len(p.sent) > 0:
			output, p.sent = p.sent[0], p.sent[1:]
			p.ended = p.ended || strings.Contains(output, "session_ended: true")
		case p.ended:
			output = scrawlEnded
		}
		p.mu.Unlock()
		if output != "" {
			return axi.Lavish{Commands: printed(output)}.Poll(ctx, file, reply, timeout)
		}
		select {
		case <-ctx.Done():
			return axi.PagePoll{}, ctx.Err()
		case <-expired:
			return axi.Lavish{Commands: printed(scrawlWaiting)}.Poll(ctx, file, reply, timeout)
		case <-p.arrived:
		}
	}
}

// pageReplies is what the supervisor has shown him on the page so far.
func (p *scrawlPage) pageReplies() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var shown []string
	for _, reply := range p.replies {
		if reply != "" {
			shown = append(shown, reply)
		}
	}
	return shown
}

// printed is a lavish-axi that prints output.
type printed string

func (output printed) Run(context.Context, execx.Request) (execx.Result, error) {
	return execx.Result{Stdout: []byte(output)}, nil
}

// boardReviews follows the board's event stream, as the Command Center does,
// and sends the review items each event carries as it arrives.
func boardReviews(t *testing.T, s *Service) <-chan []Review {
	t.Helper()
	handler := NewHTTP(s, "", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")}})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan []Review, 256)
	go func() {
		defer close(events)
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if data, isData := strings.CutPrefix(line, "data: "); isData {
				var event struct {
					Reviews []Review `json:"reviews"`
				}
				if json.Unmarshal([]byte(data), &event) == nil {
					events <- event.Reviews
				}
			}
		}
	}()
	return events
}

// feedbackFiles lists the feedback files kept for item id.
func feedbackFiles(t *testing.T, stateDir, id string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(stateDir, "reviews", "feedback", id+"-*.toon"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// His answer on a goblin's page shows done on the board within a second of
// his sending it, though the goblin is in a turn and takes typed text only at
// its next tool call. On 2026-10-09 cg-afk-left-in-command-center's item
// closed 14 s after his pick: the supervisor typed the pick into the goblin's
// terminal, which waited 5 s on the turn, before it read his Send & End, and
// typed again before it closed the item.
func TestAPageAnswerShowsDoneOnTheBoardWithinASecond(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, goblin, connection := goblinFixture(t, store)
	presentAPage(t, store, meta, 7, "mockups.html")
	goblin.startTurn(t)
	scrawl := newScrawlPage(time.Second)
	s, err := Start(context.Background(), h, Options{CFO: connection, PollPage: scrawl.poll})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	board := boardReviews(t, s)
	select {
	case <-scrawl.listening:
	case <-time.After(30 * time.Second):
		t.Fatal("the supervisor never polled the page")
	}

	// Act
	sent := time.Now()
	scrawl.send(pickedOnThePage)
	time.AfterFunc(betweenHisSends, func() { scrawl.send(approvedAsShown) })
	var done time.Duration
	for expired := time.After(time.Minute); done == 0; {
		select {
		case reviews, isOpen := <-board:
			if !isOpen {
				t.Fatal("the board's event stream ended")
			}
			if len(reviews) == 1 && reviews[0].State == "answered" {
				done = time.Since(sent)
			}
		case <-expired:
			t.Fatal("the board never showed his answer done")
		}
	}

	// Assert
	if done > time.Second {
		t.Fatalf("the board showed his answer done %s after he sent it, want under a second", done.Round(time.Millisecond))
	}
}

// The boards are told what his send did to its item the moment the page's
// poller does it: his answer closes the item, and a revision, once nothing
// more came within pageAnswerSettle, takes it off Waiting on you. Neither
// waits on the supervisor's cycle, which tells the boards only of what the
// cycle itself changed, so a change the poller made reached them at the next
// refresh, up to snapshotRefresh later. Here no cycle runs at all.
func TestTheBoardsAreToldWhatHisSendDidWithoutWaitingOnACycle(t *testing.T) {
	for name, test := range map[string]struct {
		sent     string
		within   time.Duration
		isShown  func(Review) bool
		expected string
	}{
		"his answer closes the item":               {pickedOnThePage, time.Second, func(r Review) bool { return r.State == "answered" }, "answered"},
		"his revision takes it off Waiting on you": {notedOnThePage, pageAnswerSettle + time.Second, func(r Review) bool { return r.State == "open" && r.RevisingSince != nil }, "open and revising"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, _ := testStore(t)
			_, _, connection, _ := pageOfAGoblin(t, store)
			scrawl := newScrawlPage(0)
			s := &Service{Store: store, Options: Options{CFO: connection, PollPage: scrawl.poll}, subscribers: map[chan struct{}]struct{}{}}
			board, unsubscribe := s.subscribe()
			defer unsubscribe()
			ctx, cancel := context.WithCancel(context.Background())
			defer func() {
				cancel()
				s.pageWork.Wait()
			}()
			s.watchPages(ctx)
			<-scrawl.listening

			// Act
			scrawl.send(test.sent)

			// Assert
			for expired := time.After(test.within); ; {
				select {
				case <-board:
					if items, _ := s.Items(); len(items.Reviews) == 1 && test.isShown(items.Reviews[0]) {
						return
					}
				case <-expired:
					items, _ := s.Items()
					t.Fatalf("the boards were not told the item is %s within %s, the items they would read = %+v", test.expected, test.within, items.Reviews)
				}
			}
		})
	}
}

// An approval with a note is one answer: what he sends on the page just
// before Send & End, a pick of the option the page declared or a note on an
// element, reaches the goblin and the CFO with his approval as one answer,
// and is never first read as a revision request, as his pick was on
// 2026-10-09 (wake 1000098, then 1000099).
func TestAnApprovalWithANoteIsOneAnswerAndNoRevision(t *testing.T) {
	for name, note := range map[string]struct{ sent, text string }{
		"a pick of the option the page declared": {pickedOnThePage, "Build it as shown"},
		"a note on an element":                   {notedOnThePage, "Make the header bigger"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, goblin, connection, page := pageOfAGoblin(t, store)
			scrawl := newScrawlPage(0)
			scrawl.send(note.sent)
			time.AfterFunc(betweenHisSends, func() { scrawl.send(approvedAsShown) })
			s := &Service{Store: store, Options: Options{CFO: connection, PollPage: scrawl.poll}}

			// Act
			s.watchPages(context.Background())
			s.pageWork.Wait()

			// Assert
			wakes := reviewWakes(t, h.State, meta.ID)
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "answered on the page "+page+" and ended the review") || !strings.Contains(wakes[0].Detail, note.text) || !strings.Contains(wakes[0].Detail, "Approved as shown.") || strings.Contains(wakes[0].Detail, "revision") {
				t.Fatalf("review wakes = %+v, want one saying he answered %q and approved, and no revision", wakes, note.text)
			}
			if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "answered on your review page "+page) || !strings.Contains(told[0], note.text) || !strings.Contains(told[0], "Approved as shown.") || strings.Contains(told[0], "revision") {
				t.Fatalf("the goblin got %q, want his note and his approval once, as his answer", told)
			}
			if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredIn != "page" || got.RevisingSince != nil || got.PageSettled == nil {
				t.Fatalf("the page's item = %+v, want it answered on its page, never revising, its page settled", got)
			}
			for _, reply := range scrawl.pageReplies() {
				if strings.Contains(reply, "Revision") {
					t.Errorf("the page told him %q, want no revision", reply)
				}
			}
			if kept := feedbackFiles(t, h.State, "waiting-"+meta.ID+"-7"); len(kept) != 1 {
				t.Errorf("feedback files = %q, want his one answer kept in one", kept)
			}
		})
	}
}

// His pick of the option a page declared answers the page though he does not
// end the review: its item closes as answered at once, the goblin and the CFO
// get the pick once, and the page stays watched until he ends it, which then
// adds nothing.
func TestAPickOfThePagesOptionAnswersItWithoutEndingTheReview(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, goblin, connection, page := pageOfAGoblin(t, store)
	scrawl := newScrawlPage(0)
	scrawl.send(pickedOnThePage)
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: scrawl.poll}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Act
	s.watchPages(ctx)
	var answered Review
	for deadline := time.Now().Add(15 * time.Second); answered.State != "answered"; time.Sleep(10 * time.Millisecond) {
		if answered = store.Snapshot().Reviews[0]; time.Now().After(deadline) {
			t.Fatalf("the page's item after his pick = %+v, want it answered", answered)
		}
	}
	watchedAfterThePick := len(store.watchedPages()) == 1
	told := goblin.waitForLines(t, 1)
	for deadline := time.Now().Add(15 * time.Second); len(reviewWakes(t, h.State, meta.ID)) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the CFO was never told his pick")
		}
	}
	scrawl.send(scrawlEnded)
	s.pageWork.Wait()

	// Assert
	if answered.AnsweredBy != "overlord" || answered.AnsweredIn != "page" || answered.RevisingSince != nil {
		t.Fatalf("the page's item after his pick = %+v, want it answered by him on its page, no revision", answered)
	}
	if !watchedAfterThePick {
		t.Error("the page stopped being watched at his pick, want it watched until he ends the review")
	}
	if len(told) != 1 || !strings.Contains(told[0], "answered on your review page "+page+": Build it as shown") {
		t.Fatalf("the goblin got %q, want his pick once", told)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "answered on the page "+page) || !strings.Contains(wakes[0].Detail, "Build it as shown") {
		t.Fatalf("review wakes = %+v, want the CFO told his pick once", wakes)
	}
	if got := store.Snapshot().Reviews[0]; got.PageSettled == nil {
		t.Errorf("the page's item after he ended the review = %+v, want its page settled", got)
	}
}

// An agent's end of the review is never his approval. What he sent just
// before an agent ended it stays a revision its goblin gets, and the CFO is
// told an agent ended the review, as it is when he had sent nothing.
func TestAnAgentsEndOfTheReviewIsNeverHisApproval(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, goblin, connection, page := pageOfAGoblin(t, store)
	scrawl := newScrawlPage(0)
	scrawl.send(notedOnThePage)
	scrawl.send(scrawlEndedByAnAgent)
	scrawl.send(scrawlEndedByAnAgent)
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: scrawl.poll}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	wakes := reviewWakes(t, h.State, meta.ID)
	isRevision := func(w wake.Record) bool {
		return strings.Contains(w.Detail, "asked for a revision on the page "+page)
	}
	isAgentsEnd := func(w wake.Record) bool {
		return strings.Contains(w.Detail, "an agent, not the Overlord, ended the review of "+page)
	}
	if len(wakes) != 2 || !slices.ContainsFunc(wakes, isRevision) || !slices.ContainsFunc(wakes, isAgentsEnd) {
		t.Fatalf("review wakes = %+v, want his revision and the agent's end, and no answer of his", wakes)
	}
	if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "asked for a revision on your review page "+page+": Make the header bigger") {
		t.Fatalf("the goblin got %q, want his note once, as a revision", told)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "withdrawn" || got.AnsweredBy != "" || got.Reason != "An agent ended the review on its page; the CFO was told." {
		t.Fatalf("the page's item = %+v, want it withdrawn as an agent ended its review, never answered by him", got)
	}
}

// A supervisor that stops while it waits for the rest of his answer passes on
// what he already sent: the poll consumed it, and Scrawl holds no other copy.
func TestASupervisorThatStopsStillPassesOnWhatHeSent(t *testing.T) {
	for name, test := range map[string]struct{ sent, told, text string }{
		"his pick of the option the page declared": {pickedOnThePage, "answered on the page ", "Build it as shown"},
		"his note on an element":                   {notedOnThePage, "asked for a revision on the page ", "Make the header bigger"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, connection, page := pageOfAGoblin(t, store)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			polls := 0
			s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
				if polls++; polls == 1 {
					return axi.Lavish{Commands: printed(test.sent)}.Poll(ctx, file, reply, timeout)
				}
				cancel()
				return axi.PagePoll{}, ctx.Err()
			}}}

			// Act
			s.watchPages(ctx)
			s.pageWork.Wait()

			// Assert
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, test.told+page) {
				t.Fatalf("review wakes = %+v, want the CFO told once what he sent before the supervisor stopped", wakes)
			}
			kept := feedbackFiles(t, h.State, "waiting-"+meta.ID+"-7")
			if len(kept) != 1 {
				t.Fatalf("feedback files = %q, want what he sent kept in one", kept)
			}
			if data, err := os.ReadFile(kept[0]); err != nil || !strings.Contains(string(data), test.text) {
				t.Errorf("the kept feedback = %q, %v, want what he sent: %q", data, err, test.text)
			}
		})
	}
}

// A poll that fails while the rest of his answer may be on its way is asked
// once more at once, so his pick and the approval after it are still one
// answer. Scrawl stops itself when its last review ends with no window and no
// poll connected, which fails the poll his Send & End arrives under: the next
// poll starts Scrawl again and takes what he sent.
func TestAPollThatFailsWhileHisAnswerGoesOnIsAskedAgainAtOnce(t *testing.T) {
	// Arrange
	defer func(pause time.Duration) { pagePollPause = pause }(pagePollPause)
	pagePollPause = time.Millisecond
	store, h := testStore(t)
	meta, goblin, connection, page := pageOfAGoblin(t, store)
	polls := 0
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
		output := scrawlEnded
		switch polls++; polls {
		case 1:
			output = pickedOnThePage
		case 2:
			return axi.PagePoll{}, errors.New("Lavish Editor server connection failed")
		case 3:
			output = approvedAsShown
		}
		return axi.Lavish{Commands: printed(output)}.Poll(ctx, file, reply, timeout)
	}}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "answered on the page "+page+" and ended the review") || !strings.Contains(wakes[0].Detail, "Build it as shown") || !strings.Contains(wakes[0].Detail, "Approved as shown.") {
		t.Fatalf("review wakes = %+v, want one saying he picked and approved", wakes)
	}
	if told := goblin.lines(t); len(told) != 1 || !strings.Contains(told[0], "answered on your review page "+page) {
		t.Fatalf("the goblin got %q, want his pick and his approval once, as his answer", told)
	}
	if got := store.Snapshot().Reviews[0]; got.State != "answered" || got.PageSettled == nil {
		t.Fatalf("the page's item = %+v, want it answered, its page settled", got)
	}
	if polls != 3 {
		t.Errorf("polled the page %d times, want 3: the failed poll asked again once", polls)
	}
}
