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
	meta, _, connection, _ := pageOfAGoblin(t, store)
	if err := store.closePage(store.Snapshot().Reviews[0], Review{State: "answered"}); err != nil {
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
	_, _, connection, _ := pageOfAGoblin(t, store)
	if err := store.closePage(store.Snapshot().Reviews[0], Review{State: "answered"}); err != nil {
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
	pageOfAGoblin(t, store)
	r := store.Snapshot().Reviews[0]
	if err := store.closePage(store.Snapshot().Reviews[0], Review{State: "answered", Reason: pageAnswerReceived}); err != nil {
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

func TestPageReceiptRecoveryLeavesRepublishedVersionOpen(t *testing.T) {
	for _, kind := range []string{"approval", "pick", "revision", "agent end", "user end"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange: the receipt committed, but its board mutation did not.
			store, h := testStore(t)
			meta, _, connection, page := pageOfAGoblin(t, store)
			original := store.Snapshot().Reviews[0]
			answer := pageAnswer{ID: "before-republish", Item: original, Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Prompts: []string{"Old feedback"}, Output: "Old feedback"}}
			switch kind {
			case "approval":
				answer.IsAnswer, answer.Poll.Ended, answer.Poll.EndedBy = true, true, "user"
			case "pick":
				answer.IsAnswer, answer.Poll.Picked = true, true
			case "agent end", "user end":
				answer.IsPageEnd, answer.Poll.Status = true, "ended"
				answer.Closed = Review{State: "withdrawn", Reason: "Old agent end"}
				if kind == "user end" {
					answer.Closed = Review{State: "cleared", Reason: "Old user end"}
				}
				answer.Detail = answer.Closed.Reason
			}
			if err := store.keepPageAnswer(answer); err != nil {
				t.Fatal(err)
			}
			if err := PublishWait(h, meta.ID, 9, "review the new version", pageLink, page, ""); err != nil {
				t.Fatal(err)
			}
			restarted, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			latest := restarted.Snapshot().Reviews[0]
			if latest.ID != original.ID || latest.Identity != original.Identity || !latest.CreatedAt.After(original.CreatedAt) {
				t.Fatalf("republish did not replace the same review ID with a new version: %+v", latest)
			}
			previous := typeIntoGoblin
			sent := make(chan string, 2)
			typeIntoGoblin = func(_ context.Context, _ string, _ state.TaskMeta, text string) error { sent <- text; return nil }
			ctx, cancel := context.WithCancel(context.Background())
			polled := make(chan string, 1)
			recovered := &Service{Store: restarted, Options: Options{CFO: connection, PollPage: func(ctx context.Context, _, reply string, _ time.Duration) (axi.PagePoll, error) {
				polled <- reply
				<-ctx.Done()
				return axi.PagePoll{}, ctx.Err()
			}}}
			t.Cleanup(func() { cancel(); recovered.pageWork.Wait(); typeIntoGoblin = previous })

			// Act: use the startup order, ingesting the new version before recovery.
			recovered.watchPages(ctx)
			select {
			case reply := <-polled:
				if reply != "" {
					t.Errorf("old version's reply reached the new page: %q", reply)
				}
			case <-time.After(5 * time.Second):
				t.Error("receipt recovery stopped polling the new version")
			}

			// Assert
			if got, _ := restarted.review(latest.ID); got.State != "open" || got.AnsweredBy != "" || got.PageSettled != nil || got.RevisingSince != nil {
				t.Errorf("old receipt changed the republished review: %+v", got)
			}
			for deadline := time.Now().Add(5 * time.Second); len(restarted.pageReceiptsSnapshot().Answers) != 0; time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("old receipt was not delivered independently of the new page")
				}
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 {
				t.Errorf("recovered wakes = %+v, want old feedback delivered once", wakes)
			}
			if !answer.IsPageEnd {
				select {
				case text := <-sent:
					if !strings.Contains(text, "Old feedback") {
						t.Errorf("lost old feedback: %q", text)
					}
				default:
					t.Error("old feedback did not reach its original reporter")
				}
			}
		})
	}
}

func TestPageReceiptRecoveryDoesNotGroupAcrossVersions(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	meta, _, connection, page := pageOfAGoblin(t, store)
	answer := pageAnswer{ID: "old-note", Item: store.Snapshot().Reviews[0], Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Prompts: []string{"Old revision"}, Output: "Old revision"}}
	if err := store.keepPageAnswer(answer); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(h, meta.ID, 9, "review the new version", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ingestReviews(); err != nil {
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
	scrawl.send(approvedAsShown)
	recovered := &Service{Store: restarted, Options: Options{CFO: connection, PollPage: scrawl.poll}}

	// Act
	recovered.watchPages(context.Background())
	recovered.pageWork.Wait()

	// Assert
	if len(sent) != 2 || !strings.Contains(sent[0], "Old revision") || strings.Contains(sent[0], "Approved as shown") || !strings.Contains(sent[1], "Approved as shown") || strings.Contains(sent[1], "Old revision") {
		t.Errorf("deliveries = %q, want separate ordered feedback for each version", sent)
	}
	if got := restarted.Snapshot().Reviews[0]; got.State != "answered" || got.AnsweredBy != "overlord" || got.PageSettled == nil {
		t.Errorf("new version's own approval was not applied: %+v", got)
	}
}

func TestPageReceiptRecoveryDoesNotAnswerAReplacementQuestion(t *testing.T) {
	for _, changed := range []string{"version", "reporter"} {
		t.Run(changed, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, page := askOnAPage(t, store)
			original := store.Snapshot().Reviews[0]
			answer := pageAnswer{ID: "old-approval", Item: original, IsAnswer: true, Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "Old approval"}}
			if err := store.keepPageAnswer(answer); err != nil {
				t.Fatal(err)
			}
			if changed == "version" {
				if err := PublishWait(h, meta.ID, 9, "review the new version", pageLink, page, ""); err != nil {
					t.Fatal(err)
				}
			} else {
				store.mu.Lock()
				store.db.Reviews[0].Identity = strings.Repeat("a", 64)
				store.db.Questions[0].Identity = strings.Repeat("a", 64)
				err := store.save()
				store.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			restarted, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recovered := &Service{Store: restarted, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
				cancel()
				return axi.PagePoll{}, context.Canceled
			}}}

			// Act
			recovered.watchPages(ctx)
			recovered.pageWork.Wait()

			// Assert
			if got := restarted.Snapshot().Reviews[0]; got.State != "open" || got.AnsweredBy != "" || got.PageSettled != nil {
				t.Errorf("old approval changed the replacement review: %+v", got)
			}
			if got := restarted.Snapshot().Questions[0]; got.Status != "pending" || got.AnsweredBy != "" || got.AnsweredAt != nil {
				t.Errorf("old approval answered the replacement's question: %+v", got)
			}
			pending, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range pending {
				if record.Seq == restarted.Snapshot().Questions[0].Seq && record.AnsweredBy != "" {
					t.Errorf("old approval retired the replacement question's wake: %+v", record)
				}
			}
		})
	}
}

func TestPageReceiptRecoveryDoesNotUseHistoryToAnswerANewQuestion(t *testing.T) {
	// Arrange: the old card closed after its approval was journaled, so the
	// next publication creates a distinct item while retaining the old row.
	store, h := testStore(t)
	meta, _, page := askOnAPage(t, store)
	original := store.Snapshot().Reviews[0]
	answer := pageAnswer{ID: "historical-approval", Item: original, IsAnswer: true, Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "Old approval"}}
	if err := store.keepPageAnswer(answer); err != nil {
		t.Fatal(err)
	}
	if err := store.closePage(original, Review{State: "answered", AnsweredBy: "overlord", Reason: pageAnswerReceived}); err != nil {
		t.Fatal(err)
	}
	record, err := wake.Append(h.State, "notify", meta.ID, "blocked: Approve the new version? options: Yes | No")
	if err != nil {
		t.Fatal(err)
	}
	if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, nil); err != nil {
		t.Fatal(err)
	}
	if err := PublishWait(h, meta.ID, record.Seq+20, "review the new version", pageLink, page, ""); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ingestQuestions(); err != nil {
		t.Fatal(err)
	}
	latest := restarted.watchedPages()[pageKey(page)]
	if latest.ID == original.ID || latest.State != "open" || latest.Identity != original.Identity || !latest.CreatedAt.After(original.CreatedAt) {
		t.Fatalf("new publication did not retain a distinct old review: %+v", restarted.Snapshot().Reviews)
	}
	if historical, exists := restarted.review(original.ID); !exists || !samePageVersion(historical, original) || historical.State != "answered" {
		t.Fatalf("closed historical version missing: %+v", historical)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	polled := false
	recovered := &Service{Store: restarted, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
		polled = true
		cancel()
		return axi.PagePoll{}, context.Canceled
	}}}

	// Act
	recovered.watchPages(ctx)
	recovered.pageWork.Wait()

	// Assert
	if !polled {
		t.Error("new review was no longer polled")
	}
	if got, _ := restarted.review(latest.ID); got.State != "open" || got.PageSettled != nil || got.AnsweredBy != "" {
		t.Errorf("new review changed: %+v", got)
	}
	isQuestionFound := false
	for _, question := range restarted.Snapshot().Questions {
		if question.Seq == record.Seq {
			isQuestionFound = true
			if question.Status != "pending" || question.AnsweredBy != "" || question.AnsweredAt != nil {
				t.Errorf("historical approval answered new question: %+v", question)
			}
		}
	}
	if !isQuestionFound {
		t.Fatal("new question missing")
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	isWakeFound := false
	for _, candidate := range pending {
		if candidate.Seq == record.Seq {
			isWakeFound = true
			if candidate.AnsweredBy != "" || candidate.Answered != "" {
				t.Errorf("historical approval retired new question's wake: %+v", candidate)
			}
		}
	}
	if !isWakeFound {
		t.Fatal("new question's wake missing")
	}
	if receipts := restarted.pageReceiptsSnapshot().Answers; len(receipts) != 0 {
		t.Errorf("old feedback not delivered: %+v", receipts)
	}
	if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 {
		t.Errorf("old feedback wakes = %+v, want once", wakes)
	}
}

// A page answer recovered after a restart answers its page only: neither the
// question its goblin asked before it nor one asked after it reads answered.
func TestPageReceiptRecoveryAnswersNoQuestion(t *testing.T) {
	for _, hasLaterQuestion := range []bool{false, true} {
		t.Run(map[bool]string{false: "question asked before the answer", true: "later question on same version"}[hasLaterQuestion], func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, firstWake, page := askOnAPage(t, store)
			original := store.Snapshot().Reviews[0]
			answer := pageAnswer{ID: "page-answer", Item: original, IsAnswer: true, Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "Old approval"}}
			if err := store.keepPageAnswer(answer); err != nil {
				t.Fatal(err)
			}
			if err := store.closePage(original, Review{State: "answered", AnsweredBy: "overlord", Reason: pageAnswerReceived}); err != nil {
				t.Fatal(err)
			}
			wantedWake := firstWake
			if hasLaterQuestion {
				record, err := wake.Append(h.State, "notify", meta.ID, "blocked: Another choice? options: Yes | No")
				if err != nil {
					t.Fatal(err)
				}
				if err := SurfaceNotify(h.State, meta.ID, record, record.Detail, nil); err != nil {
					t.Fatal(err)
				}
				wantedWake = record
			}
			restarted, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.ingestQuestions(); err != nil {
				t.Fatal(err)
			}
			recovered := &Service{Store: restarted, Options: Options{PollPage: newScrawlPage(0).poll}}

			// Act
			recovered.watchPages(context.Background())
			recovered.pageWork.Wait()

			// Assert
			for _, question := range restarted.Snapshot().Questions {
				if question.Seq == wantedWake.Seq && (question.Status != "pending" || question.AnsweredBy != "") {
					t.Errorf("the page's answer answered question %+v, want it pending", question)
				}
			}
			pending, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range pending {
				if record.Seq == wantedWake.Seq && record.AnsweredBy != "" {
					t.Errorf("the page's answer retired the question's wake: %+v", record)
				}
			}
			if _, isWatched := restarted.watchedPages()[pageKey(page)]; isWatched {
				t.Error("its own review did not settle")
			}
		})
	}
}

func TestPagePollDoesNotApproveVersionPublishedWhilePolling(t *testing.T) {
	for _, beforePublish := range []int{0, 1} {
		t.Run([]string{"first poll", "settling poll"}[beforePublish], func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, _, page := pageOfAGoblin(t, store)
			original := store.Snapshot().Reviews[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			polls := 0
			s := &Service{Store: store, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
				polls++
				if polls <= beforePublish {
					return axi.PagePoll{Status: "feedback", Output: "Old note"}, nil
				}
				if polls == beforePublish+1 {
					if err := PublishWait(h, meta.ID, 9, "review the new version", pageLink, page, ""); err != nil {
						t.Error(err)
					}
					if err := store.ingestReviews(); err != nil {
						t.Error(err)
					}
					return axi.PagePoll{Status: "feedback", Ended: true, EndedBy: "user", Output: "Old approval"}, nil
				}
				cancel()
				return axi.PagePoll{}, context.Canceled
			}}}

			// Act
			s.watchPages(ctx)
			s.pageWork.Wait()

			// Assert
			if got := store.Snapshot().Reviews[0]; !got.CreatedAt.After(original.CreatedAt) || got.State != "open" || got.AnsweredBy != "" || got.PageSettled != nil {
				t.Errorf("in-flight poll approved the replacement version: %+v", got)
			}
			if polls != beforePublish+2 {
				t.Errorf("polls = %d, want polling to continue on the new version", polls)
			}
			if wakes := reviewWakes(t, h.State, meta.ID); len(wakes) != 1 {
				t.Errorf("old feedback wakes = %+v, want one", wakes)
			}
		})
	}
}

func TestPageReceiptRecoveryStopsIfOldFeedbackCannotBeQueued(t *testing.T) {
	for _, isEnded := range []bool{false, true} {
		t.Run(map[bool]string{false: "collecting", true: "ended"}[isEnded], func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _, _, page := pageOfAGoblin(t, store)
			answer := pageAnswer{ID: "old-note", Item: store.Snapshot().Reviews[0], IsAnswer: isEnded, Status: "collecting", Poll: axi.PagePoll{Status: "feedback", Ended: isEnded, EndedBy: "user", Output: "Old feedback"}}
			if err := store.keepPageAnswer(answer); err != nil {
				t.Fatal(err)
			}
			if err := PublishWait(h, meta.ID, 9, "review the new version", pageLink, page, ""); err != nil {
				t.Fatal(err)
			}
			restarted, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.ingestReviews(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(restarted.pageReceiptsPath(), restarted.pageReceiptsPath()+".before"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(restarted.pageReceiptsPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			previousPause := pagePollPause
			pagePollPause = time.Millisecond
			t.Cleanup(func() { pagePollPause = previousPause })
			polls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			recovered := &Service{Store: restarted, Options: Options{PollPage: func(context.Context, string, string, time.Duration) (axi.PagePoll, error) {
				polls++
				cancel()
				return axi.PagePoll{}, context.Canceled
			}}}

			// Act
			recovered.watchPages(ctx)
			recovered.pageWork.Wait()

			// Assert
			if polls != 0 {
				t.Errorf("polled the new version %d times despite an unqueued old receipt", polls)
			}
			if pending := restarted.pageReceiptsSnapshot().Answers; len(pending) != 1 || pending[0].Status != "collecting" || pending[0].Poll.Output != answer.Poll.Output {
				t.Errorf("unqueued receipt was lost: %+v", pending)
			}
		})
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
			meta, _, connection, _ := pageOfAGoblin(t, store)
			r := store.Snapshot().Reviews[0]
			r.State = "answered"
			if err := store.settlePage(r, r); err != nil {
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
