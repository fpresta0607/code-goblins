package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func cfoReceiptRecipient(stateDir string) nativehook.CFORecipient {
	return nativehook.CFORecipient{State: stateDir, HostID: "cfo-host", HostPID: 10, HostStart: time.Date(2026, 9, 15, 8, 0, 0, 0, time.UTC), ProgramPID: 20, ProgramStart: time.Date(2026, 9, 15, 8, 0, 1, 0, time.UTC), Harness: "claude", SessionID: "cfo-1", Registration: strings.Repeat("a", 64)}
}

func TestAnUnboundCFOAnswerIsNotConfirmedByAReplacementPrompt(t *testing.T) {
	for _, status := range []string{"running", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			// Arrange: an older store recorded only the logical terminal name.
			store, home := testStore(t)
			since := time.Now().UTC().Add(-time.Hour)
			sentAnswer(t, store, since)
			store.mu.Lock()
			store.db.Actions[0].Status = status
			store.db.Actions[0].Awaiting.Recipient = nativehook.CFORecipient{}
			store.updateQuestionOutcomes()
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(home)
			if err != nil {
				t.Fatal(err)
			}
			tookPrompt(t, reopened, since.Add(deliveryQuiet+time.Second))

			// Act
			err = reopened.settleDeliveries(since.Add(deliveryQuiet+2*time.Second), idle)
			action, question := outcome(reopened)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if action.Status != "uncertain" || question.Status != "uncertain" || question.AnsweredBy != "" {
				t.Errorf("replacement prompt settled an unbound answer: action=%s question=%s answered_by=%q, want uncertain with no accepted answer", action.Status, question.Status, question.AnsweredBy)
			}
		})
	}
}

func TestACFOAnswerRequiresTheExactRecipientOfItsLatePrompt(t *testing.T) {
	tests := []struct {
		name       string
		change     func(*nativehook.CFORecipient)
		isAccepted bool
	}{
		{"same recipient", func(*nativehook.CFORecipient) {}, true},
		{"same creation instants in another timezone", func(r *nativehook.CFORecipient) {
			r.HostStart = r.HostStart.In(time.FixedZone("proof", -5*60*60))
			r.ProgramStart = r.ProgramStart.In(time.FixedZone("proof", -5*60*60))
		}, true},
		{"missing receipt binding", func(r *nativehook.CFORecipient) { *r = nativehook.CFORecipient{} }, false},
		{"another home", func(r *nativehook.CFORecipient) { r.State += "-another" }, false},
		{"another terminal", func(r *nativehook.CFORecipient) { r.HostID = "other-cfo" }, false},
		{"replacement host", func(r *nativehook.CFORecipient) { r.HostPID++ }, false},
		{"reused host pid", func(r *nativehook.CFORecipient) { r.HostStart = r.HostStart.Add(time.Nanosecond) }, false},
		{"replacement program on the same thread", func(r *nativehook.CFORecipient) { r.ProgramPID++ }, false},
		{"reused program pid", func(r *nativehook.CFORecipient) { r.ProgramStart = r.ProgramStart.Add(time.Nanosecond) }, false},
		{"another harness", func(r *nativehook.CFORecipient) { r.Harness = "codex" }, false},
		{"another thread", func(r *nativehook.CFORecipient) { r.SessionID = "cfo-2" }, false},
		{"another registration", func(r *nativehook.CFORecipient) { r.Registration = strings.Repeat("b", 64) }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			since := time.Now().UTC().Add(-time.Hour)
			sentAnswer(t, store, since)
			store.mu.Lock()
			store.db.Actions[0].Status = "uncertain"
			store.db.Actions[0].Awaiting.Harness = "claude"
			store.db.Actions[0].Awaiting.Recipient = cfoReceiptRecipient(home.State)
			store.updateQuestionOutcomes()
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(home)
			if err != nil {
				t.Fatal(err)
			}
			tookPrompt(t, reopened, since.Add(deliveryQuiet+time.Second))
			reopened.mu.Lock()
			session := reopened.db.Sessions["claude/cfo-1"]
			session.PromptRecipient = cfoReceiptRecipient(home.State)
			test.change(&session.PromptRecipient)
			reopened.db.Sessions[session.ID] = session
			reopened.mu.Unlock()

			// Act
			err = reopened.settleDeliveries(since.Add(deliveryQuiet+2*time.Second), idle)
			action, question := outcome(reopened)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if (action.Status == "succeeded") != test.isAccepted || (question.AnsweredBy == "overlord") != test.isAccepted {
				t.Errorf("late receipt: action=%s answered_by=%q, want accepted=%t", action.Status, question.AnsweredBy, test.isAccepted)
			}
			if !test.isAccepted && (action.Awaiting == nil || !action.Awaiting.Recipient.Matches(cfoReceiptRecipient(home.State)) || question.Status != "uncertain") {
				t.Errorf("rejected receipt changed the original recipient or uncertain history: awaiting=%t question=%s", action.Awaiting != nil, question.Status)
			}
		})
	}
}

func TestNativeCFORecipientCapturesTheRegisteredProgramAndConversation(t *testing.T) {
	tests := []struct {
		name               string
		changeRecord       func(*host.Record)
		changeConversation func(*CFOConversation)
		isValid            bool
	}{
		{name: "registered recipient", isValid: true},
		{name: "replacement program", changeRecord: func(r *host.Record) { r.ChildPID = 4 }},
		{name: "reused program pid", changeRecord: func(r *host.Record) { r.ChildStart = r.ChildStart.Add(time.Nanosecond) }},
		{name: "missing host", changeRecord: func(r *host.Record) { r.HostPID = 0 }},
		{name: "missing program creation", changeRecord: func(r *host.Record) { r.ChildStart = time.Time{} }},
		{name: "another conversation program", changeConversation: func(c *CFOConversation) { c.PID++ }},
		{name: "another conversation host", changeConversation: func(c *CFOConversation) { c.Host = "another-cfo" }},
		{name: "another conversation harness", changeConversation: func(c *CFOConversation) { c.Harness = "codex" }},
		{name: "conversation from before program birth", changeConversation: func(c *CFOConversation) { c.Updated = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: process metadata only, no terminal or harness is started.
			stateDir := t.TempDir()
			nativePrimary(t, stateDir)
			primary, identity, err := readPrimary(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			recordHost(t, stateDir, os.Getpid())
			record, err := host.ReadRecord(stateDir, "cfo")
			if err != nil {
				t.Fatal(err)
			}
			record.ChildStart = primary.Process.Start
			if test.changeRecord != nil {
				test.changeRecord(&record)
			}
			recordBytes, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDir, "hosts", "cfo.json"), recordBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			conversation := CFOConversation{Harness: primary.Agent, Session: "saved-thread", Host: "cfo", PID: primary.Process.PID, Updated: time.Now().UTC()}
			if test.changeConversation != nil {
				test.changeConversation(&conversation)
			}
			conversationBytes, err := json.Marshal(conversation)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cfoConversationPath(stateDir), conversationBytes, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			recipient, err := NativeCFORecipient(stateDir)

			// Assert
			if test.isValid {
				if err != nil || !recipient.Valid() || recipient.Registration != identity || recipient.SessionID != "saved-thread" || recipient.ProgramPID != primary.Process.PID || !recipient.ProgramStart.Equal(primary.Process.Start) || recipient.HostPID != record.HostPID || !recipient.HostStart.Equal(primary.Process.Start) {
					t.Errorf("recipient=%+v error=%v, want the exact registered program and conversation", recipient, err)
				}
			} else if !errors.Is(err, ErrRejected) || recipient.Valid() {
				t.Errorf("recipient=%+v error=%v, want an unverified recipient refused", recipient, err)
			}
		})
	}
}

func TestNativeCFOReceiptSurvivesIngestionAndStoreReopen(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	now := time.Now().UTC().Add(-time.Minute)
	recipient := cfoReceiptRecipient(home.State)
	sentAnswer(t, store, now)
	store.mu.Lock()
	store.db.Actions[0].Status = "uncertain"
	store.updateQuestionOutcomes()
	store.mu.Unlock()
	makeEvent := func(name string, at time.Time, binding nativehook.CFORecipient) nativehook.Event {
		input, err := json.Marshal(map[string]string{"session_id": "cfo-1", "cwd": home.Root, "hook_event_name": name})
		if err != nil {
			t.Fatal(err)
		}
		event, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: "claude", HostID: binding.HostID, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		event.Recipient = binding
		return event
	}
	if err := store.Accept(makeEvent("SessionStart", now, recipient)); err != nil {
		t.Fatal(err)
	}
	prompt := makeEvent("UserPromptSubmit", now.Add(time.Second), recipient)
	if err := nativehook.Spool(home.State, prompt); err != nil {
		t.Fatal(err)
	}
	if got, err := NativeHostPromptSince(home.State, recipient, now); err != nil || !got {
		t.Fatalf("spooled prompt=%t error=%v, want accepted by its recipient", got, err)
	}

	// Act: a new registration/start and tool activity never rebind that prompt.
	if err := store.Ingest(); err != nil {
		t.Fatal(err)
	}
	replacement := recipient
	replacement.HostID = "reopened-cfo"
	replacement.ProgramPID++
	replacement.ProgramStart = replacement.ProgramStart.Add(time.Second)
	replacement.Registration = strings.Repeat("b", 64)
	for _, event := range []nativehook.Event{makeEvent("SessionStart", now.Add(2*time.Second), replacement), makeEvent("PostToolUse", now.Add(3*time.Second), replacement)} {
		if err := store.Accept(event); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	session := reopened.Snapshot().Sessions["claude/cfo-1"]
	if !session.PromptAt.Equal(prompt.OccurredAt) || !recipient.Matches(session.PromptRecipient) {
		t.Errorf("reopen or lifecycle events changed the accepted prompt identity: %+v", session)
	}
	for _, check := range []struct {
		recipient  nativehook.CFORecipient
		isAccepted bool
	}{{recipient, true}, {replacement, false}, {nativehook.CFORecipient{}, false}} {
		if got, err := NativeHostPromptSince(home.State, check.recipient, now); err != nil || got != check.isAccepted {
			t.Errorf("stored prompt=%t error=%v, want accepted=%t", got, err, check.isAccepted)
		}
	}
	if err := reopened.settleDeliveries(now.Add(4*time.Second), idle); err != nil {
		t.Fatal(err)
	}
	action, question := outcome(reopened)
	if action.Status != "succeeded" || question.AnsweredBy != "overlord" {
		t.Errorf("the actual earlier receipt was lost after later lifecycle events: action=%s answered_by=%q", action.Status, question.AnsweredBy)
	}
}

func TestANewRegistrationCannotBindAnEarlierUnboundPrompt(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	now := time.Now().UTC().Add(-time.Minute)
	makeEvent := func(name string, at time.Time, recipient nativehook.CFORecipient) nativehook.Event {
		input, err := json.Marshal(map[string]string{"session_id": "cfo-1", "cwd": home.Root, "hook_event_name": name})
		if err != nil {
			t.Fatal(err)
		}
		e, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: "claude", HostID: "cfo-host", Now: at})
		if err != nil {
			t.Fatal(err)
		}
		e.Recipient = recipient
		return e
	}
	for _, event := range []nativehook.Event{makeEvent("SessionStart", now, nativehook.CFORecipient{}), makeEvent("UserPromptSubmit", now.Add(time.Second), nativehook.CFORecipient{})} {
		if err := store.Accept(event); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	for _, event := range []nativehook.Event{makeEvent("SessionStart", now.Add(2*time.Second), cfoReceiptRecipient(home.State)), makeEvent("PostToolUse", now.Add(3*time.Second), cfoReceiptRecipient(home.State))} {
		if err := store.Accept(event); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	session := store.Snapshot().Sessions["claude/cfo-1"]
	if !session.PromptAt.Equal(now.Add(time.Second)) || session.PromptRecipient != (nativehook.CFORecipient{}) {
		t.Errorf("registration or tool activity changed the unbound prompt: %+v", session)
	}
}

func TestANativeCFOSenderBindsTheActualReceivingProgram(t *testing.T) {
	// Arrange: the existing native fixture runs only this test binary.
	store, home := testStore(t)
	t.Setenv("HERDR_PANE_ID", "")
	cfo := hostTerminal(t, home.State, "cfo")
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the program recorded %q, want its registration", lines)
	}
	recipient, err := NativeCFORecipient(home.State)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	result, err := (&CFOConnection{State: home.State}).Send(context.Background(), recipient.Registration, "hello")

	// Assert
	if err != nil || result.Awaiting == nil || !recipient.Matches(result.Awaiting.Recipient) {
		t.Fatalf("Send=%+v error=%v, want the exact receiving program bound to the pending answer", result, err)
	}
	service := &Service{Store: store}
	if look := service.lookAtTerminal(*result.Awaiting); look.Gone {
		t.Error("the original receiving program was reported gone")
	}
	changed := *result.Awaiting
	changed.Recipient.ProgramStart = changed.Recipient.ProgramStart.Add(time.Nanosecond)
	if look := service.lookAtTerminal(changed); !look.Gone {
		t.Error("another program creation identity was reported as the original recipient")
	}
	if typed := cfo.exit(t); len(typed) != 2 || typed[1] != "Overlord: hello" {
		t.Errorf("the terminal received %q, want the registration and one answer", typed)
	}
}

func TestAClosedCFOsBoundLateReceiptStillSettlesAfterStoreReopen(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	since := time.Now().UTC().Add(-time.Minute)
	sentAnswer(t, store, since)
	if err := store.settleDeliveries(since.Add(time.Second), gone); err != nil {
		t.Fatal(err)
	}
	closed, _ := outcome(store)
	if closed.Status != "uncertain" {
		t.Fatalf("closed recipient action=%s, want uncertain", closed.Status)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	tookPrompt(t, reopened, since.Add(2*time.Second))

	// Act: the original program's actual receipt arrives after its closure.
	err = reopened.settleDeliveries(since.Add(3*time.Second), gone)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	action, question := outcome(reopened)
	if action.Status != "succeeded" || question.AnsweredBy != "overlord" {
		t.Errorf("original late receipt: action=%s answered_by=%q, want delivered", action.Status, question.AnsweredBy)
	}
}

func TestAGoblinsLateReceiptStillRequiresItsSpawnGeneration(t *testing.T) {
	for _, generation := range []string{"g1", "g2"} {
		t.Run(generation, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			now := time.Now().UTC().Add(-time.Minute)
			review := openReview("plan-task-1", "task-1")
			if err := store.acceptReview(review); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queue(Action{ID: "answer-review-1", Kind: "review_answer", ReviewID: review.ID, Generation: review.Identity, Text: "Go with the grid"}); err != nil {
				t.Fatal(err)
			}
			if err := store.ProcessOne(context.Background(), func(context.Context, Action) (Evaluation, error) {
				return Evaluation{Reason: sentToGoblin, Awaiting: &Awaiting{Task: "task-1", Generation: "g1", Since: now}}, nil
			}); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(home)
			if err != nil {
				t.Fatal(err)
			}
			reopened.mu.Lock()
			reopened.db.Sessions["codex/worker-1"] = Session{ID: "codex/worker-1", NativeID: "worker-1", Harness: "codex", Role: "goblin", TaskID: "task-1", Generation: generation, PromptAt: now.Add(time.Second)}
			reopened.mu.Unlock()

			// Act
			err = reopened.settleDeliveries(now.Add(2*time.Second), idle)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			snapshot := reopened.Snapshot()
			isAccepted := generation == "g1"
			if (snapshot.Actions[0].Status == "succeeded") != isAccepted || snapshot.Reviews[0].Delivered != isAccepted {
				t.Errorf("action=%s review delivered=%t, want accepted=%t", snapshot.Actions[0].Status, snapshot.Reviews[0].Delivered, isAccepted)
			}
		})
	}
}

func TestANativeCFOWakeDoesNotChangeItsRecipientDuringDelivery(t *testing.T) {
	for _, shouldChangeThread := range []bool{false, true} {
		name := "same recipient"
		if shouldChangeThread {
			name = "another thread after typing"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			t.Setenv("HERDR_PANE_ID", "")
			cfo := hostTerminal(t, stateDir, "cfo")
			cfo.typeLine(t, "register")
			if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
				t.Fatalf("the program recorded %q, want its registration", lines)
			}
			reads := 0
			connection := &CFOConnection{State: stateDir, ReadScreen: func(host.Record) ([]string, error) {
				reads++
				if reads == 1 {
					return []string{"ready"}, nil
				}
				if reads == 2 {
					if shouldChangeThread {
						conversation, err := ReadCFOConversation(stateDir)
						if err != nil {
							return nil, err
						}
						conversation.Session = "another-thread"
						data, err := json.Marshal(conversation)
						if err != nil {
							return nil, err
						}
						if err := os.WriteFile(cfoConversationPath(stateDir), data, 0o600); err != nil {
							return nil, err
						}
					}
					return []string{"CFO: wake proof"}, nil
				}
				return []string{"✽ Pondering… (esc to interrupt)"}, nil
			}}

			// Act
			err := connection.deliverNative(context.Background(), state.TaskMeta{ID: "cfo", Harness: "claude"}, "CFO: wake proof")

			// Assert
			if shouldChangeThread && !errors.Is(err, ErrRejected) {
				t.Errorf("changed recipient delivery error=%v, want rejected", err)
			} else if !shouldChangeThread && err != nil {
				t.Errorf("same recipient delivery error=%v, want accepted", err)
			}
			if typed := cfo.exit(t); len(typed) != 2 || typed[1] != "CFO: wake proof" {
				t.Errorf("the terminal received %q, want the registration and one wake", typed)
			}
		})
	}
}
