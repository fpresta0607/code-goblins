package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
)

func TestNativeCFOLeavesAnUnreadyComposerUntouched(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		harness string
		screen  []string
		readErr error
	}{
		{"codex draft", "codex", []string{"› my unfinished thought", "80% context left"}, nil},
		{"codex trust dialog", "codex", []string{"Do you trust the contents of this directory?", "› 1. Yes, continue", "› Ask Codex to do anything"}, nil},
		{"codex unreadable", "codex", nil, errors.New("screen unavailable")},
		{"claude draft", "claude", []string{"❯ my unfinished thought", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			terminal, _ := shimTerminal(t, stateDir, scenario.harness)
			terminal.typeLine(t, "register by program")
			if lines := terminal.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
				t.Fatalf("registration = %q", lines)
			}
			connection := &CFOConnection{State: stateDir, ReadScreen: func(host.Record) ([]string, error) {
				return scenario.screen, scenario.readErr
			}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			result, err := connection.Send(ctx, registrationIdentity(t, stateDir), "Reply received")

			// Assert
			if !errors.Is(err, ErrDeferred) || result.Awaiting != nil {
				t.Errorf("Send = %+v, %v; want queued without typing or claiming Sent", result, err)
			}
			if lines := terminal.lines(t); len(lines) != 1 {
				t.Errorf("terminal input = %q; want only registration, with no answer or dialog choice", lines)
			}
		})
	}
}

func TestBoardCommandsReachTheCFOWithoutAProsePrefix(t *testing.T) {
	for _, command := range []string{"/status", "!git status --short"} {
		t.Run(command, func(t *testing.T) {
			store, home := testStore(t)
			terminal := nativeCFOComposer(t, home.State, "ready")
			service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			_, err := service.deliverMessage(ctx, Action{Kind: "message", Text: command})

			if err != nil {
				t.Fatal(err)
			}
			lines := terminal.lines(t)
			if len(lines) != 2 || lines[1] != "accepted "+command {
				t.Fatalf("terminal input = %q; want the exact command once", lines)
			}
		})
	}
}

// longMultiLineMessage is a message like the one #1740 lost the head of: a
// pasted reply of 1,583 bytes over 24 lines, longer than one read of the
// Claude Code that took it.
func longMultiLineMessage() string {
	lines := make([]string, 24)
	for i := range lines {
		lines[i] = fmt.Sprintf("Line %02d of a reply the Overlord pasted, which doesn’t end here.", i+1)
	}
	return strings.Join(lines, "\n")
}

func TestHisLongMultiLineMessageStartsAnIdleClaudeCFOsTurnWholeAndOnce(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	terminal := nativeCFOComposer(t, home.State, "claude-ready")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
	message := longMultiLineMessage()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := service.deliverMessage(ctx, Action{Kind: "message", Text: message})

	// Assert
	if err != nil || result.Awaiting != nil {
		t.Fatalf("deliverMessage = %+v, %v; want it taken at once by the idle CFO", result, err)
	}
	lines := terminal.lines(t)
	if len(lines) != 2 || lines[1] != "accepted "+oneLine(message) {
		t.Errorf("terminal input = %q; want one submitted turn holding the whole message", lines)
	}
}

func TestHisLongMultiLineMessageToAClaudeCFOInATurnIsTakenWholeOnce(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, home := testStore(t)
	terminal := nativeCFOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
	message := longMultiLineMessage()
	if _, err := store.Queue(Action{ID: "long-message", Kind: "message", Text: message}); err != nil {
		t.Fatal(err)
	}

	// Act
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	sent := store.Snapshot().Actions[0]
	if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lines := terminal.waitForLines(t, 3)
	takenInRecord(t, records, "claude", "session-1", "", strings.TrimPrefix(lines[1], "queued "))
	if err := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
		t.Fatal(err)
	}
	delivered := store.Snapshot().Actions[0]

	// Assert
	if sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentToCFO || !sent.CanSendNow {
		t.Fatalf("send during the turn = %+v; want it sent, awaiting the CFO's next tool call, and sendable now by Claude Code's own key", sent)
	}
	if want := []string{"queued " + oneLine(message), "accepted " + oneLine(message)}; len(lines) != 3 || lines[1] != want[0] || lines[2] != want[1] {
		t.Errorf("terminal input = %q; want the whole message queued once and taken once", lines)
	}
	if delivered.Status != "succeeded" || delivered.Awaiting != nil || delivered.CanSendNow {
		t.Errorf("once the CFO's record shows it taken = %+v; want it delivered with its queued mark and Send now gone", delivered)
	}
}

// sendNow presses Send now on message id of task in the board, which asks the
// action API, with the board's token, to send it now.
func sendNow(service *Service, task, id string) *httptest.ResponseRecorder {
	return boardRequest(NewHTTP(service, "board.local", nil), "POST", "/api/actions", fmt.Sprintf(`{"id":"now-%s","kind":"message_now","task_id":%q,"generation":"","target":%q}`, id, task, id))
}

// The Overlord, 2026-10-30: "when sent now it actually sends now same way
// termianl expereince down with ctrl x crtl s". His message typed into a
// Claude Code CFO in a turn waits in Claude Code's own queue, and Send now
// presses Claude Code's own key for it, Ctrl+X Ctrl+S, once however often he
// presses, and offers it no more.
func TestSendNowOnHisMessageInABusyClaudeCFOsQueuePressesItsKeyOnce(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCFOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}

	// Act
	first := sendNow(service, "", "m1")
	second := sendNow(service, "", "m1")

	// Assert
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("Send now answered %d %q, then %d %q; want both accepted", first.Code, first.Body, second.Code, second.Body)
	}
	if lines := terminal.waitForLines(t, 3); len(lines) != 3 || !strings.HasPrefix(lines[1], "queued ") || !strings.HasSuffix(lines[1], "stop and rebase") || lines[2] != "sent now" {
		t.Fatalf("terminal input = %q; want the message queued and then sent now", lines)
	}
	time.Sleep(500 * time.Millisecond)
	if lines := terminal.waitForLines(t, 3); len(lines) != 3 {
		t.Errorf("terminal input = %q; want Ctrl+X Ctrl+S pressed once", lines)
	}
	if sent := store.Snapshot().Actions[0]; !sent.IsSentNow || sent.CanSendNow || sent.Status != "running" || sent.Message != "Typed while it was working and sent now." {
		t.Errorf("after Send now = %+v; want it on its way, sent now and saying so, and Send now gone", sent)
	}
}

// A line waiting in Claude Code's queue leaves its prompt line empty but for
// the hint it draws there, which is nothing he left unsent, so his next message
// is typed into the turn behind it and not held back as if it were.
func TestHisNextMessageIsTypedBehindOneInABusyClaudeCFOsQueue(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCFOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
	for _, id := range []string{"m1", "m2"} {
		if _, err := store.Queue(Action{ID: id, Kind: "message", Text: "from " + id}); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	err := errors.Join(store.ProcessOne(context.Background(), service.execute), store.ProcessOne(context.Background(), service.execute))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	lines := terminal.waitForLines(t, 3)
	if len(lines) != 3 || !strings.HasSuffix(lines[1], "from m1") || !strings.HasSuffix(lines[2], "from m2") {
		t.Fatalf("terminal input = %q; want both messages queued in turn", lines)
	}
	for _, sent := range store.Snapshot().Actions {
		if sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentToCFO {
			t.Errorf("%s = %+v; want it typed into the turn, awaiting the CFO", sent.ID, sent)
		}
	}
}

// Send now on a typed message is refused in plain words, with nothing pressed,
// nothing marked sent and its words as they were, where words he typed into
// the CFO's own prompt line would go with it, where it is a command, which
// runs when its turn ends, or where the CFO's harness has no key that takes it
// sooner, as Codex has none: it takes a line typed during a turn at its next
// tool call.
func TestSendNowOnATypedMessageIsRefusedWithItsReason(t *testing.T) {
	for _, test := range []struct {
		name, mode, text, draft, want string
		wantCanSendNow                bool
	}{
		{name: "his words in its prompt line", mode: "claude-busy", text: "stop and rebase", draft: "and the", want: "Its prompt line holds words that were never sent.", wantCanSendNow: true},
		{name: "a command", mode: "claude-busy", text: "/compact", want: "A command runs when its turn ends."},
		{name: "a harness with no such key", mode: "busy", text: "stop and rebase", want: "has no key that takes it sooner"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCFOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: test.text}); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			if test.draft != "" {
				record, err := host.ReadRecord(home.State, "cfo")
				if err != nil {
					t.Fatal(err)
				}
				client, err := host.Dial(record)
				if err != nil {
					t.Fatal(err)
				}
				err = client.Input([]byte(test.draft))
				client.Close()
				if err != nil {
					t.Fatal(err)
				}
				for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
					screen, err := host.ReadScreen(record)
					if err == nil && strings.Contains(strings.Join(screen, "\n"), "❯ "+test.draft) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("the CFO's prompt line never showed his words: %v", err)
					}
				}
			}

			// Act
			response := sendNow(service, "", "m1")

			// Assert
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("Send now answered %d %q; want it refused with %q", response.Code, response.Body, test.want)
			}
			time.Sleep(500 * time.Millisecond)
			if lines := terminal.waitForLines(t, 2); slices.Contains(lines, "sent now") {
				t.Errorf("terminal input = %q; want no key pressed", lines)
			}
			if sent := store.Snapshot().Actions[0]; sent.IsSentNow || sent.CanSendNow != test.wantCanSendNow || sent.Message != sentToCFO {
				t.Errorf("after a refused Send now = %+v; want it not sent now, its words kept, and Send now offered %v", sent, test.wantCanSendNow)
			}
		})
	}
}

// typedIntoCFOsTurn saves his message id on the store as one typed into a
// Claude Code CFO's terminal that waits there, in status, with Send now
// offered.
func typedIntoCFOsTurn(t *testing.T, store *Store, id, status string) {
	t.Helper()
	at := time.Now().UTC()
	store.mu.Lock()
	defer store.mu.Unlock()
	store.db.Actions = append(store.db.Actions, Action{ID: id, Kind: "message", Text: "stop and rebase", Status: status, CreatedAt: at, UpdatedAt: at, Awaiting: &Awaiting{Host: "cfo", Harness: "claude", Since: at, QuietSince: at}, CanSendNow: true})
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
}

// standInForSendNowKey stands in for the CFO's terminal as Send now presses
// its key, counting each press and answering it as press says.
func standInForSendNowKey(t *testing.T, press func(count int32) error) *atomic.Int32 {
	t.Helper()
	previous := pressSendNow
	t.Cleanup(func() { pressSendNow = previous })
	var presses atomic.Int32
	pressSendNow = func(context.Context, *Service, Awaiting) error {
		return press(presses.Add(1))
	}
	return &presses
}

// Two presses of Send now on his message in a Claude Code CFO's queue, from
// two screens, press its key once even when the second comes while the
// first still waits on the CFO's terminal, as a delivery holding the CFO's
// typing for seconds has it wait.
func TestSendNowPressedTwiceAtOnceOnATypedMessagePressesItsKeyOnce(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	typedIntoCFOsTurn(t, store, "m1", "running")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
	pressing, release := make(chan struct{}), make(chan struct{})
	presses := standInForSendNowKey(t, func(count int32) error {
		if count == 1 {
			close(pressing)
			<-release
		}
		return nil
	})
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- sendNow(service, "", "m1") }()
	select {
	case <-pressing:
	case <-time.After(30 * time.Second):
		t.Fatal("the first Send now never reached the CFO's terminal")
	}

	// Act
	second := sendNow(service, "", "m1")
	close(release)
	firstResponse := <-first

	// Assert
	if firstResponse.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("Send now answered %d %q, then %d %q; want both accepted", firstResponse.Code, firstResponse.Body, second.Code, second.Body)
	}
	if count := presses.Load(); count != 1 {
		t.Errorf("Ctrl+X Ctrl+S pressed %d times, want once", count)
	}
	if sent := actionOf(store, "m1"); !sent.IsSentNow || sent.CanSendNow {
		t.Errorf("after Send now = %+v; want it sent now, and Send now gone", sent)
	}
}

// A Send now whose key could not go says why and gives the message back: it
// is not sent now, Send now is offered again, and the next press tries the
// key again.
func TestSendNowWhoseKeyCouldNotGoOffersItAgain(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	typedIntoCFOsTurn(t, store, "m1", "running")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
	presses := standInForSendNowKey(t, func(count int32) error {
		if count == 1 {
			return errors.New("Its terminal is not ready to take it yet.")
		}
		return nil
	})

	// Act
	refused := sendNow(service, "", "m1")
	given := actionOf(store, "m1")
	accepted := sendNow(service, "", "m1")

	// Assert
	if refused.Code != http.StatusConflict || !strings.Contains(refused.Body.String(), "not ready to take it yet") {
		t.Fatalf("the first Send now answered %d %q; want it refused with its reason", refused.Code, refused.Body)
	}
	if given.IsSentNow || !given.CanSendNow {
		t.Errorf("after a Send now whose key could not go = %+v; want it not sent now, and Send now offered", given)
	}
	if count := presses.Load(); accepted.Code != http.StatusAccepted || count != 2 {
		t.Errorf("the second Send now answered %d %q after %d presses; want it accepted on a second press", accepted.Code, accepted.Body, count)
	}
	if sent := actionOf(store, "m1"); !sent.IsSentNow || sent.CanSendNow {
		t.Errorf("after the second Send now = %+v; want it sent now, and Send now gone", sent)
	}
}

// Send now on his typed message the CFO never picked up, which is no longer on
// its way, presses nothing and says why, even from a board that still offers
// it.
func TestSendNowOnATypedMessageNeverPickedUpPressesNothing(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	typedIntoCFOsTurn(t, store, "m1", "uncertain")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
	presses := standInForSendNowKey(t, func(int32) error { return nil })

	// Act
	response := sendNow(service, "", "m1")

	// Assert
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "that message is no longer waiting") {
		t.Fatalf("Send now answered %d %q; want it refused as no longer waiting", response.Code, response.Body)
	}
	if count := presses.Load(); count != 0 {
		t.Errorf("Ctrl+X Ctrl+S pressed %d times, want never", count)
	}
	if sent := actionOf(store, "m1"); sent.IsSentNow {
		t.Errorf("after a refused Send now = %+v; want it not sent now", sent)
	}
}

// A message the CFO takes while its Send now key is being pressed keeps the
// words of that take: the key's own words are for a message still waiting.
func TestSendNowOnAMessageTakenDuringThePressKeepsTheWordsOfTheTake(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	typedIntoCFOsTurn(t, store, "m1", "running")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}
	standInForSendNowKey(t, func(int32) error {
		return store.settleDeliveries(time.Now().UTC(), func(Awaiting) terminalLook { return terminalLook{Took: true} })
	})

	// Act
	response := sendNow(service, "", "m1")

	// Assert
	if response.Code != http.StatusAccepted {
		t.Fatalf("Send now answered %d %q; want it accepted", response.Code, response.Body)
	}
	if sent := actionOf(store, "m1"); sent.Status != "succeeded" || sent.Message != "Taken by the CFO, as its own record of the conversation shows." {
		t.Errorf("after the take = %+v; want it delivered, saying so", sent)
	}
}

// A Claude Code CFO whose terminal shows no prompt line of Claude Code's, as
// when a screen of its own covers it in its turn (here the stand-in draws
// another harness's screen), takes no Send now key, and the refusal says its
// terminal is not ready rather than that its prompt line holds words.
func TestSendNowOnATypedMessageWaitsForTheCFOsPromptLine(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	terminal := nativeCFOComposer(t, home.State, "busy")
	typedIntoCFOsTurn(t, store, "m1", "running")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CFO: &CFOConnection{State: home.State}}}

	// Act
	response := sendNow(service, "", "m1")

	// Assert
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Its terminal is not ready to take it yet.") {
		t.Fatalf("Send now answered %d %q; want it refused as not ready", response.Code, response.Body)
	}
	time.Sleep(500 * time.Millisecond)
	if lines := terminal.waitForLines(t, 1); slices.Contains(lines, "sent now") {
		t.Errorf("terminal input = %q; want no key pressed", lines)
	}
	if sent := actionOf(store, "m1"); sent.IsSentNow || !sent.CanSendNow {
		t.Errorf("after a refused Send now = %+v; want it not sent now, and Send now offered", sent)
	}
}

func TestNativeCFOAnswerPathsPreserveRepliesWhileItIsTyping(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"cfo_answer", "review_answer", "question_clear", "run_result"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				store, home := testStore(t)
				mode := "ready"
				if harness == "claude" {
					mode = "claude-ready"
				}
				terminal := nativeCFOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				connection := &CFOConnection{State: home.State, ReadScreen: func(host.Record) ([]string, error) {
					if harness == "claude" {
						rule := strings.Repeat("─", 80)
						return []string{rule, "❯ unfinished thought", rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
					}
					return []string{"› unfinished thought", "100% context left"}, nil
				}}
				service := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
				switch kind {
				case "cfo_answer", "question_clear":
					question := Question{ID: "held-question", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
					action := Action{ID: "held-action", Kind: kind, Generation: identity, QuestionID: question.ID}
					if kind == "cfo_answer" {
						action.Text, action.AnswerKind = "One", "option"
					}
					if _, err := store.Queue(action); err != nil {
						t.Fatal(err)
					}
				case "review_answer":
					review := Review{ID: "held-review", Identity: identity, Title: "Check the proof", State: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
					if err := store.acceptReview(review); err != nil {
						t.Fatal(err)
					}
					if _, err := store.Queue(Action{ID: "held-action", Kind: kind, Generation: identity, ReviewID: review.ID, Text: "One"}); err != nil {
						t.Fatal(err)
					}
				case "run_result":
					finishedRun(t, service, identity, "held-run")
				}

				// Act
				if kind == "run_result" {
					if err := service.finishRuns(context.Background()); err != nil {
						t.Fatal(err)
					}
				} else if err := store.ProcessOne(context.Background(), service.execute); !errors.Is(err, ErrDeferred) {
					t.Fatalf("held send = %v; want an untyped queued reply", err)
				}
				reopened, err := Open(home)
				if err != nil {
					t.Fatal(err)
				}
				service.Store = reopened

				// Assert
				if lines := terminal.lines(t); len(lines) != 1 {
					t.Errorf("input while typing = %q; want only registration", lines)
				}
				if kind == "review_answer" && reopened.Snapshot().Reviews[0].Delivered {
					t.Fatal("untyped review reply was marked delivered")
				}
				if kind == "run_result" && !strings.Contains(reopened.Snapshot().Runs[0].Untold, "Run item held-run") {
					t.Fatal("the untyped run result was lost", reopened.Snapshot().Runs[0])
				}
				if kind == "run_result" {
					if err := service.retellRuns(context.Background()); err != nil {
						t.Fatalf("retry while typing = %v; want normal waiting without losing the result", err)
					}
					if reopened.Snapshot().Runs[0].Untold == "" {
						t.Fatal("retry while typing discarded the run result")
					}
				}
				connection.ReadScreen = nil
				if kind == "run_result" {
					if err := service.retellRuns(context.Background()); err != nil {
						t.Fatal(err)
					}
					if reopened.Snapshot().Runs[0].Untold != "" {
						t.Error("accepted run result still waits to be told")
					}
				} else if err := reopened.ProcessOne(context.Background(), service.execute); err != nil {
					t.Fatal(err)
				}
				if lines := terminal.lines(t); len(lines) != 2 || !strings.Contains(lines[1], "One") && !strings.Contains(lines[1], "held-run") && !strings.Contains(lines[1], "dismissed your question held-question") {
					t.Errorf("resumed delivery input = %q; want the preserved reply accepted once", lines)
				}
				if kind == "cfo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("accepted question answer lacks its history author")
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("accepted review reply remains undelivered")
				}
			})
		}
	}
}

func TestNativeCFOBusyAnswersBecomeDeliveredOnlyWhenTheQueuedPromptIsTaken(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"cfo_answer", "review_answer"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				records := useConversations(t)
				store, home := testStore(t)
				mode := "busy"
				if harness == "claude" {
					mode = "claude-busy"
				}
				terminal := nativeCFOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				service := &Service{Store: store, Options: Options{CFO: &CFOConnection{State: home.State}}}
				action := Action{ID: "busy-answer", Kind: kind, Generation: identity, Text: "Reply received"}
				if kind == "cfo_answer" {
					question := Question{ID: "busy-question", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
					action.QuestionID = question.ID
				} else {
					review := Review{ID: "busy-review", Identity: identity, Title: "Check the proof", State: "open", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
					if err := store.acceptReview(review); err != nil {
						t.Fatal(err)
					}
					action.ReviewID = review.ID
				}
				if _, err := store.Queue(action); err != nil {
					t.Fatal(err)
				}

				// Act
				if err := store.ProcessOne(context.Background(), service.execute); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(home)
				if err != nil {
					t.Fatal(err)
				}
				sent := reopened.Snapshot().Actions[0]

				// Assert
				if sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentToCFO {
					t.Fatalf("busy send = %+v; want submitted and awaiting actual acceptance", sent)
				}
				if kind == "review_answer" && reopened.Snapshot().Reviews[0].Delivered {
					t.Fatal("busy review was marked delivered before it was taken")
				}
				if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				terminal.waitForLines(t, 3)
				if _, err := reopened.Ingest(); err != nil {
					t.Fatal(err)
				}
				if err := reopened.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
					t.Fatal(err)
				}
				if reported := reopened.Snapshot().Actions[0]; reported.Status != "running" || reported.Awaiting == nil {
					t.Errorf("after the CFO's prompt hook alone = %+v; want it still on its way, since a hook runs as the harness queues the text", reported)
				}
				typed := strings.TrimPrefix(terminal.lines(t)[1], "queued ")
				takenInRecord(t, records, harness, "session-1", "", typed)
				if err := reopened.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
					t.Fatal(err)
				}
				delivered := reopened.Snapshot().Actions[0]
				if delivered.Status != "succeeded" || delivered.Awaiting != nil || !strings.Contains(delivered.Message, "record of the conversation") {
					t.Errorf("once the CFO's record shows it taken = %+v; want it delivered", delivered)
				}
				if input := strings.Join(terminal.lines(t), "\n"); strings.Count(input, "queued Overlord:") != 1 || strings.Count(input, "accepted Overlord:") != 1 {
					t.Errorf("busy input = %q; want one submission and one later acceptance", input)
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("the accepted review still reads undelivered")
				}
				if kind == "cfo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("the accepted question lacks its history author")
				}
			})
		}
	}
}

func TestAnUntypedCFOAnswerStaysQueuedAcrossReopen(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	question := Question{ID: "held-answer", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	answer := Action{ID: "held-action", Kind: "cfo_answer", Generation: identity, QuestionID: question.ID, Text: "One"}
	if _, err := store.Queue(answer); err != nil {
		t.Fatal(err)
	}

	// Act
	err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, errors.Join(ErrDeferred, errors.New("The CFO is typing; your answer is queued and nothing was sent."))
	})
	reopened, openErr := Open(home)

	// Assert
	if !errors.Is(err, ErrDeferred) || openErr != nil {
		t.Fatalf("deferred action/reopen = %v, %v", err, openErr)
	}
	queued, asked := outcome(reopened)
	if queued.Status != "queued" || queued.Awaiting != nil || asked.Status != "queued" || queued.Text != "One" {
		t.Fatalf("reopened answer = %+v, question = %+v; want the unsent answer preserved as queued", queued, asked)
	}
	if err := reopened.ProcessOne(context.Background(), func(_ context.Context, action Action) (Evaluation, error) {
		if action.ID != answer.ID || action.Text != answer.Text {
			t.Errorf("resumed answer = %+v; want the same answer", action)
		}
		return Evaluation{Reason: "Taken by the CFO."}, nil
	}); err != nil {
		t.Fatal(err)
	}
	delivered, history := outcome(reopened)
	if delivered.Status != "succeeded" || history.AnsweredBy != "overlord" || history.AnsweredOption != "One" {
		t.Errorf("delivery/history = %+v, %+v; want the preserved answer delivered with its author and choice", delivered, history)
	}
}

func TestADeferredDeliveryDoesNotHoldOtherRunnableActions(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	question := Question{ID: "held-answer", Identity: identity, Text: "Choose", CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{
		{ID: "held-action", Kind: "cfo_answer", Generation: identity, QuestionID: question.ID, Text: "One"},
		{ID: "other-action", Kind: "evaluate", TaskID: "task-1"},
	} {
		if _, err := store.Queue(action); err != nil {
			t.Fatal(err)
		}
	}
	var attempted []string
	execute := func(_ context.Context, action Action) (Evaluation, error) {
		attempted = append(attempted, action.ID)
		if action.ID == "held-action" {
			return Evaluation{}, ErrDeferred
		}
		return Evaluation{Reason: "Checked."}, nil
	}

	// Act
	first := store.ProcessOne(context.Background(), execute)
	second := store.ProcessOne(context.Background(), execute)

	// Assert
	if !errors.Is(first, ErrDeferred) || second != nil || strings.Join(attempted, ",") != "held-action,other-action" {
		t.Errorf("attempts = %q, outcomes = %v, %v; want one deferred send and the independent action", attempted, first, second)
	}
	if store.HasRunnable() {
		t.Error("the deferred send can run again immediately; want a bounded retry gap")
	}
}

// An answer reaches a CFO at a live Daybreak offer, which one Escape
// dismisses before the answer is typed, and one whose offer left its rows on
// screen after that Escape: they are history that owns no key, so the answer
// is typed and submitted under them, never queued behind them (#1556).
func TestNativeCFOProvesAcceptanceAcrossTheDialogAndPasteBoundary(t *testing.T) {
	for _, mode := range []string{"daybreak", "paste", "late-modal", "daybreak-stuck"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			terminal := nativeCFOComposer(t, stateDir, mode)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			result, err := (&CFOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")
			input := strings.Join(terminal.lines(t), "\n")

			// Assert
			if strings.Contains(input, "dialog-enter") || strings.Contains(input, "typed-under-dialog") {
				t.Errorf("unsafe input: %s", input)
			}
			switch mode {
			case "daybreak", "daybreak-stuck", "paste":
				if err != nil || result.Awaiting != nil || strings.Count(input, "accepted Overlord: Reply received") != 1 {
					t.Errorf("Send = %+v, %v, input %q; want one accepted answer with no unproved Sent state", result, err, input)
				}
			case "late-modal":
				if err == nil || errors.Is(err, ErrDeferred) || strings.Contains(input, "accepted ") {
					t.Errorf("Send = %+v, %v, input %q; want the typed answer held unsubmitted, without replay", result, err, input)
				}
			}
			if strings.HasPrefix(mode, "daybreak") && strings.Count(input, "escape") != 1 {
				t.Errorf("input %q; want exactly one Escape", input)
			}
		})
	}
}

func TestNativeCFOCannotCallAnAnswerSentWhileItRemainsInTheIdleComposer(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	terminal := nativeCFOComposer(t, stateDir, "ignored-enter")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := (&CFOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")

	// Assert
	if err == nil || errors.Is(err, ErrDeferred) || result.Awaiting != nil {
		t.Errorf("idle unsent input = %+v, %v; want unconfirmed typed input, without Sent or automatic replay", result, err)
	}
	if input := strings.Join(terminal.lines(t), "\n"); strings.Contains(input, "accepted ") || strings.Contains(input, "queued ") {
		t.Errorf("idle input = %q; want no accepted or queued prompt", input)
	}
}

