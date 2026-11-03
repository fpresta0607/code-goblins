package fleettree

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type parsingMessageContext struct {
	context.Context
	opens   int64
	checks  atomic.Int32
	started chan struct{}
}

func (c *parsingMessageContext) Err() error {
	if fsx.Opens() > c.opens && c.checks.Add(1) == 5 {
		close(c.started)
		<-c.Done()
	}
	return c.Context.Err()
}

type waitingMessageContext struct {
	context.Context
	once              sync.Once
	started           chan struct{}
	opens             int64
	shouldWaitForOpen bool
}

func (c *waitingMessageContext) Done() <-chan struct{} {
	if !c.shouldWaitForOpen || fsx.Opens() > c.opens {
		c.once.Do(func() { close(c.started) })
	}
	return c.Context.Done()
}

// deadlineContext is a deadline the test runs out itself once the reader
// waits on it, so no stall before then can run it out early.
type deadlineContext struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func (c *deadlineContext) Done() <-chan struct{} { return c.done }

func (c *deadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// endableContext is a context the returned func ends, by cancelling it or by
// running out its deadline, with the error the ended context reports.
func endableContext(isDeadline bool) (context.Context, func(), error) {
	if isDeadline {
		deadline := &deadlineContext{Context: context.Background(), done: make(chan struct{})}
		return deadline, func() { deadline.once.Do(func() { close(deadline.done) }) }, context.DeadlineExceeded
	}
	ctx, cancel := context.WithCancel(context.Background())
	return ctx, cancel, context.Canceled
}

func contextMessageEntries(harness string, from, to int) []object {
	var entries []object
	for index := from; index < to; index++ {
		id, text := fmt.Sprintf("message-%d", index), fmt.Sprintf("words %d", index)
		switch harness {
		case "claude":
			entries = append(entries, prompted(id, text))
		case "codex":
			entries = append(entries, codexCompleted("UserMessage", object{"id": id, "content": []object{{"type": "text", "text": text}}}))
		case "pi":
			parent := ""
			if index > 0 {
				parent = fmt.Sprintf("message-%d", index-1)
			}
			entries = append(entries, piMessageEntry(id, parent, "user", object{"content": text}))
		}
	}
	return entries
}

func contextMessageFixture(t *testing.T, harness string) (*Reader, string) {
	t.Helper()
	home := t.TempDir()
	var path string
	switch harness {
	case "claude":
		path = conversation(t, home)
	case "codex":
		path = codexRollout(t, home, "context-session")
	case "pi":
		path = piSession(t, home, piHeader())
	}
	appendLines(t, path, contextMessageEntries(harness, 0, 120)...)
	return &Reader{Home: home}, path
}

func checkContextMessageHistory(t *testing.T, reader *Reader, path string, count int) {
	t.Helper()
	var history []Message
	before := -1
	for {
		page, _, err := reader.MessagePage(path, before)
		if err != nil {
			t.Fatal(err)
		}
		history = append(page.Messages, history...)
		if !page.More {
			break
		}
		if len(page.Messages) == 0 || before >= 0 && page.Messages[0].Seq >= before {
			t.Fatal("history stopped advancing toward the first message")
		}
		before = page.Messages[0].Seq
	}
	if len(history) != count {
		t.Fatalf("history has %d messages, want %d", len(history), count)
	}
	for index, message := range history {
		if message.ID != fmt.Sprintf("message-%d", index) || message.Text != fmt.Sprintf("words %d", index) || index > 0 && message.Seq <= history[index-1].Seq {
			t.Fatalf("history message %d = %+v", index, message)
		}
	}
}

func TestMessagePageContextCancelsActiveParsingWithoutPoisoningHistory(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, isWarm := range []bool{false, true} {
			for _, isDeadline := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/warm=%t/deadline=%t", harness, isWarm, isDeadline), func(t *testing.T) {
					reader, path := contextMessageFixture(t, harness)
					if harness == "claude" && !isWarm {
						padding := make([]object, 160)
						for index := range padding {
							padding[index] = object{"type": "api-request-blob", "content": strings.Repeat("x", 64<<10)}
						}
						appendLines(t, path, padding...)
					}
					count := 120
					var held MessagePage
					var cursor MessageCursor
					if isWarm {
						var err error
						held, cursor, err = reader.MessagePage(path, -1)
						if err != nil {
							t.Fatal(err)
						}
						appendLines(t, path, contextMessageEntries(harness, 120, 140)...)
						count = 140
					}
					ctx, end, want := endableContext(isDeadline)
					defer end()
					reading := &parsingMessageContext{Context: ctx, opens: fsx.Opens(), started: make(chan struct{})}
					finished := make(chan error, 1)
					go func() {
						_, _, err := reader.MessagePageContext(reading, path, -1)
						finished <- err
					}()

					select {
					case <-reading.started:
					case <-time.After(time.Minute):
						t.Fatal("reader did not reach an active parsing cancellation point")
					}
					end()
					select {
					case err := <-finished:
						if !errors.Is(err, want) {
							t.Fatalf("active parsing returned %v, want %v", err, want)
						}
					case <-time.After(time.Minute):
						t.Fatal("active parsing ignored cancellation")
					}

					checkContextMessageHistory(t, reader, path, count)
					if isWarm {
						changed, _, isAnew, err := reader.MessagesSince(path, cursor)
						if err != nil || isAnew || len(changed) != 20 {
							t.Fatalf("sibling cursor after cancellation = %d changes, anew %t, %v", len(changed), isAnew, err)
						}
						if held.Messages[0].Text != "words 70" || held.Messages[len(held.Messages)-1].Text != "words 119" {
							t.Fatal("cancellation modified a sibling's page")
						}
					}
				})
			}
		}
	}
}

func TestMessagePageContextCancelsWholeHistoryWaitWithoutPoisoningHistory(t *testing.T) {
	for _, isReacquiring := range []bool{false, true} {
		for _, isDeadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("reacquiring=%t/deadline=%t", isReacquiring, isDeadline), func(t *testing.T) {
				reader, path := contextMessageFixture(t, "claude")
				held, cursor, err := reader.MessagePage(path, -1)
				if err != nil {
					t.Fatal(err)
				}
				log := reader.messageLogs[path]
				log.from, log.whole = 1, make(chan struct{})
				ctx, end, want := endableContext(isDeadline)
				defer end()
				waiting := &waitingMessageContext{Context: ctx, started: make(chan struct{}), opens: fsx.Opens(), shouldWaitForOpen: true}
				finished := make(chan error, 1)
				go func() {
					_, _, err := reader.MessagePageContext(waiting, path, -1)
					finished <- err
				}()

				select {
				case <-waiting.started:
				case <-time.After(time.Minute):
					t.Fatal("reader did not wait for complete shared history")
				}
				if isReacquiring {
					log.mu.Lock()
					log.from = 0
					close(log.whole)
				}
				isLocked := isReacquiring
				defer func() {
					if isLocked {
						log.mu.Unlock()
					}
				}()
				end()
				select {
				case err := <-finished:
					if !errors.Is(err, want) {
						t.Fatalf("whole-history lookup returned %v, want %v", err, want)
					}
				case <-time.After(time.Minute):
					t.Fatal("whole-history wait ignored cancellation")
				}
				if isReacquiring {
					log.mu.Unlock()
					isLocked = false
				} else {
					log.mu.Lock()
					log.from = 0
					close(log.whole)
					log.mu.Unlock()
				}

				again, _, err := reader.MessagePage(path, -1)
				changed, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
				if err != nil || sinceErr != nil || isAnew || len(changed) != 0 || !reflect.DeepEqual(held, again) {
					t.Fatalf("cancelled history wait changed a sibling's cache: anew %t, %v, %v", isAnew, err, sinceErr)
				}
			})
		}
	}
}

func TestMessagePageContextCancelsContendedLocksWithoutPoisoningHistory(t *testing.T) {
	for _, harness := range []string{"claude", "codex", "pi"} {
		for _, isMapLock := range []bool{false, true} {
			for _, isDeadline := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/map=%t/deadline=%t", harness, isMapLock, isDeadline), func(t *testing.T) {
					reader, path := contextMessageFixture(t, harness)
					held, cursor, err := reader.MessagePage(path, -1)
					if err != nil {
						t.Fatal(err)
					}
					mutex := &reader.messagesMu
					if !isMapLock {
						switch harness {
						case "claude":
							mutex = &reader.messageLogs[path].mu
						case "codex":
							mutex = &reader.codexLogs[path].mu
						case "pi":
							mutex = &reader.piLogs[path].mu
						}
					}
					mutex.Lock()
					isLocked := true
					defer func() {
						if isLocked {
							mutex.Unlock()
						}
					}()
					ctx, cancel := context.WithCancel(context.Background())
					if isDeadline {
						cancel()
						ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond) // stall-accepted: the test means this deadline to run out, and the reader's first call to Done marks its wait begun, so a deadline run out early still ends the lookup with the error the test wants.
					}
					defer cancel()
					waiting := &waitingMessageContext{Context: ctx, started: make(chan struct{})}
					finished := make(chan error, 1)
					go func() {
						_, _, err := reader.MessagePageContext(waiting, path, -1)
						finished <- err
					}()

					select {
					case <-waiting.started:
					case <-time.After(time.Minute):
						t.Fatal("reader did not enter its cancellable lock wait")
					}
					if !isDeadline {
						cancel()
					}
					want := context.Canceled
					if isDeadline {
						want = context.DeadlineExceeded
					}
					select {
					case err := <-finished:
						if !errors.Is(err, want) {
							t.Fatalf("contended lookup returned %v, want %v", err, want)
						}
					case <-time.After(time.Minute):
						t.Fatal("lookup waited for the held mutex after cancellation")
					}
					mutex.Unlock()
					isLocked = false

					again, _, err := reader.MessagePage(path, -1)
					changed, _, isAnew, sinceErr := reader.MessagesSince(path, cursor)
					if err != nil || sinceErr != nil || isAnew || len(changed) != 0 || !reflect.DeepEqual(held, again) {
						t.Fatalf("cancelled lock wait changed a sibling's cache: anew %t, %v, %v", isAnew, err, sinceErr)
					}
				})
			}
		}
	}
}
