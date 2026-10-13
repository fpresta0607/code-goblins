package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestPageReceiptIsDurableAndAcknowledgedBeforeSettling(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, connection, _ := pageOfAGoblin(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan string, 1)
	polls := 0
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
		polls++
		if polls == 1 {
			return axi.Lavish{Commands: printed(notedOnThePage)}.Poll(ctx, file, reply, timeout)
		}
		second <- reply
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	t.Cleanup(func() { cancel(); s.pageWork.Wait() })

	// Act
	s.watchPages(ctx)
	select {
	case reply := <-second:
		// Assert
		if reply != "Received." {
			t.Errorf("settling poll reply = %q, want receipt without claiming delivery", reply)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the settling poll never started")
	}
	data, err := os.ReadFile(store.pageReceiptsPath())
	if err != nil || !strings.Contains(string(data), "Make the header bigger") {
		t.Errorf("durable receipt journal lacks consumed feedback before settling: %v", err)
	}
	if kept := feedbackFiles(t, h.State, "waiting-"+meta.ID+"-7"); len(kept) != 1 {
		t.Errorf("feedback files before settling = %q, want the first receipt kept", kept)
	}
}

func TestPageReceiptRecoversGroupingAfterRestart(t *testing.T) {
	for name, ending := range map[string]string{"user approval": approvedAsShown, "agent end": scrawlEndedByAnAgent} {
		t.Run(name, func(t *testing.T) {
			// Arrange: capture durable state while the settling poll is blocked,
			// then open that exact crash image in another scratch home.
			store, _ := testStore(t)
			meta, _, connection, _ := pageOfAGoblin(t, store)
			ctx, cancel := context.WithCancel(context.Background())
			settling := make(chan struct{})
			polls := 0
			s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
				polls++
				if polls == 1 {
					return axi.Lavish{Commands: printed(notedOnThePage)}.Poll(ctx, file, reply, timeout)
				}
				close(settling)
				<-ctx.Done()
				return axi.PagePoll{}, ctx.Err()
			}}}
			s.watchPages(ctx)
			select {
			case <-settling:
			case <-time.After(10 * time.Second):
				cancel()
				t.Fatal("no settling poll")
			}
			crashed, err := fsx.ReadFile(store.pageReceiptsPath())
			board, boardErr := fsx.ReadFile(store.path())
			cancel()
			s.pageWork.Wait()
			if err != nil {
				t.Fatal(err)
			}
			if boardErr != nil {
				t.Fatal(boardErr)
			}
			restarted, recoveryHome := testStore(t)
			if err := fsx.AtomicWriteFile(restarted.path(), board); err != nil {
				t.Fatal(err)
			}
			if err := fsx.AtomicWriteFile(restarted.pageReceiptsPath(), crashed); err != nil {
				t.Fatal(err)
			}
			restarted, err = Open(recoveryHome)
			if err != nil {
				t.Fatal(err)
			}
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			var sent []string
			typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error {
				sent = append(sent, text)
				return nil
			}
			scrawl := newScrawlPage(0)
			scrawl.send(ending)
			if name == "agent end" {
				scrawl.send(ending)
			}
			recovered := &Service{Store: restarted, Options: Options{CFO: connection, PollPage: scrawl.poll}}

			// Act
			recovered.watchPages(context.Background())
			recovered.pageWork.Wait()
			recovered.watchPages(context.Background())
			recovered.pageWork.Wait()

			// Assert
			if len(sent) != 1 || !strings.Contains(sent[0], "Make the header bigger") {
				t.Fatalf("recovered delivery = %q, want the consumed note exactly once", sent)
			}
			wakes := reviewWakes(t, recoveryHome.State, meta.ID)
			if name == "user approval" {
				if !strings.Contains(sent[0], "Approved as shown.") || strings.Contains(sent[0], "revision") || len(wakes) != 1 {
					t.Errorf("recovered approval = %q, wakes = %+v, want one grouped answer", sent, wakes)
				}
			} else if !strings.Contains(sent[0], "revision") || len(wakes) != 2 || !strings.Contains(wakes[1].Detail, "an agent, not the Overlord") || restarted.Snapshot().Reviews[0].State != "withdrawn" {
				t.Errorf("recovered agent end = %q, wakes = %+v, want revision then agent end", sent, wakes)
			}
		})
	}
}

func TestPageReceiptsStayOrderedAndDoNotReplayUncertainDelivery(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, _, connection, page := pageOfAGoblin(t, store)
	if err := store.closePage(pageKey(page), Review{State: "answered"}); err != nil {
		t.Fatal(err)
	}
	previous := typeIntoGoblin
	release, typing, drained := make(chan struct{}), make(chan struct{}, 1), make(chan struct{})
	var mu sync.Mutex
	var sent []string
	typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error {
		mu.Lock()
		sent = append(sent, text)
		mu.Unlock()
		select {
		case typing <- struct{}{}:
		default:
		}
		<-release
		return nil
	}
	scrawl := newScrawlPage(0)
	polls := 0
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
		polls++
		switch polls {
		case 1, 3, 5:
			text := []string{"first", "second", "third"}[(polls-1)/2]
			return axi.PagePoll{Status: "feedback", Prompts: []string{text}, Output: text}, nil
		case 2, 4, 6:
			return axi.PagePoll{Status: "waiting"}, nil
		default:
			close(drained)
			return axi.PagePoll{Status: "ended", EndedBy: "user"}, nil
		}
	}}}
	isReleased := false
	t.Cleanup(func() {
		if !isReleased {
			close(release)
		}
		s.pageWork.Wait()
		typeIntoGoblin = previous
	})

	// Act
	s.watchPages(context.Background())
	select {
	case <-typing:
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery attempt")
	}
	select {
	case <-drained:
	case <-time.After(10 * time.Second):
		t.Fatal("polling did not drain behind first delivery")
	}
	crashed, err := fsx.ReadFile(store.pageReceiptsPath())
	if err != nil {
		t.Fatal(err)
	}
	board, err := fsx.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	isReleased = true
	s.pageWork.Wait()

	// Assert: the live queue delivers in order once.
	if len(sent) != 3 {
		t.Fatalf("sent = %q, want three ordered notes", sent)
	}
	for i, word := range []string{"first", "second", "third"} {
		if !strings.Contains(sent[i], ": "+word) {
			t.Errorf("delivery %d = %q, want %s", i, sent[i], word)
		}
	}
	restarted, recoveryHome := testStore(t)
	if err := fsx.AtomicWriteFile(restarted.path(), board); err != nil {
		t.Fatal(err)
	}
	if err := fsx.AtomicWriteFile(restarted.pageReceiptsPath(), crashed); err != nil {
		t.Fatal(err)
	}
	restarted, err = Open(recoveryHome)
	if err != nil {
		t.Fatal(err)
	}
	sent = nil
	scrawl.send(scrawlEnded)
	recovered := &Service{Store: restarted, Options: Options{CFO: connection, PollPage: scrawl.poll}}
	recovered.watchPages(context.Background())
	recovered.pageWork.Wait()
	recovered.watchPages(context.Background())
	recovered.pageWork.Wait()
	if len(sent) != 0 {
		t.Errorf("replayed uncertain delivery or overtook it: %q", sent)
	}
	wakes := reviewWakes(t, recoveryHome.State, meta.ID)
	if len(wakes) != 3 {
		t.Fatalf("recovery wakes = %+v, want three ordered receipts", wakes)
	}
	for i, word := range []string{"first", "second", "third"} {
		if !strings.Contains(wakes[i].Detail, "inspect") {
			t.Errorf("wake %d = %q, want explicit uncertainty", i, wakes[i].Detail)
		}
		files := feedbackFiles(t, recoveryHome.State, meta.ID)
		hasFeedback := false
		for _, file := range files {
			if strings.Contains(wakes[i].Detail, file) {
				data, err := os.ReadFile(file)
				hasFeedback = err == nil && string(data) == word
			}
		}
		if !hasFeedback {
			t.Errorf("wake %d does not name saved %q feedback: %s", i, word, wakes[i].Detail)
		}
	}
}

func TestClosedPageKeepsPollingBehindBusyDelivery(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, connection, page := pageOfAGoblin(t, store)
	if err := store.closePage(pageKey(page), Review{State: "answered"}); err != nil {
		t.Fatal(err)
	}
	previous := typeIntoGoblin
	release := make(chan struct{})
	typeIntoGoblin = func(context.Context, string, state.TaskMeta, string) error { <-release; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	polledAgain := make(chan string, 1)
	polls := 0
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: func(ctx context.Context, file, reply string, timeout time.Duration) (axi.PagePoll, error) {
		polls++
		switch polls {
		case 1:
			return axi.Lavish{Commands: printed(notedOnThePage)}.Poll(ctx, file, reply, timeout)
		case 2:
			return axi.PagePoll{Status: "waiting"}, nil
		default:
			polledAgain <- reply
			<-ctx.Done()
			return axi.PagePoll{}, ctx.Err()
		}
	}}}
	t.Cleanup(func() { cancel(); close(release); s.pageWork.Wait(); typeIntoGoblin = previous })

	// Act
	s.watchPages(ctx)

	// Assert
	select {
	case <-polledAgain:
	case <-time.After(time.Second):
		t.Fatal("a closed item's note blocked the next poll on terminal delivery")
	}
}

func TestPageSweepKeepsPollingBehindBusyDelivery(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	meta, _, connection, page := pageOfAGoblin(t, store)
	previous := typeIntoGoblin
	release := make(chan struct{})
	typeIntoGoblin = func(context.Context, string, state.TaskMeta, string) error { <-release; return nil }
	s := &Service{Store: store, Options: Options{CFO: connection}}
	polledAgain := make(chan string, 1)
	polls := 0
	s.Options.PollPage = func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		polls++
		if polls == 1 {
			return axi.PagePoll{Status: "feedback", Prompts: []string{"First note"}, Output: "First note"}, nil
		}
		polledAgain <- "polled"
		return axi.PagePoll{Status: "waiting"}, nil
	}
	finished := make(chan error, 1)
	t.Cleanup(func() { close(release); <-finished; s.pageWork.Wait(); typeIntoGoblin = previous })

	// Act
	go func() { finished <- s.takePage(context.Background(), page, pageOwner{task: meta.ID, key: meta.ID}, "") }()

	// Assert
	select {
	case <-polledAgain:
	case <-time.After(time.Second):
		t.Fatal("a swept note blocked the next poll on terminal delivery")
	}
}

func TestPageDeliveryDoesNotCloseANewerReview(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, _, page := pageOfAGoblin(t, store)
	r := store.Snapshot().Reviews[0]
	if err := store.closePage(pageKey(page), Review{State: "answered", Reason: pageAnswerReceived}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	later := r
	later.ID, later.CreatedAt = "later-review", time.Now().Add(time.Minute)
	store.db.Reviews = append(store.db.Reviews, later)
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	answer := pageAnswer{ID: "earlier-answer", Item: r, Poll: axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "Approved"}, IsAnswer: true, Status: "queued"}
	if err := store.keepPageAnswer(answer); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store}

	// Act
	if err := s.tellAnswer(context.Background(), answer); err != nil {
		t.Fatal(err)
	}

	// Assert
	if got, _ := store.review(later.ID); got.State != "open" || got.PageSettled != nil {
		t.Fatalf("later review = %+v, want earlier delivery to leave it alone", got)
	}
}

func TestPageDeliveryDoesNotRetrySendingAfterAResultSaveFailure(t *testing.T) {
	// Arrange: a failed result save leaves the durable pre-send intent intact.
	store, _ := testStore(t)
	_, _, connection, _ := pageOfAGoblin(t, store)
	r := store.Snapshot().Reviews[0]
	r.State = "answered"
	answer := pageAnswer{ID: "uncertain-result", Item: r, Poll: axi.PagePoll{Status: "feedback", Output: "Make it bigger"}, Status: "sending"}
	if err := store.keepPageAnswer(answer); err != nil {
		t.Fatal(err)
	}
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	attempts := 0
	typeIntoGoblin = func(context.Context, string, state.TaskMeta, string) error { attempts++; return nil }
	s := &Service{Store: store, Options: Options{CFO: connection}}

	// Act
	if err := s.tellAnswer(context.Background(), answer); err != nil {
		t.Fatal(err)
	}

	// Assert
	if attempts != 0 {
		t.Fatalf("retried a possibly delivered message %d times", attempts)
	}
}

func TestPageSweepAndWatchShareOnePoller(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, _, _, page := pageOfAGoblin(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	polled := make(chan struct{}, 3)
	s := &Service{Store: store, Options: Options{
		PageSessions: func() ([]axi.PageSession, error) { return []axi.PageSession{{File: page, Pending: 1}}, nil },
		PollPage: func(ctx context.Context, _, _ string, _ time.Duration) (axi.PagePoll, error) {
			polled <- struct{}{}
			<-ctx.Done()
			return axi.PagePoll{}, ctx.Err()
		},
	}}
	finished := make(chan error, 1)
	t.Cleanup(func() { cancel(); <-finished; s.pageWork.Wait() })

	// Act
	go func() { finished <- s.sweepSessions(ctx) }()
	select {
	case <-polled:
	case <-time.After(5 * time.Second):
		t.Fatal("sweep never polled")
	}
	s.watchPages(ctx)

	// Assert
	select {
	case <-polled:
		t.Fatal("watcher polled a page the sweep was already consuming")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPageReceiptReportsQueuedDeliveryWithoutClaimingItWasRead(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, connection, _ := pageOfAGoblin(t, store)
	previous := typeIntoGoblin
	t.Cleanup(func() { typeIntoGoblin = previous })
	typeIntoGoblin = func(context.Context, string, state.TaskMeta, string) error { return fleet.ErrQueuedForToolCall }
	scrawl := newScrawlPage(0)
	scrawl.send(approvedAsShown)
	s := &Service{Store: store, Options: Options{CFO: connection, PollPage: scrawl.poll}}

	// Act
	s.watchPages(context.Background())
	s.pageWork.Wait()

	// Assert
	wakes := reviewWakes(t, h.State, meta.ID)
	if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "queued") || strings.Contains(wakes[0].Detail, "has it") {
		t.Errorf("queued delivery wake = %+v, want queued without a read claim", wakes)
	}
	if reason := store.Snapshot().Reviews[0].Reason; !strings.Contains(reason, "queued") || strings.Contains(reason, "has it") {
		t.Errorf("queued delivery reason = %q, want queued without a read claim", reason)
	}
}

func TestPageReceiptsRecoverQueuedAndNotifyingOnce(t *testing.T) {
	for _, status := range []string{"queued", "notifying"} {
		t.Run(status, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, connection, page := pageOfAGoblin(t, store)
			r := store.Snapshot().Reviews[0]
			r.State = "answered"
			if err := store.settlePage(pageKey(page), r); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"first", "second"} {
				answer := pageAnswer{ID: id, Item: r, Poll: axi.PagePoll{Status: "feedback", Prompts: []string{id}, Output: id}, Status: status}
				if status == "notifying" {
					answer.Detail = "Already sent " + id
					if _, _, err := wake.AppendFirst(h.State, "page-answer:"+id, "review", meta.ID, answer.Detail); err != nil {
						t.Fatal(err)
					}
				}
				if err := store.keepPageAnswer(answer); err != nil {
					t.Fatal(err)
				}
			}
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			var sent []string
			typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error {
				sent = append(sent, text)
				return nil
			}

			// Act
			for range 2 {
				restarted, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				s := &Service{Store: restarted, Options: Options{CFO: connection}}
				s.watchPages(context.Background())
				s.pageWork.Wait()
			}

			// Assert
			if status == "queued" {
				if len(sent) != 2 || !strings.Contains(sent[0], ": first") || !strings.Contains(sent[1], ": second") {
					t.Errorf("queued recovery sent = %q, want first then second once", sent)
				}
			} else if len(sent) != 0 {
				t.Errorf("replayed known terminal result: %q", sent)
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 2 {
				t.Errorf("recovery wakes = %+v, want two once only", wakes)
			}
		})
	}
}

func TestPageReceiptKeepsFullFeedbackUntilItsFileCanBeSaved(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, _, _ := pageOfAGoblin(t, store)
	feedbackDir := filepath.Join(h.State, "reviews", "feedback")
	if err := os.MkdirAll(filepath.Dir(feedbackDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feedbackDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	output := "beginning must survive " + strings.Repeat("notes ", pageFeedbackInline)
	r := store.Snapshot().Reviews[0]
	answer := pageAnswer{ID: "unwritable-feedback", Item: r, Poll: axi.PagePoll{Status: "feedback", Output: output}, Status: "queued"}
	if err := store.keepPageAnswer(answer); err != nil {
		t.Fatal(err)
	}
	s := &Service{Store: store}

	// Act
	s.tellPage(context.Background(), pageKey(r.LavishPage))
	s.pageWork.Wait()

	// Assert
	pending := store.pageReceiptsSnapshot().Answers
	if len(pending) != 1 || pending[0].Poll.Output != output {
		t.Fatal("unsaved full feedback was discarded after a bounded wake")
	}
	if err := os.Remove(feedbackDir); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	recovered := &Service{Store: restarted}
	recovered.watchPages(context.Background())
	recovered.pageWork.Wait()
	if len(restarted.pageReceiptsSnapshot().Answers) != 0 {
		t.Error("repaired feedback file still has a pending receipt")
	}
	if files := feedbackFiles(t, h.State, r.ID); len(files) != 1 {
		t.Fatalf("recovered files = %q, want one", files)
	} else if data, err := os.ReadFile(files[0]); err != nil || string(data) != output {
		t.Errorf("full feedback not restored: %v", err)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 {
		t.Errorf("recovery duplicated wake: %+v", wakes)
	}
}

func TestPageReceiptIsIndependentOfSupervisorCapacity(t *testing.T) {
	// Arrange: each poll fits Scrawl's limit; the grouped receipt exceeds 8 MiB.
	store, h := testStore(t)
	pageOfAGoblin(t, store)
	store.mu.Lock()
	store.db.Reviews = append(store.db.Reviews, Review{ID: "other-review", State: "answered", Reason: strings.Repeat("x", 7<<20)})
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	previousPause := pagePollPause
	pagePollPause = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	settling := make(chan string, 1)
	output := "complete feedback " + strings.Repeat("y", 1<<20)
	polls := 0
	s := &Service{Store: store, Options: Options{PollPage: func(ctx context.Context, _, reply string, _ time.Duration) (axi.PagePoll, error) {
		polls++
		if polls <= 4 {
			return axi.PagePoll{Status: "feedback", Output: output, Prompts: []string{output}}, nil
		}
		settling <- reply
		<-ctx.Done()
		return axi.PagePoll{}, ctx.Err()
	}}}
	t.Cleanup(func() { cancel(); s.pageWork.Wait(); pagePollPause = previousPause })

	// Act
	s.watchPages(ctx)

	// Assert
	select {
	case reply := <-settling:
		if reply != "Received." {
			t.Fatalf("receipt reply = %q", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("journal capacity prevented acknowledgment and continued grouping")
	}
	wanted := strings.Join([]string{output, output, output, output}, "\n")
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	pending := restarted.pageReceiptsSnapshot().Answers
	if len(pending) != 1 || pending[0].Poll.Output != wanted || len(pending[0].Poll.Prompts) != 4 {
		t.Fatal("restart lost the full grouped receipt at supervisor capacity")
	}
	files := feedbackFiles(t, h.State, store.Snapshot().Reviews[0].ID)
	if len(files) != 1 {
		t.Fatalf("feedback files = %q, want one complete grouped receipt", files)
	}
	if data, err := os.ReadFile(files[0]); err != nil || string(data) != wanted {
		t.Fatalf("readable grouped feedback differs: %v", err)
	}
}

func TestPageReceiptCommitsJournalBeforeExtendingReadableFeedback(t *testing.T) {
	// Arrange: the feedback directory is writable but the journal cannot commit.
	store, h := testStore(t)
	pageOfAGoblin(t, store)
	s := &Service{Store: store}
	answer := pageAnswer{ID: "grouped", Item: store.Snapshot().Reviews[0], Poll: axi.PagePoll{Output: "earlier note", Prompts: []string{"earlier note"}}, Status: "collecting"}
	if !s.keepAnswer(context.Background(), answer) {
		t.Fatal("initial receipt failed")
	}
	backup := store.pageReceiptsPath() + ".before"
	if err := os.Rename(store.pageReceiptsPath(), backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.pageReceiptsPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	answer.Poll = together(answer.Poll, axi.PagePoll{Output: "later approval", Prompts: []string{"later approval"}, Ended: true, EndedBy: "user"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	if s.keepAnswer(ctx, answer) {
		t.Fatal("journal replacement unexpectedly succeeded")
	}

	// Assert: a crash cannot expose newer raw feedback beside older metadata.
	files := feedbackFiles(t, h.State, answer.Item.ID)
	if len(files) != 1 {
		t.Fatalf("feedback files = %q", files)
	}
	if data, err := os.ReadFile(files[0]); err != nil || string(data) != "earlier note" {
		t.Fatalf("readable feedback advanced before its journal: %q, %v", data, err)
	}
	if got := store.pageReceiptsSnapshot().Answers; len(got) != 1 || got[0].Poll.Output != "earlier note" {
		t.Fatal("failed journal write changed the committed receipt")
	}
	if err := os.Remove(store.pageReceiptsPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, store.pageReceiptsPath()); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(h)
	if err != nil || restarted.pageReceiptsSnapshot().Answers[0].Poll.Output != "earlier note" {
		t.Fatalf("failed write changed restart contents: %v", err)
	}
}

func TestPageReceiptRecoversLatestGroupAroundReadableWrite(t *testing.T) {
	for _, stage := range []string{"before readable write", "after readable write"} {
		t.Run(stage, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, connection, _ := pageOfAGoblin(t, store)
			s := &Service{Store: store}
			answer := pageAnswer{ID: "grouped", Item: store.Snapshot().Reviews[0], Poll: axi.PagePoll{Output: "earlier note", Prompts: []string{"earlier note"}}, Status: "collecting"}
			if !s.keepAnswer(context.Background(), answer) {
				t.Fatal("initial receipt failed")
			}
			answer.Poll = together(answer.Poll, axi.PagePoll{Output: "later approval", Prompts: []string{"later approval"}, Ended: true, EndedBy: "user"})
			answer.IsAnswer = true
			if err := store.keepPageAnswer(answer); err != nil {
				t.Fatal(err)
			}
			if stage == "after readable write" {
				if _, err := s.savePageAnswer(answer); err != nil {
					t.Fatal(err)
				}
			}
			previous := typeIntoGoblin
			t.Cleanup(func() { typeIntoGoblin = previous })
			var sent []string
			typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error {
				sent = append(sent, text)
				return nil
			}

			// Act
			for range 2 {
				restarted, err := Open(h)
				if err != nil {
					t.Fatal(err)
				}
				recovered := &Service{Store: restarted, Options: Options{CFO: connection, PollPage: newScrawlPage(0).poll}}
				recovered.watchPages(context.Background())
				recovered.pageWork.Wait()
			}

			// Assert
			if len(sent) != 1 || !strings.Contains(sent[0], "earlier note later approval") {
				t.Fatalf("recovered group = %q, want complete grouped approval once", sent)
			}
			files := feedbackFiles(t, h.State, answer.Item.ID)
			if len(files) != 1 {
				t.Fatalf("feedback files = %q", files)
			}
			if data, err := os.ReadFile(files[0]); err != nil || string(data) != "earlier note\nlater approval" {
				t.Fatalf("readable feedback lost newer contents: %q, %v", data, err)
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 {
				t.Errorf("recovered group wakes = %+v, want one", wakes)
			}
		})
	}
}
