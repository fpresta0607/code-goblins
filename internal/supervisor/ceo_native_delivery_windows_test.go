package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
)

func TestNativeCEOLeavesAnUnreadyComposerUntouched(t *testing.T) {
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
			connection := &CEOConnection{State: stateDir, ReadScreen: func(host.Record) ([]string, error) {
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

func TestBoardCommandsReachTheCEOWithoutAProsePrefix(t *testing.T) {
	for _, test := range []struct{ name, command string }{
		{name: "a slash command", command: "/status"},
		{name: "a shell command", command: "!git status --short"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "ready")
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			_, err := service.deliverMessage(ctx, Action{Kind: "message", Text: test.command})

			if err != nil {
				t.Fatal(err)
			}
			// A ! command goes as the line that runs it in Windows PowerShell (#2051).
			command, isShell, err := shellMessageLine(home.State, "", "", test.command)
			if err != nil {
				t.Fatal(err)
			}
			if !isShell {
				command = test.command
			}
			lines := terminal.lines(t)
			if len(lines) != 2 || lines[1] != "accepted "+command {
				t.Fatalf("terminal input = %q; want the exact command once", lines)
			}
		})
	}
}

// Codex 0.160 lists its skills while its line ends in a $ word and takes an
// Enter then as a pick from that list, so his message that ends in a mention
// reaches a Codex CEO with a space after it, and the Enter submits it.
func TestHisMessageEndingInAMentionIsSubmittedToACodexCEO(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "ready")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	_, err := service.deliverMessage(ctx, Action{Kind: "message", Text: "use $alpha-notes"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if lines := terminal.lines(t); len(lines) != 2 || lines[1] != "accepted use $alpha-notes " {
		t.Fatalf("terminal input = %q; want the message submitted once with its trailing space", lines)
	}
}

// On 2026-11-02 the Overlord sent the CEO a lone "?". Its Claude Code took
// the key as its shortcut help, the board read the "?" in the footer's "? for
// shortcuts" as typed and pressed Enter on an empty prompt line, and the
// message sat Queued for fourteen minutes, never taken. A "?" reaches the CEO
// as words, idle or in a turn, and a turn's record then settles it.
func TestHisLoneQuestionMarkReachesAClaudeCEOAsWords(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		// Arrange
		store, home := testStore(t)
		terminal := nativeCEOComposer(t, home.State, "claude-manual")
		service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		// Act
		result, err := service.deliverMessage(ctx, Action{Kind: "message", Text: "?"})

		// Assert
		if err != nil || result.Awaiting != nil {
			t.Errorf("deliverMessage = %+v, %v; want it taken at once by the idle CEO", result, err)
		}
		if lines := terminal.lines(t); len(lines) != 2 || lines[1] != "accepted ?" {
			t.Errorf("terminal input = %q; want the ? submitted as words once", lines)
		}
	})
	t.Run("in a turn", func(t *testing.T) {
		// Arrange
		records := useConversations(t)
		store, home := testStore(t)
		terminal := nativeCEOComposer(t, home.State, "claude-manual-busy")
		service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
		if _, err := store.Queue(Action{ID: "question-mark", Kind: "message", Text: "?"}); err != nil {
			t.Fatal(err)
		}
		pastUndoWindow(t, store)

		// Act
		if err := store.ProcessOne(context.Background(), service.execute); err != nil {
			t.Fatal(err)
		}
		lines := terminal.waitForLines(t, 2)
		if len(lines) == 2 && lines[1] == "queued ?" {
			takenInRecord(t, records, "claude", "session-1", "", "?")
		}
		if err := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
			t.Fatal(err)
		}

		// Assert
		if len(lines) != 2 || lines[1] != "queued ?" {
			t.Fatalf("terminal input = %q; want the ? queued in the turn as words once", lines)
		}
		if settled := store.Snapshot().Actions[0]; settled.Status != "succeeded" || settled.Awaiting != nil {
			t.Errorf("once the CEO's record shows it = %+v; want it delivered, not left waiting", settled)
		}
	})
}

// His message to a Claude Code CEO is delivered, and drawn as his row with
// nothing left waiting, in each shape Claude Code records it (#2242): sent
// between turns, as a user record once dequeued; sent during a turn, as a
// queued_command attachment its model takes in that turn; and a ! command,
// queued by its words alone and run as a bash-input row. The board types a
// command only once the CEO's turn has ended (#2332), so that one reaches
// an idle CEO.
func TestHisMessageToAClaudeCEOIsDeliveredAndDrawnInEachShapeItsRecordTakes(t *testing.T) {
	for _, shape := range []struct {
		name   string
		typed  string
		record func(at, recorded string) []map[string]any
		source string
		drawn  string
		queued bool
		isIdle bool
	}{
		{name: "between turns", typed: "check the gate", source: fleettree.FromOverlord, drawn: "check the gate",
			record: func(at, _ string) []map[string]any {
				return []map[string]any{
					{"type": "queue-operation", "operation": "enqueue", "timestamp": at, "content": "check the gate"},
					{"type": "queue-operation", "operation": "dequeue", "timestamp": at},
					{"type": "user", "uuid": "u1", "timestamp": at, "message": map[string]any{"role": "user", "content": "check the gate"}},
				}
			}},
		{name: "during a turn", typed: "check the gate", source: fleettree.FromOverlord, drawn: "check the gate", queued: true,
			record: func(at, _ string) []map[string]any {
				return []map[string]any{
					{"type": "queue-operation", "operation": "enqueue", "timestamp": at, "content": "check the gate"},
					{"type": "attachment", "uuid": "a1", "timestamp": at, "attachment": map[string]any{"type": "queued_command", "prompt": "check the gate", "commandMode": "prompt"}},
					{"type": "queue-operation", "operation": "remove", "timestamp": at, "content": "check the gate"},
				}
			}},
		// Claude Code records a ! line by its words after the !, and the board
		// draws its own shell-mode line as the command he typed (#2051).
		{name: "a ! command typed once the turn has ended", typed: "! claude doctor", source: fleettree.FromShell, drawn: "claude doctor", isIdle: true,
			record: func(at, recorded string) []map[string]any {
				return []map[string]any{
					{"type": "queue-operation", "operation": "enqueue", "timestamp": at, "content": recorded},
					{"type": "queue-operation", "operation": "dequeue", "timestamp": at},
					{"type": "user", "uuid": "u1", "timestamp": at, "message": map[string]any{"role": "user", "content": "<bash-input>" + recorded + "</bash-input>"}},
				}
			}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			mode, taken := "claude-manual-busy", "queued "
			if shape.isIdle {
				mode, taken = "claude-manual", "accepted "
			}
			terminal := nativeCEOComposer(t, home.State, mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "sent", Kind: "message", Text: shape.typed}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act: the board types it, then the CEO's record shows it taken.
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			lines := terminal.waitForLines(t, 2)
			typed, isShell, err := shellMessageLine(home.State, "sent", "", shape.typed)
			if err != nil {
				t.Fatal(err)
			}
			if !isShell {
				typed = shape.typed
			}
			var record []byte
			for _, entry := range shape.record(time.Now().UTC().Format(time.RFC3339Nano), strings.TrimPrefix(typed, "! ")) {
				line, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				record = append(append(record, line...), '\n')
			}
			path := filepath.Join(records, ".claude", "projects", "C--ceo-home", "session-1.jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, record, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
				t.Fatal(err)
			}
			var reader fleettree.Reader
			page, _, err := reader.MessagePage(path, -1)
			if err != nil {
				t.Fatal(err)
			}
			page.Messages, page.Waiting = shownShellCommands(home.State, page.Messages, page.Waiting)

			// Assert
			if len(lines) != 2 || lines[1] != taken+typed {
				t.Fatalf("terminal input = %q; want the message typed once", lines)
			}
			if settled := store.Snapshot().Actions[0]; settled.Status != "succeeded" || settled.Awaiting != nil {
				t.Errorf("once the CEO's record shows it taken = %+v; want it delivered, not left on its way", settled)
			}
			if len(page.Messages) != 1 || page.Messages[0].Kind != fleettree.MessagePrompt || page.Messages[0].Source != shape.source || page.Messages[0].Text != shape.drawn || page.Messages[0].IsQueued != shape.queued {
				t.Errorf("conversation = %+v; want his one row, %s %q, queued %v", page.Messages, shape.source, shape.drawn, shape.queued)
			}
			if len(page.Waiting) != 0 {
				t.Errorf("waiting = %+v; want nothing left Sending", page.Waiting)
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

func TestHisLongMultiLineMessageStartsAnIdleClaudeCEOsTurnWholeAndOnce(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-ready")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	message := longMultiLineMessage()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := service.deliverMessage(ctx, Action{Kind: "message", Text: message})

	// Assert
	if err != nil || result.Awaiting != nil {
		t.Fatalf("deliverMessage = %+v, %v; want it taken at once by the idle CEO", result, err)
	}
	lines := terminal.lines(t)
	if len(lines) != 2 || lines[1] != "accepted "+oneLine(message) {
		t.Errorf("terminal input = %q; want one submitted turn holding the whole message", lines)
	}
}

// A line starting with $ is words, so a long one goes as the paste any other
// long message does, and its head is not lost as #1740's was.
func TestHisLongMultiLineMessageToAClaudeCEOInATurnIsTakenWholeOnce(t *testing.T) {
	for _, message := range []string{longMultiLineMessage(), "$40k a month: " + longMultiLineMessage()} {
		t.Run(message[:12], func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "claude-busy")
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "long-message", Kind: "message", Text: message}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

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
			if sent.Status != "running" || sent.Awaiting == nil || sent.Message != heldByCEO || sent.CanSendNow {
				t.Fatalf("send during the turn = %+v; want it queued in the CEO's session, awaiting the CEO's next tool call", sent)
			}
			if want := []string{"queued " + oneLine(message), "accepted " + oneLine(message)}; len(lines) != 3 || lines[1] != want[0] || lines[2] != want[1] {
				t.Errorf("terminal input = %q; want the whole message queued once and taken once", lines)
			}
			if delivered.Status != "succeeded" || delivered.Awaiting != nil || delivered.CanSendNow {
				t.Errorf("once the CEO's record shows it taken = %+v; want it delivered with its queued mark gone", delivered)
			}
		})
	}
}

// sendNow presses Send now on message id of task in the board, which asks the
// action API, with the board's token, to send it now.
func sendNow(service *Service, task, id string) *httptest.ResponseRecorder {
	return boardRequest(NewHTTP(service, "board.local", nil), "POST", "/api/actions", fmt.Sprintf(`{"id":"now-%s","kind":"message_now","task_id":%q,"generation":"","target":%q}`, id, task, id))
}

// The CEO, 2026-11-03, withdrawing the 2026-11-02 rule that interrupting
// stays the box's Stop: his message to the CEO waits on the board for its
// undo window, and Send now skips the window, types it into the turn and
// presses the harness's own send-now key once, which interrupts that turn
// and has the CEO take the message at once. Once typed it is the harness's,
// so Send now is no longer offered. A line starting with $ is words, not a
// command, so it waits too.
func TestSendNowInterruptsTheCEOsTurnAndDeliversHisMessageOnce(t *testing.T) {
	for _, test := range []struct{ mode, text, interrupt string }{
		{mode: "claude-busy", text: "stop and rebase", interrupt: "sent now"},
		{mode: "claude-busy", text: "$40k a month is too much, stop the migration", interrupt: "sent now"},
		{mode: "busy", text: "stop and rebase", interrupt: "interrupted"},
	} {
		t.Run(test.mode+" "+test.text, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: test.text}); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			held, typedInWindow := store.Snapshot().Actions[0], terminal.lines(t)

			// Act
			first := sendNow(service, "", "m1")
			second := sendNow(service, "", "m1")
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}

			// Assert
			if held.Status != "queued" || !held.CanSendNow || len(typedInWindow) != 1 {
				t.Fatalf("in its undo window = %+v, terminal %q; want it held on the board, nothing typed, with Send now offered", held, typedInWindow)
			}
			if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
				t.Fatalf("Send now answered %d %q, then %d %q; want both accepted", first.Code, first.Body, second.Code, second.Body)
			}
			if lines := terminal.waitForLines(t, 3); len(lines) != 3 || !strings.HasPrefix(lines[1], "queued ") || !strings.HasSuffix(lines[1], test.text) || lines[2] != test.interrupt {
				t.Fatalf("terminal input = %q; want the message queued into the turn once and the harness's own send-now key pressed once", lines)
			}
			time.Sleep(500 * time.Millisecond)
			if lines := terminal.lines(t); len(lines) != 3 || lines[2] != test.interrupt {
				t.Errorf("terminal input = %q; want the send-now key pressed exactly once", lines)
			}
			if sent := store.Snapshot().Actions[0]; !sent.IsSentNow || sent.CanSendNow || sent.Status != "running" || sent.Awaiting == nil || sent.Message != sentNowInTurn {
				t.Errorf("after Send now = %+v; want it typed into the turn, awaiting the CEO, and Send now gone", sent)
			}
		})
	}
}

// Send now interrupts the turn its message was typed into, never the turn
// that is already answering that message: a turn that ends as the answer is
// typed and submitted leaves the busy read from before typing stale, and its
// harness starts a turn on the just-submitted answer, which its own record
// proves taken. The key is not pressed then, as a goblin's delivery returns
// once the goblin's record proves the text taken rather than pressing it.
func TestSendNowDoesNotInterruptTheTurnThatTookHisAnswerFirst(t *testing.T) {
	// Arrange
	home := useConversations(t)
	store, h := testStore(t)
	terminal := nativeCEOComposer(t, h.State, "claude-busy")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: h.State}}}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	// Its record shows the answer taken by the turn the submit started.
	takenInRecordAt(t, home, "claude", "session-1", "", "stop and rebase", time.Now().Add(time.Hour))

	// Act
	sendNow(service, "", "m1")
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}

	// Assert: the answer is queued once and no interrupt key follows it.
	if lines := terminal.waitForLines(t, 2); len(lines) != 2 || lines[1] != "queued stop and rebase" {
		t.Fatalf("terminal input = %q; want the answer typed into the turn once", lines)
	}
	time.Sleep(500 * time.Millisecond)
	if lines := terminal.lines(t); len(lines) != 2 {
		t.Errorf("terminal input = %q; want no interrupt key after the answer", lines)
	}
	if sent := store.Snapshot().Actions[0]; sent.Status != "succeeded" || sent.Message != "Taken by the CEO in its native terminal, as its record showed." {
		t.Errorf("after Send now = %+v; want it taken by the record, with no interrupt key", sent)
	}
}

// A board message or answer from him is his own input, so the CEO's next
// screen is bound to him, while a notice the supervisor writes is nobody's and
// clears that binding: a picker a notice draws never reaches Modern Chat as
// his.
func TestABoardMessageKeepsHisMarkOnTheCEOsTerminalAndANoticeClearsIt(t *testing.T) {
	// Arrange
	useConversations(t)
	store, h := testStore(t)
	ceo := nativeCEOComposer(t, h.State, "ready")
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: h.State}}}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "carry on"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	record, err := host.ReadRecord(h.State, "ceo")
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := host.View(record)
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()

	// Assert: his board message is his.
	if _, submission, _, err := viewer.Screen(record, ""); err != nil || submission == "" {
		t.Fatalf("submission = %q, %v after his board message; want the screen bound to him", submission, err)
	}

	// Act: a notice the supervisor writes.
	if _, err := service.Options.CEO.SendNotice(context.Background(), registrationIdentity(t, h.State), "Review request for CEO: look"); err != nil {
		t.Fatal(err)
	}

	// Assert: the notice is nobody's input.
	if _, submission, _, err := viewer.Screen(record, ""); err != nil || submission != "" {
		t.Fatalf("submission = %q, %v after a notice; want it cleared", submission, err)
	}
	if lines := ceo.lines(t); !slices.Contains(lines, "accepted carry on") {
		t.Errorf("terminal input = %q; want his message typed", lines)
	}
}

// A line waiting in Claude Code's queue leaves its prompt line empty but for
// the hint it draws there, which is nothing he left unsent, so his next message
// is typed into the turn behind it and not held back as if it were.
func TestHisNextMessageIsTypedBehindOneInABusyClaudeCEOsQueue(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	for _, id := range []string{"m1", "m2"} {
		if _, err := store.Queue(Action{ID: id, Kind: "message", Text: "from " + id}); err != nil {
			t.Fatal(err)
		}
	}
	pastUndoWindow(t, store)
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	ceoShows(t, home.State, "❯ Press up to edit queued messages")

	// Act
	err := store.ProcessOne(context.Background(), service.execute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	lines := terminal.waitForLines(t, 3)
	if len(lines) != 3 || !strings.HasSuffix(lines[1], "from m1") || !strings.HasSuffix(lines[2], "from m2") {
		t.Fatalf("terminal input = %q; want both messages queued in turn", lines)
	}
	for _, sent := range store.Snapshot().Actions {
		if sent.Status != "running" || sent.Awaiting == nil || sent.Message != heldByCEO {
			t.Errorf("%s = %+v; want it typed into the turn, awaiting the CEO", sent.ID, sent)
		}
	}
}

// typeIntoCEO types keys into the CEO's terminal, as he does in it, and waits
// until its screen shows shown.
func typeIntoCEO(t *testing.T, stateDir, keys, shown string) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, "ceo")
	if err != nil {
		t.Fatal(err)
	}
	client, err := host.Dial(record)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Input([]byte(keys))
	client.Close()
	if err != nil {
		t.Fatal(err)
	}
	ceoShows(t, stateDir, shown)
}

// ceoShows waits until the CEO's screen shows shown.
func ceoShows(t *testing.T, stateDir, shown string) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, "ceo")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		screen, err := host.ReadScreen(record)
		if err == nil && strings.Contains(strings.Join(screen, "\n"), shown) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the CEO's screen never showed %q: %v", shown, err)
		}
	}
}

// The Overlord, 2026-11-02: "nor do they work with interrupts". Claude Code,
// Codex and pi put the lines typed into a turn back on the prompt line when
// the turn is interrupted, so his message typed into the CEO's turn, which he
// then stopped with the box's Stop, goes as the CEO's next prompt rather than
// sitting there unsent while its bubble says Sent. While the turn runs it is
// left in the harness's queue.
func TestHisMessageInATurnHeStoppedGoesAsTheCEOsNextPrompt(t *testing.T) {
	for _, test := range []struct{ mode, prompt, ready string }{
		{mode: "claude-busy", prompt: "❯ stop and rebase", ready: "bypass permissions on"},
		{mode: "busy", prompt: "› stop and rebase", ready: "100% context left"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)
			if err := store.ProcessOne(context.Background(), service.execute); err != nil {
				t.Fatal(err)
			}
			inTurn := terminal.waitForLines(t, 2)
			inTurnErr := service.submitHandedBack(context.Background())
			typeIntoCEO(t, home.State, "\x1b", test.prompt)
			// The prompt line is drawn before the row that shows the harness
			// ready, so the one look below waits for the whole screen.
			ceoShows(t, home.State, test.ready)

			// Act
			err := service.submitHandedBack(context.Background())

			// Assert
			if inTurnErr != nil || err != nil {
				t.Fatalf("submitHandedBack = %v in the turn, %v after it; want no error", inTurnErr, err)
			}
			if want := []string{"queued stop and rebase"}; !slices.Equal(inTurn[1:], want) {
				t.Errorf("terminal input in the turn = %q; want %q, nothing pressed", inTurn, want)
			}
			if lines := terminal.waitForLines(t, 4); !slices.Equal(lines[1:], []string{"queued stop and rebase", "interrupted", "accepted stop and rebase"}) {
				t.Errorf("terminal input = %q; want his message submitted once its turn was stopped", lines)
			}
			if sent := actionOf(store, "m1"); sent.Status != "running" || sent.Awaiting == nil || sent.Message != "Its turn was stopped, so it was submitted as the next prompt." {
				t.Errorf("after the turn was stopped = %+v; want it submitted again, awaiting the CEO", sent)
			}
		})
	}
}

// A turn he stopped puts every line typed into it back on the prompt line
// together, which goes as one prompt, so each of those lines is taken once the
// CEO's record shows that prompt, rather than called never picked up for
// awaiting its own line alone. A line typed before them that is not on the
// prompt line still awaits its own words.
func TestLinesHandedBackTogetherAreTakenFromTheOnePromptTheyWentAs(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-ready")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	at := time.Now().UTC()
	for n, typed := range []string{"an earlier one", "check the tests", "stop and rebase"} {
		since := at.Add(time.Duration(n-3) * time.Second)
		store.db.Actions = append(store.db.Actions, Action{ID: fmt.Sprintf("m%d", n), Kind: "message", Text: typed, Status: "running", Message: sentToCEO, CreatedAt: since, UpdatedAt: since,
			Awaiting: &Awaiting{Host: "ceo", Harness: "claude", Since: since, QuietSince: since, Digest: monitor.TextDigest(typed), Session: "session-1", Typed: typed}})
	}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	typeIntoCEO(t, home.State, "check the tests stop and rebase", "❯ check the tests stop and rebase")
	ceoShows(t, home.State, "bypass permissions on")

	// Act
	err := service.submitHandedBack(context.Background())
	lines := terminal.waitForLines(t, 2)
	takenInRecord(t, records, "claude", "session-1", "", "check the tests\nstop and rebase")
	settled := store.settleDeliveries(time.Now().UTC(), service.lookAtTerminal)

	// Assert
	if err != nil || settled != nil {
		t.Fatalf("submitHandedBack = %v, settleDeliveries = %v; want no error", err, settled)
	}
	if !slices.Equal(lines[1:], []string{"accepted check the tests stop and rebase"}) {
		t.Errorf("terminal input = %q; want the two lines submitted as one prompt", lines)
	}
	for _, id := range []string{"m1", "m2"} {
		if taken := actionOf(store, id); taken.Status != "succeeded" {
			t.Errorf("%s = %+v; want it taken from the one prompt it went in", id, taken)
		}
	}
	if earlier := actionOf(store, "m0"); earlier.Status != "running" || earlier.Message != sentToCEO || earlier.Awaiting == nil || earlier.Awaiting.Digest != monitor.TextDigest("an earlier one") {
		t.Errorf("m0 = %+v; want the line that was not on the prompt line still awaiting its own words", earlier)
	}
}

// Words of his own on the CEO's prompt line are never submitted for him, even
// while a message of his typed into a turn awaits the CEO.
func TestHisOwnWordsOnTheCEOsPromptLineAreNeverSubmittedForHim(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-ready")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	at := time.Now().UTC()
	store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", Text: "stop and rebase", Status: "running", CreatedAt: at, UpdatedAt: at, Awaiting: &Awaiting{Host: "ceo", Harness: "claude", Since: at, QuietSince: at, Typed: "stop and rebase"}})
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	typeIntoCEO(t, home.State, "and the", "❯ and the")

	// Act
	err := service.submitHandedBack(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if lines := terminal.lines(t); len(lines) != 1 {
		t.Errorf("terminal input = %q; want nothing submitted", lines)
	}
}

// A paste of his after his message, which a turn he stopped handed back to
// the CEO's prompt line, is never submitted for him: the line goes only while
// it ends with the message.
func TestHisPasteAfterAHandedBackMessageIsNeverSubmittedForHim(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)
	if err := store.ProcessOne(context.Background(), service.execute); err != nil {
		t.Fatal(err)
	}
	terminal.waitForLines(t, 2)
	typeIntoCEO(t, home.State, "\x1b", "❯ stop and rebase")
	typeIntoCEO(t, home.State, "\x1b[200~and a note of mine\x1b[201~", "❯ stop and rebase[Pasted text #1]")

	// Act
	err := service.submitHandedBack(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if lines := terminal.lines(t); !slices.Equal(lines[1:], []string{"queued stop and rebase", "interrupted"}) {
		t.Errorf("terminal input = %q; want nothing submitted over his paste", lines)
	}
	if sent := actionOf(store, "m1"); sent.Status != "running" || sent.Message != heldByCEO {
		t.Errorf("after his paste = %+v; want it still awaiting the CEO, never submitted again", sent)
	}
}

// A lone "?" of his on the CEO's prompt line, handed back by a turn he
// stopped, is never submitted with words of his beside it, even words that
// end as it does: the line is submitted only while it holds nothing else.
func TestAHandedBackQuestionMarkIsNeverSubmittedWithHisWordsBesideIt(t *testing.T) {
	for name, words := range map[string]string{
		"his words after it":  "? is the build green?",
		"his words before it": "is the build green ?",
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "claude-ready")
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			at := time.Now().UTC()
			store.db.Actions = append(store.db.Actions, Action{ID: "m1", Kind: "message", Text: "?", Status: "running", CreatedAt: at, UpdatedAt: at, Awaiting: &Awaiting{Host: "ceo", Harness: "claude", Since: at, QuietSince: at, Typed: "?", Pasted: true}})
			if err := store.save(); err != nil {
				t.Fatal(err)
			}
			typeIntoCEO(t, home.State, words, "❯ "+words)

			// Act
			err := service.submitHandedBack(context.Background())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(500 * time.Millisecond)
			if lines := terminal.lines(t); len(lines) != 1 {
				t.Errorf("terminal input = %q; want nothing submitted over his words", lines)
			}
			if sent := actionOf(store, "m1"); sent.Message == "Its turn was stopped, so it was submitted as the next prompt." {
				t.Errorf("after his words = %+v; want it left on his prompt line unsubmitted", sent)
			}
		})
	}
}

// A message typed into the CEO's turn is its harness's the moment its submit
// key goes: the turn takes it only at its next tool call, which only the CEO's
// own record shows, and settleDeliveries reads that record. So the delivery
// waits on no take, and his bubble reads Queued in the CEO's session at once,
// with the controls that act on that queue (#2332), rather than Queued on the
// board while his words already sit in the CEO's queue.
func TestADeliveryToACEOInATurnReturnsOnceItsSubmitKeyWent(t *testing.T) {
	for _, mode := range []string{"claude-busy", "busy"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			started := time.Now()

			// Act
			result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: "stop and rebase"})
			took := time.Since(started)

			// Assert
			if err != nil || result.Reason != heldByCEO || result.Awaiting == nil || result.Awaiting.Typed != "stop and rebase" {
				t.Fatalf("deliverMessage = %+v, %v; want it queued in the CEO's session and awaiting the CEO's next tool call", result, err)
			}
			// stall-accepted: the delivery that waited for a busy CEO's take returned after exactly nativeConfirm, so no longer bound can tell it apart.
			if took >= nativeConfirm {
				t.Errorf("deliverMessage returned after %v; want it back once its submit key went, before the %v an idle CEO's take is waited for", took, nativeConfirm)
			}
			if lines := terminal.waitForLines(t, 2); len(lines) != 2 || lines[1] != "queued stop and rebase" {
				t.Errorf("terminal input = %q; want the message queued in the turn once", lines)
			}
		})
	}
}

// The Overlord, 2026-11-13: "the CEO just queues the message and puts it into
// the chat input, but doesn't actually fire the message" (#2541). A message
// typed into the CEO's turn read Queued in the CEO's session once its submit
// key went, though a Claude Code slow to read, as on a loaded machine, takes
// that key as a new line and leaves the words on its prompt line unsent. The
// key is pressed again while the words show over the row it added, and words
// that never leave the prompt line are taken back off it and read Not sent.
func TestHisMessageInTheCEOsTurnIsQueuedOnlyOnceItLeavesThePromptLine(t *testing.T) {
	previous := nativeShown
	nativeShown = 2 * time.Second
	t.Cleanup(func() { nativeShown = previous })
	for _, tc := range []struct {
		mode     string
		isQueued bool
	}{
		{mode: "claude-busy-lost-enter", isQueued: true},
		{mode: "claude-busy-enters-lost", isQueued: false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, tc.mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}

			// Act
			result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: "stop and rebase"})

			// Assert
			input := strings.Join(terminal.lines(t), "\n")
			if tc.isQueued {
				if err != nil || result.Reason != heldByCEO || result.Awaiting == nil || strings.Count(input, "queued stop and rebase") != 1 {
					t.Errorf("deliverMessage = %+v, %v with terminal input %q; want it queued in the turn once, after its submit key was pressed again", result, err, input)
				}
				return
			}
			if !errors.Is(err, ErrNotSent) || result.Awaiting != nil || strings.Contains(input, "queued ") || !strings.Contains(input, "cleared") {
				t.Errorf("deliverMessage = %+v, %v with terminal input %q; want it taken back off the prompt line and Not sent", result, err, input)
			}
		})
	}
}

// The Overlord's message of 2026-11-13 19:02Z reached the CEO's turn with
// words after it that the board never typed, and the board's Enter sent them
// with his: it checked only that his words ended on the prompt line. Its take
// search looks for the words it typed, so the message read Queued for hours
// (#2541). Words beside his on the prompt line are never submitted with them:
// the line is left as it is, and his words are watched where they were typed.
func TestWordsBesideHisMessageOnTheCEOsPromptLineAreNeverSubmittedWithIt(t *testing.T) {
	for _, mode := range []string{"claude-busy-joined", "claude-ready-joined"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}

			// Act
			result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: "stop and rebase"})

			// Assert
			if err == nil || result.Awaiting != nil || result.LeftTyped == nil || result.LeftTyped.Typed != "stop and rebase" {
				t.Errorf("deliverMessage = %+v, %v; want nothing submitted and his words watched where they were typed", result, err)
			}
			if input := strings.Join(terminal.lines(t), "\n"); strings.Contains(input, "queued ") || strings.Contains(input, "accepted ") {
				t.Errorf("terminal input = %q; want nothing submitted", input)
			}
		})
	}
}

// The Overlord, 2026-11-13: "Stale old messages that appear randomly when
// memory gets low" (#2541). A machine short of memory reads a terminal slowly,
// and a read that failed once a message's submit key went ended its delivery
// in doubt with nothing left watching for its take, so a message the CEO took
// read Not confirmed for good. One whose submit key went is still watched, and
// its take settles it as Sent.
func TestAMessageWhoseScreenCouldNotBeReadAfterItsSubmitKeyIsSettledByItsTake(t *testing.T) {
	for _, mode := range []string{"claude-ready", "claude-busy"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			nativeCEOComposer(t, home.State, mode)
			screens, _ := harness.NativeScreens(harness.Claude)
			hasShown := false
			connection := &CEOConnection{State: home.State, ReadScreen: func(record host.Record) ([]string, error) {
				screen, err := host.ReadScreen(record)
				if err != nil {
					return nil, err
				}
				hasShown = hasShown || screens.Shows(screen, "stop and rebase")
				if hasShown && !screens.Shows(screen, "stop and rebase") {
					return nil, errors.New("the read of the screen timed out")
				}
				return screen, nil
			}}
			service := &Service{Store: store, Options: Options{CEO: connection}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act
			processed := store.ProcessOne(context.Background(), service.execute)
			doubted := actionOf(store, "m1")
			takenInRecord(t, records, "claude", "session-1", "", "stop and rebase")
			settled := store.settleDeliveries(time.Now(), service.lookAtTerminal)
			sent := actionOf(store, "m1")

			// Assert
			if processed != nil || settled != nil {
				t.Fatal(processed, settled)
			}
			if doubted.Status != "uncertain" || doubted.Awaiting == nil {
				t.Errorf("after the failed read = %s %q, awaiting %v; want it in doubt and still watched for its take", doubted.Status, doubted.Message, doubted.Awaiting)
			}
			if sent.Status != "succeeded" {
				t.Errorf("after its take = %s %q; want it Sent", sent.Status, sent.Message)
			}
		})
	}
}

// A read that failed once his words were typed, before their submit key, left
// them on the CEO's prompt line with nothing watching them, where they held
// back every message after them. They are watched where they were typed, to be
// taken back off the prompt line once they show there alone, as words left
// typed for any other reason are (#2306, #2541).
func TestHisWordsTypedBeforeAFailedReadAreWatchedWhereTheyWereTyped(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failing int
	}{
		{name: "the read that looked for them", failing: 1},
		{name: "the read before their submit key", failing: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, "claude-ready")
			screens, _ := harness.NativeScreens(harness.Claude)
			shown := 0
			connection := &CEOConnection{State: home.State, ReadScreen: func(record host.Record) ([]string, error) {
				screen, err := host.ReadScreen(record)
				if err == nil && screens.Shows(screen, "stop and rebase") {
					if shown++; shown == tc.failing {
						return nil, errors.New("the read of the screen timed out")
					}
				}
				return screen, err
			}}
			service := &Service{Store: store, Options: Options{CEO: connection}}

			// Act
			result, err := service.deliverMessage(context.Background(), Action{Kind: "message", Text: "stop and rebase"})

			// Assert
			if err == nil || result.Awaiting != nil || result.LeftTyped == nil || result.LeftTyped.Typed != "stop and rebase" {
				t.Errorf("deliverMessage = %+v, %v; want nothing submitted and his words watched where they were typed", result, err)
			}
			if input := strings.Join(terminal.lines(t), "\n"); strings.Contains(input, "accepted ") {
				t.Errorf("terminal input = %q; want nothing submitted", input)
			}
		})
	}
}

// The Overlord, 2026-11-13: "when a chat is not turning or it's not running.
// The CEO, it just queues the message and puts it into the chat input, but
// doesn't actually fire the message and then start the turn. From scrap, um,
// from a startup, fix that." (#2541). His message reaches a CEO that is not in
// a turn, whatever its screen said as the board typed: one that just started
// and, slow to read on a loaded machine, took the submit key as a new line, as
// a resumed goblin's Claude Code did on 2026-10-20; one whose turn ended as his
// words reached it; and one whose stop hook opened its next turn as they did,
// as the CEO's did for a wake through that evening. It reads Sent once the
// CEO's record shows it taken.
func TestHisMessageIsTakenByACEOThatIsNotInATurn(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		input      []string
	}{
		{name: "one that just started and took its submit key as a new line", mode: "claude-ready-lost-enter", input: []string{"enter taken as a new line", "accepted stop and rebase"}},
		{name: "one whose turn ended as his words reached it", mode: "claude-busy-ends", input: []string{"turn ended", "accepted stop and rebase"}},
		{name: "one whose stop hook opened its next turn as his words reached it", mode: "claude-ready-reopens", input: []string{"turn opened by its stop hook", "queued stop and rebase", "accepted stop and rebase"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, tc.mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act
			processed := store.ProcessOne(context.Background(), service.execute)
			if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			input := terminal.waitForLines(t, len(tc.input)+1)[1:]
			takenInRecord(t, records, "claude", "session-1", "", "stop and rebase")
			settled := store.settleDeliveries(time.Now(), service.lookAtTerminal)
			sent := actionOf(store, "m1")

			// Assert
			if processed != nil || settled != nil {
				t.Fatal(processed, settled)
			}
			if !slices.Equal(input, tc.input) {
				t.Errorf("terminal input = %q; want %q", input, tc.input)
			}
			if sent.Status != "succeeded" || sent.Awaiting != nil {
				t.Errorf("once the CEO's record shows it taken = %s %q, awaiting %v; want it Sent", sent.Status, sent.Message, sent.Awaiting)
			}
		})
	}
}

// The Overlord, 2026-11-14 01:09Z: "again fix modernt erminal durign
// compaction it renders not confirmed old stale messages as if its queued or
// being sent" (#2541). The CEO's Claude Code queued his two messages while it
// compacted and took them once it was back. One sent while the CEO compacts
// reads Queued in its turn and Sent once taken, never dropped.
func TestHisMessageToACEOThatIsCompactingIsTakenOnceItIsBack(t *testing.T) {
	// Arrange
	records := useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "claude-busy-compacting")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
		t.Fatal(err)
	}
	pastUndoWindow(t, store)

	// Act
	processed := store.ProcessOne(context.Background(), service.execute)
	queued := actionOf(store, "m1")
	if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	input := terminal.waitForLines(t, 3)[1:]
	takenInRecord(t, records, "claude", "session-1", "", "stop and rebase")
	settled := store.settleDeliveries(time.Now(), service.lookAtTerminal)
	sent := actionOf(store, "m1")

	// Assert
	if processed != nil || settled != nil {
		t.Fatal(processed, settled)
	}
	if queued.Status != "running" || queued.Awaiting == nil || !queued.Awaiting.IsHeld || queued.Message != heldByCEO {
		t.Errorf("while the CEO compacts = %s %q, awaiting %+v; want it queued in the CEO's turn", queued.Status, queued.Message, queued.Awaiting)
	}
	if want := []string{"queued stop and rebase", "accepted stop and rebase"}; !slices.Equal(input, want) {
		t.Errorf("terminal input = %q; want %q", input, want)
	}
	if sent.Status != "succeeded" || sent.Awaiting != nil {
		t.Errorf("once the CEO's record shows it taken = %s %q, awaiting %v; want it Sent", sent.Status, sent.Message, sent.Awaiting)
	}
}

// A harness that reads its keys late, as a Claude Code on a machine short of
// memory does, can take his message's submit key only after its delivery gave
// up on it with his words still on the prompt line, where they could not be
// taken back either. The message read Not confirmed for good, though the CEO
// had it. Its take settles it as Sent, and nothing of it is left to take back
// (#2541).
func TestHisMessageTheCEOTakesAfterItsDeliveryGaveUpReadsSentOnceTaken(t *testing.T) {
	previous := nativeShown
	nativeShown = 2 * time.Second
	t.Cleanup(func() { nativeShown = previous })
	for _, tc := range []struct {
		mode  string
		input []string
	}{
		{mode: "claude-ready-stalls", input: []string{"accepted stop and rebase"}},
		{mode: "claude-busy-stalls", input: []string{"queued stop and rebase", "accepted stop and rebase"}},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			// Arrange
			records := useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, tc.mode)
			screens, _ := harness.NativeScreens(harness.Claude)
			stall, hasStalled := terminal.typed+".stall", false
			connection := &CEOConnection{State: home.State, ReadScreen: func(record host.Record) ([]string, error) {
				screen, err := host.ReadScreen(record)
				// The harness reads no key after the words it drew.
				if err == nil && !hasStalled && screens.Shows(screen, "stop and rebase") {
					hasStalled = true
					if err := os.WriteFile(stall, nil, 0o600); err != nil {
						return nil, err
					}
				}
				return screen, err
			}}
			service := &Service{Store: store, Options: Options{CEO: connection}}
			if _, err := store.Queue(Action{ID: "m1", Kind: "message", Text: "stop and rebase"}); err != nil {
				t.Fatal(err)
			}
			pastUndoWindow(t, store)

			// Act
			processed := store.ProcessOne(context.Background(), service.execute)
			doubted := actionOf(store, "m1")
			if err := os.Remove(stall); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(terminal.typed+".accept", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			input := terminal.waitForLines(t, len(tc.input)+1)[1:]
			takenInRecord(t, records, "claude", "session-1", "", "stop and rebase")
			settled := store.settleDeliveries(time.Now(), service.lookAtTerminal)
			sent := actionOf(store, "m1")

			// Assert
			if processed != nil || settled != nil {
				t.Fatal(processed, settled)
			}
			if doubted.Status != "uncertain" || doubted.Awaiting == nil || doubted.LeftTyped == nil {
				t.Errorf("once its delivery gave up = %s %q, awaiting %v, left typed %v; want it in doubt, watched for its take and where it was typed", doubted.Status, doubted.Message, doubted.Awaiting, doubted.LeftTyped)
			}
			if !slices.Equal(input, tc.input) {
				t.Errorf("terminal input = %q; want %q", input, tc.input)
			}
			if sent.Status != "succeeded" || sent.Awaiting != nil || sent.LeftTyped != nil {
				t.Errorf("once the CEO's record shows it taken = %s %q, awaiting %v, left typed %v; want it Sent with nothing left to take back", sent.Status, sent.Message, sent.Awaiting, sent.LeftTyped)
			}
		})
	}
}

func TestNativeCEOAnswerPathsPreserveRepliesWhileItIsTyping(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"ceo_answer", "review_answer", "question_clear", "run_result"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				store, home := testStore(t)
				mode := "ready"
				if harness == "claude" {
					mode = "claude-ready"
				}
				terminal := nativeCEOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				connection := &CEOConnection{State: home.State, ReadScreen: func(host.Record) ([]string, error) {
					if harness == "claude" {
						rule := strings.Repeat("─", 80)
						return []string{rule, "❯ unfinished thought", rule, "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
					}
					return []string{"› unfinished thought", "100% context left"}, nil
				}}
				service := &Service{Store: store, Options: Options{CEO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
				switch kind {
				case "ceo_answer", "question_clear":
					question := Question{ID: "held-question", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
					if err := store.acceptQuestion(question); err != nil {
						t.Fatal(err)
					}
					action := Action{ID: "held-action", Kind: kind, Generation: identity, QuestionID: question.ID}
					if kind == "ceo_answer" {
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
				if kind == "ceo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("accepted question answer lacks its history author")
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("accepted review reply remains undelivered")
				}
			})
		}
	}
}

func TestNativeCEOBusyAnswersBecomeDeliveredOnlyWhenTheQueuedPromptIsTaken(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		for _, kind := range []string{"ceo_answer", "review_answer"} {
			t.Run(harness+"/"+kind, func(t *testing.T) {
				// Arrange
				records := useConversations(t)
				store, home := testStore(t)
				mode := "busy"
				if harness == "claude" {
					mode = "claude-busy"
				}
				terminal := nativeCEOComposer(t, home.State, mode)
				identity := registrationIdentity(t, home.State)
				service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
				action := Action{ID: "busy-answer", Kind: kind, Generation: identity, Text: "Reply received"}
				if kind == "ceo_answer" {
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
				if sent.Status != "running" || sent.Awaiting == nil || sent.Message != heldByCEO {
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
					t.Errorf("after the CEO's prompt hook alone = %+v; want it still on its way, since a hook runs as the harness queues the text", reported)
				}
				typed := strings.TrimPrefix(terminal.lines(t)[1], "queued ")
				takenInRecord(t, records, harness, "session-1", "", typed)
				if err := reopened.settleDeliveries(time.Now().UTC(), service.lookAtTerminal); err != nil {
					t.Fatal(err)
				}
				delivered := reopened.Snapshot().Actions[0]
				if delivered.Status != "succeeded" || delivered.Awaiting != nil || !strings.Contains(delivered.Message, "record of the conversation") {
					t.Errorf("once the CEO's record shows it taken = %+v; want it delivered", delivered)
				}
				if input := strings.Join(terminal.lines(t), "\n"); strings.Count(input, "queued Overlord:") != 1 || strings.Count(input, "accepted Overlord:") != 1 {
					t.Errorf("busy input = %q; want one submission and one later acceptance", input)
				}
				if kind == "review_answer" && !reopened.Snapshot().Reviews[0].Delivered {
					t.Error("the accepted review still reads undelivered")
				}
				if kind == "ceo_answer" && reopened.Snapshot().Questions[0].AnsweredBy != "overlord" {
					t.Error("the accepted question lacks its history author")
				}
			})
		}
	}
}

func TestAnUntypedCEOAnswerStaysQueuedAcrossReopen(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	_, identity, _, _ := primaryFixture(t, store)
	question := Question{ID: "held-answer", Identity: identity, Text: "Choose", Options: []string{"One"}, CreatedAt: time.Now().UTC()}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	answer := Action{ID: "held-action", Kind: "ceo_answer", Generation: identity, QuestionID: question.ID, Text: "One"}
	if _, err := store.Queue(answer); err != nil {
		t.Fatal(err)
	}

	// Act
	err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
		return Evaluation{}, errors.Join(ErrDeferred, errors.New("The CEO is typing; your answer is queued and nothing was sent."))
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
		return Evaluation{Reason: "Taken by the CEO."}, nil
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
		{ID: "held-action", Kind: "ceo_answer", Generation: identity, QuestionID: question.ID, Text: "One"},
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

// An answer reaches a CEO at a live Daybreak offer, which one Escape
// dismisses before the answer is typed, and one whose offer left its rows on
// screen after that Escape: they are history that owns no key, so the answer
// is typed and submitted under them, never queued behind them (#1556).
func TestNativeCEOProvesAcceptanceAcrossTheDialogAndPasteBoundary(t *testing.T) {
	for _, mode := range []string{"daybreak", "paste", "late-modal", "daybreak-stuck"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			terminal := nativeCEOComposer(t, stateDir, mode)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			result, err := (&CEOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")
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

func TestNativeCEOCannotCallAnAnswerSentWhileItRemainsInTheIdleComposer(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	terminal := nativeCEOComposer(t, stateDir, "ignored-enter")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := (&CEOConnection{State: stateDir}).Send(ctx, registrationIdentity(t, stateDir), "Reply received")

	// Assert
	if err == nil || errors.Is(err, ErrDeferred) || result.Awaiting != nil {
		t.Errorf("idle unsent input = %+v, %v; want unconfirmed typed input, without Sent or automatic replay", result, err)
	}
	if input := strings.Join(terminal.lines(t), "\n"); strings.Contains(input, "accepted ") || strings.Contains(input, "queued ") {
		t.Errorf("idle input = %q; want no accepted or queued prompt", input)
	}
}

// A slash command the CEO's harness runs itself starts no turn, and Codex
// and pi record none, so an idle CEO took it once it left the input line,
// and the board reads it taken rather than Sent. A command typed into a
// Codex or pi turn could be dropped with nothing to show it (#2193), and one
// in Claude Code's queue is taken alone by its send-now key, ahead of his
// message behind it (#2332), so it waits for the turn to end, nothing typed,
// and a message behind it can be sent now. A command its harness runs at
// once beside a turn waits for nothing: it is typed into the turn, as he
// types it in the terminal, and reads taken once the prompt line shows it
// ran (the Overlord, 2026-11-11: "cant type / commands into modern chat like
// terminal", #2372). One of those its harness queued after all reads queued
// in the CEO's session, as his words there do.
func TestASlashCommandReachesTheCEOInItsTrueState(t *testing.T) {
	for _, test := range []struct {
		mode      string
		command   string
		typed     string
		isWaiting bool
		isHeld    bool
	}{
		{mode: "claude-ready", command: "/remote-control", typed: "accepted /remote-control"},
		{mode: "ready", command: "/remote-control", typed: "accepted /remote-control"},
		{mode: "claude-busy", command: "/remote-control", typed: "ran /remote-control"},
		{mode: "busy", command: "/status", typed: "ran /status"},
		{mode: "claude-busy", command: "/model opus", typed: "queued /model opus", isHeld: true},
		{mode: "claude-busy", command: "/compact", isWaiting: true},
		{mode: "busy", command: "/remote-control", isWaiting: true},
		// A ! command waits as a slash command does.
		{mode: "claude-busy", command: "! claude doctor", isWaiting: true},
	} {
		t.Run(test.mode+" "+strings.TrimLeft(test.command, "/! "), func(t *testing.T) {
			// Arrange
			useConversations(t)
			store, home := testStore(t)
			terminal := nativeCEOComposer(t, home.State, test.mode)
			service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			// Act
			result, err := service.deliverMessage(ctx, Action{ID: "command", Kind: "message", Text: test.command})

			// Assert
			lines := terminal.lines(t)
			if !test.isWaiting {
				lines = terminal.waitForLines(t, 2)
			}
			switch {
			case test.isWaiting:
				if !errors.Is(err, ErrDeferred) || len(lines) != 1 || !result.CanSendNow || result.Reason != "Queued until the CEO's turn ends. Send now has it take the message at once." {
					t.Errorf("deliverMessage = %+v, %v, terminal input %q; want it held for the turn's end, nothing typed, with Send now for the words behind it", result, err, lines)
				}
			case test.isHeld:
				if err != nil || result.Awaiting == nil || !result.Awaiting.IsHeld || result.Reason != heldByCEO || len(lines) != 2 || lines[1] != test.typed {
					t.Errorf("deliverMessage = %+v, %v, terminal input %q; want it typed once and queued in the CEO's session", result, err, lines)
				}
			default:
				if err != nil || result.Awaiting != nil || !strings.Contains(result.Reason, "Taken by the CEO") || len(lines) != 2 || lines[1] != test.typed {
					t.Errorf("deliverMessage = %+v, %v, terminal input %q; want it typed once and taken", result, err, lines)
				}
			}
		})
	}
}

// His words queued in the CEO's Claude Code turn leave its prompt line
// offering its queue, and it offers that queue still once a command typed
// after them ran beside the turn. The offer was there before the command, so
// it says nothing of the command, which reads taken as it does with nothing
// queued (#2372).
func TestACommandRunBesideTheCEOsTurnReadsTakenWithHisWordsInItsQueue(t *testing.T) {
	// Arrange
	_, service, terminal, _, _ := heldQueue(t, false, "claude-busy", "use staging")
	promptLineEmpties(t, terminal, "claude")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := service.deliverMessage(ctx, Action{ID: "command", Kind: "message", Text: "/remote-control"})

	// Assert
	if err != nil || result.Awaiting != nil || result.Reason != "Taken by the CEO in its native terminal, which ran your command beside its turn." {
		t.Errorf("deliverMessage = %+v, %v; want the command read as run beside the turn", result, err)
	}
	if lines := terminal.waitForLines(t, 3); len(lines) != 3 || lines[1] != "queued use staging" || lines[2] != "ran /remote-control" {
		t.Errorf("terminal input = %q; want his words queued, then the command typed once and run", lines)
	}
}

// An idle CEO whose input line clears for an answer before its working row
// draws has run no command, so only a command reads as one it ran, and an
// answer still waits on the CEO's record as Sent (#2193).
func TestAnAnswerAnIdleCEOClearsWithNoTurnShownReadsSentNotRun(t *testing.T) {
	// Arrange
	useConversations(t)
	store, home := testStore(t)
	terminal := nativeCEOComposer(t, home.State, "unmoved")
	service := &Service{Store: store, Options: Options{CEO: &CEOConnection{State: home.State}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	// Act
	result, err := service.deliverMessage(ctx, Action{Kind: "message", Text: "use staging"})

	// Assert
	if err != nil || result.Awaiting == nil || result.Reason != sentToCEO {
		t.Errorf("deliverMessage = %+v, %v; want it Sent and awaiting the CEO's record, never a command it ran", result, err)
	}
	if lines := terminal.waitForLines(t, 2); len(lines) != 2 || lines[1] != "accepted use staging" {
		t.Errorf("terminal input = %q; want the answer typed once", lines)
	}
}
