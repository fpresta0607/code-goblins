package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func registeredStartRecipient(t *testing.T, stateDir string) nativehook.CFORecipient {
	t.Helper()
	nativePrimary(t, stateDir)
	primary, _, err := readPrimary(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	recordHost(t, stateDir, os.Getpid())
	record, err := host.ReadRecord(stateDir, "cfo")
	if err != nil {
		t.Fatal(err)
	}
	record.ChildStart = primary.Process.Start
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", "cfo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(CFOConversation{Harness: primary.Agent, Session: "saved-thread", Host: "cfo", PID: primary.Process.PID, Updated: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfoConversationPath(stateDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	recipient, err := NativeCFORecipient(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	return recipient
}

func TestNativeRegistrationStartAdmissionRequiresItsOriginalAuthenticatedBinding(t *testing.T) {
	tests := []struct {
		name        string
		changeEvent func(*nativehook.Event, string)
		changeState func(*testing.T, string)
	}{
		{name: "legacy unbound prompt", changeEvent: func(e *nativehook.Event, _ string) { e.Recipient = nativehook.CFORecipient{} }},
		{name: "foreign state", changeEvent: func(e *nativehook.Event, root string) { e.Recipient.State = root }},
		{name: "foreign home", changeEvent: func(e *nativehook.Event, root string) { e.CWD = filepath.Join(root, "work") }},
		{name: "another registration", changeEvent: func(e *nativehook.Event, _ string) { e.Recipient.Registration = strings.Repeat("f", 64) }},
		{name: "another host", changeEvent: func(e *nativehook.Event, _ string) { e.HostID = "another-cfo"; e.Recipient.HostID = e.HostID }},
		{name: "reused host pid", changeEvent: func(e *nativehook.Event, _ string) {
			e.Recipient.HostStart = e.Recipient.HostStart.Add(-time.Nanosecond)
		}},
		{name: "reused program pid", changeEvent: func(e *nativehook.Event, _ string) {
			e.Recipient.ProgramStart = e.Recipient.ProgramStart.Add(time.Nanosecond)
		}},
		{name: "another saved thread", changeEvent: func(e *nativehook.Event, _ string) { e.SessionID = "other-thread"; e.Recipient.SessionID = e.SessionID }},
		{name: "another harness", changeEvent: func(e *nativehook.Event, _ string) { e.Harness = "codex"; e.Recipient.Harness = e.Harness }},
		{name: "before program birth", changeEvent: func(e *nativehook.Event, _ string) { e.OccurredAt = e.Recipient.ProgramStart.Add(-time.Nanosecond) }},
		{name: "spawned child", changeEvent: func(e *nativehook.Event, _ string) {
			e.ParentSessionID = "parent"
			e.ParentHarness = e.Harness
			e.Relation = "spawned"
		}},
		{name: "cfo with generation", changeEvent: func(e *nativehook.Event, _ string) { e.Generation = "g1" }},
		{name: "goblin without start", changeEvent: func(e *nativehook.Event, root string) {
			e.Role = "goblin"
			e.TaskID = "task-1"
			e.Generation = "g1"
			e.Harness = "codex"
			e.CWD = filepath.Join(root, "work")
			e.Recipient = nativehook.CFORecipient{}
		}},
		{name: "subagent without start", changeEvent: func(e *nativehook.Event, _ string) {
			e.Role = "subagent"
			e.ParentSessionID = "parent"
			e.ParentHarness = e.Harness
			e.Relation = "delegated"
			e.Recipient = nativehook.CFORecipient{}
		}},
		{name: "missing registration", changeState: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "primary.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing custody", changeState: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, ".lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "foreign custody", changeState: func(t *testing.T, dir string) {
			holder, err := lock.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			holder.Hostname = "foreign-machine"
			data, err := json.Marshal(holder)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lock"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "reused custody pid", changeState: func(t *testing.T, dir string) {
			holder, err := lock.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			holder.Start = holder.Start.Add(time.Nanosecond)
			data, err := json.Marshal(holder)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lock"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dead native host", changeState: func(t *testing.T, dir string) {
			record, err := host.ReadRecord(dir, "cfo")
			if err != nil {
				t.Fatal(err)
			}
			record.HostPID = 2147483647
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hosts", "cfo.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "replaced native program", changeState: func(t *testing.T, dir string) {
			record, err := host.ReadRecord(dir, "cfo")
			if err != nil {
				t.Fatal(err)
			}
			record.ChildPID++
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hosts", "cfo.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			recipient := registeredStartRecipient(t, home.State)
			input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": recipient.SessionID, "cwd": home.Root})
			if err != nil {
				t.Fatal(err)
			}
			event, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: recipient.Harness, HostID: recipient.HostID})
			if err != nil {
				t.Fatal(err)
			}
			event.Recipient = recipient
			if test.changeEvent != nil {
				test.changeEvent(&event, home.Root)
			}
			if test.changeState != nil {
				test.changeState(t, home.State)
			}
			originalRecipient := event.Recipient

			// Act
			err = store.Accept(event)

			// Assert
			if err == nil || (!errors.Is(err, ErrDeferred) && !errors.Is(err, ErrRejected)) || len(store.Snapshot().Sessions) != 0 || event.Recipient != originalRecipient {
				t.Errorf("unverified start error=%v sessions=%d; want refusal with the original event untouched", err, len(store.Snapshot().Sessions))
			}
		})
	}
}

func TestVerifiedNativeRegistrationAdmitsAnAuthenticPromptWithoutSessionStart(t *testing.T) {
	// Arrange: hooks were enabled after launch, so no SessionStart exists.
	store, home := testStore(t)
	since := time.Now().UTC().Add(-time.Second)
	sentAnswer(t, store, since)
	store.mu.Lock()
	store.db.Actions[0].Status = "uncertain"
	store.db.Actions[0].Awaiting.Recipient = nativehook.CFORecipient{}
	store.updateQuestionOutcomes()
	err := store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	recipient := registeredStartRecipient(t, home.State)
	input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": recipient.SessionID, "cwd": home.Root})
	if err != nil {
		t.Fatal(err)
	}
	event, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: recipient.Harness, HostID: recipient.HostID})
	if err != nil {
		t.Fatal(err)
	}
	event.Recipient = recipient
	if err := nativehook.Spool(home.State, event); err != nil {
		t.Fatal(err)
	}

	// Act: ingest the actual event without synthesizing a start or prompt.
	if err := store.Ingest(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.settleDeliveries(time.Now().UTC(), idle); err != nil {
		t.Fatal(err)
	}

	// Assert: the old unbound answer remains uncertain despite this prompt.
	session, hasSession := reopened.Snapshot().Sessions[recipient.Harness+"/"+recipient.SessionID]
	if !hasSession || !session.PromptAt.Equal(event.OccurredAt) || !session.PromptRecipient.Matches(recipient) || session.LastEventID != event.ID || session.Phase != "active" {
		t.Errorf("authentic prompt has no admitted session: present=%t session=%+v", hasSession, session)
	}
	action, question := outcome(reopened)
	if action.Status != "uncertain" || question.AnsweredBy != "" || action.Awaiting == nil || action.Awaiting.Recipient.Valid() {
		t.Errorf("registration admitted an old unbound answer: action=%s answered_by=%q", action.Status, question.AnsweredBy)
	}
}

func TestNativeRegistrationAdmitsToolOrStopActivityWithoutInventingPromptAcceptance(t *testing.T) {
	for _, name := range []string{"PostToolUse", "Stop", "Interrupt"} {
		t.Run(name, func(t *testing.T) {
			// Arrange: a pending answer has the actual current recipient.
			store, home := testStore(t)
			since := time.Now().UTC().Add(-time.Second)
			sentAnswer(t, store, since)
			recipient := registeredStartRecipient(t, home.State)
			store.mu.Lock()
			store.db.Actions[0].Awaiting.Recipient = recipient
			store.db.Actions[0].Awaiting.Host = recipient.HostID
			store.db.Actions[0].Awaiting.Harness = recipient.Harness
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			normalize := func(name string) nativehook.Event {
				input, err := json.Marshal(map[string]string{"hook_event_name": name, "session_id": recipient.SessionID, "cwd": home.Root})
				if err != nil {
					t.Fatal(err)
				}
				event, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: recipient.Harness, HostID: recipient.HostID})
				if err != nil {
					t.Fatal(err)
				}
				event.Recipient = recipient
				return event
			}

			// Act: a real tool/stop notification provides activity, not a prompt.
			if err := store.Accept(normalize(name)); err != nil {
				t.Fatal(err)
			}
			if err := store.settleDeliveries(time.Now().UTC(), idle); err != nil {
				t.Fatal(err)
			}

			// Assert
			session := store.Snapshot().Sessions[recipient.Harness+"/"+recipient.SessionID]
			action, question := outcome(store)
			if !session.PromptAt.IsZero() || session.PromptRecipient != (nativehook.CFORecipient{}) || action.Status != "running" || question.AnsweredBy != "" {
				t.Fatalf("non-prompt activity manufactured acceptance: prompt=%v action=%s answered_by=%q", session.PromptAt, action.Status, question.AnsweredBy)
			}
			prompt := normalize("UserPromptSubmit")
			if err := store.Accept(prompt); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := reopened.settleDeliveries(time.Now().UTC(), idle); err != nil {
				t.Fatal(err)
			}
			action, question = outcome(reopened)
			if action.Status != "succeeded" || question.AnsweredBy != "overlord" {
				t.Errorf("the later exact recipient prompt did not settle after Open: action=%s answered_by=%q", action.Status, question.AnsweredBy)
			}
		})
	}
}

func TestRegistrationNeverRetrofittedAPreRegistrationPrompt(t *testing.T) {
	// Arrange: the actual pre-registration event has no authenticated binding.
	store, home := testStore(t)
	input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "saved-thread", "cwd": home.Root})
	if err != nil {
		t.Fatal(err)
	}
	event, err := nativehook.Normalize(strings.NewReader(string(input)), nativehook.Context{Harness: "claude", HostID: "cfo"})
	if err != nil {
		t.Fatal(err)
	}
	if err := nativehook.Spool(home.State, event); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nativehook.SpoolDir(home.State), event.ID+".event.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(); err != nil {
		t.Fatal(err)
	}

	// Act: a later registration cannot authenticate an earlier invocation.
	registeredStartRecipient(t, home.State)
	if err := store.Ingest(); err != nil {
		t.Fatal(err)
	}

	// Assert: retain the exact event without creating session or prompt evidence.
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || len(store.Snapshot().Sessions) != 0 || len(store.Snapshot().Seen) != 0 {
		t.Errorf("registration changed the pre-registration event: error=%v sessions=%d seen=%d", err, len(store.Snapshot().Sessions), len(store.Snapshot().Seen))
	}
}
