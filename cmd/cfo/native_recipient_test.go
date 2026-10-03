package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func nativeHookRegistration(t *testing.T, harness string) (string, string, nativehook.CFORecipient) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := lock.Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(dir) })
	primary, err := json.Marshal(struct {
		Host    string    `json:"host"`
		Agent   string    `json:"agent"`
		Process lock.Info `json:"process"`
	}{"cfo", harness, *process})
	if err != nil {
		t.Fatal(err)
	}
	record, err := json.Marshal(host.Record{ID: "cfo", HostPID: os.Getpid(), ChildPID: os.Getpid(), ChildStart: process.Start, Pipe: `\\.\pipe\unused-model-free-hook-fixture`, Token: "fixture", Version: host.Version})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := json.Marshal(supervisor.CFOConversation{Harness: harness, Session: "saved-thread", Host: "cfo", PID: os.Getpid(), Updated: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		filepath.Join(dir, "primary.json"):          primary,
		filepath.Join(dir, "hosts", "cfo.json"):     record,
		filepath.Join(dir, "cfo-conversation.json"): conversation,
	} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"HERDR_PANE_ID", "CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
	t.Setenv("CFO_ROLE", "cfo")
	t.Setenv(host.IDVariable, "cfo")
	recipient, err := supervisor.NativeCFORecipient(dir)
	if err != nil {
		t.Fatal(err)
	}
	return root, dir, recipient
}

func TestNativeHookCallerFixture(t *testing.T) {
	if os.Getenv("CFO_HOOK_CALLER_FIXTURE") == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if os.Getenv("CFO_HOOK_CALLER_FIXTURE") != "prompt" {
		return
	}
	// TestMain clears terminal identity before running any test.
	t.Setenv("CFO_ROLE", "cfo")
	t.Setenv(host.IDVariable, "cfo")
	root := os.Getenv("CFO_HOOK_FIXTURE_HOME")
	event := spooledNativeHook(t, root, filepath.Join(root, "state"), "codex", "UserPromptSubmit", "saved-thread")
	if !event.Recipient.Valid() {
		t.Fatal("the actual descendant prompt has no authenticated recipient")
	}
}

func TestNativeHookAuthenticatesAnActualDescendantAndRejectsAnUnrelatedProgram(t *testing.T) {
	for _, shouldUseUnrelatedProgram := range []bool{false, true} {
		name := "actual descendant"
		if shouldUseUnrelatedProgram {
			name = "unrelated registered program"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange: run only this test binary, never a real terminal or model.
			root, dir, recipient := nativeHookRegistration(t, "codex")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestNativeHookCallerFixture$")
			mode := "prompt"
			if shouldUseUnrelatedProgram {
				mode = "hold"
			}
			command.Env = append(os.Environ(), "CFO_HOOK_CALLER_FIXTURE="+mode, "CFO_HOOK_FIXTURE_HOME="+root)
			if !shouldUseUnrelatedProgram {
				// Act
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("fixture: %v %s", err, output)
				}
			} else {
				input, err := command.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = input.Close(); _ = command.Wait() })
				start, isAlive := proc.StartTime(command.Process.Pid)
				if !isAlive {
					t.Fatal("the fixture did not start")
				}
				if err := lock.Release(dir); err != nil {
					t.Fatal(err)
				}
				holder, err := lock.AcquireOwner(dir, command.Process.Pid, "saved-thread")
				if err != nil {
					t.Fatal(err)
				}
				primary, err := json.Marshal(struct {
					Host    string    `json:"host"`
					Agent   string    `json:"agent"`
					Process lock.Info `json:"process"`
				}{"cfo", "codex", *holder})
				if err != nil {
					t.Fatal(err)
				}
				record, err := host.ReadRecord(dir, "cfo")
				if err != nil {
					t.Fatal(err)
				}
				record.ChildPID, record.ChildStart = command.Process.Pid, start
				recordBytes, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				conversation, err := json.Marshal(supervisor.CFOConversation{Harness: "codex", Session: "saved-thread", Host: "cfo", PID: command.Process.Pid, Updated: time.Now().UTC()})
				if err != nil {
					t.Fatal(err)
				}
				for path, data := range map[string][]byte{filepath.Join(dir, "primary.json"): primary, filepath.Join(dir, "hosts", "cfo.json"): recordBytes, filepath.Join(dir, "cfo-conversation.json"): conversation} {
					if err := os.WriteFile(path, data, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := supervisor.NativeCFORecipient(dir); err != nil {
					t.Fatalf("the unrelated registration is not live: %v", err)
				}
				// Act: the parent caller cannot authenticate as its child program.
				event := spooledNativeHook(t, root, dir, "codex", "UserPromptSubmit", "saved-thread")
				if event.Recipient != (nativehook.CFORecipient{}) {
					t.Error("a readable live registration authenticated an unrelated caller")
				}
				store, err := supervisor.Open(home.Home{Root: root, State: dir, Data: filepath.Join(root, "data")})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Ingest(); err != nil {
					t.Fatal(err)
				}
				if len(store.Snapshot().Sessions) != 0 {
					t.Error("an unrelated caller bypassed SessionStart through its readable registration")
				}
				return
			}

			// Assert: a child hook's actual ancestry reaches the registered CFO.
			files, err := os.ReadDir(nativehook.SpoolDir(dir))
			if err != nil || len(files) != 1 {
				t.Fatalf("spool count=%d error=%v", len(files), err)
			}
			data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			var event nativehook.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if !event.Recipient.Matches(recipient) {
				t.Error("the actual descendant hook lost its authenticated recipient")
			}
			store, err := supervisor.Open(home.Home{Root: root, State: dir, Data: filepath.Join(root, "data")})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Ingest(); err != nil {
				t.Fatal(err)
			}
			reopened, err := supervisor.Open(store.Home)
			if err != nil {
				t.Fatal(err)
			}
			session := reopened.Snapshot().Sessions["codex/saved-thread"]
			if session.LastEventID != event.ID || !session.PromptAt.Equal(event.OccurredAt) || !session.PromptRecipient.Matches(recipient) {
				t.Error("the actual descendant hook was not admitted through Spool/Ingest/Open without SessionStart")
			}
		})
	}
}

func spooledNativeHook(t *testing.T, root, dir, harness, eventName, session string) nativehook.Event {
	t.Helper()
	input, err := json.Marshal(map[string]string{"hook_event_name": eventName, "session_id": session, "cwd": root, "agent_id": "child-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if exit := runNativeHook([]string{harness, "--home", root, "--state", dir}, bytes.NewReader(input), &out, &errs, commandRuntime{}); exit != 0 || strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("hook exit=%d reply=%q diagnostics=%q", exit, out.String(), errs.String())
	}
	if errs.Len() != 0 {
		t.Logf("hook diagnostics: %s", errs.String())
	}
	files, err := os.ReadDir(nativehook.SpoolDir(dir))
	if err != nil || len(files) != 1 {
		t.Fatalf("hook spool count=%d error=%v", len(files), err)
	}
	data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestNativeHookBindsOnlyItsVerifiedCurrentCFORecipient(t *testing.T) {
	for _, harness := range []string{"codex", "claude", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange: a model-free live registration names this caller.
			root, dir, recipient := nativeHookRegistration(t, harness)
			eventName := "UserPromptSubmit"
			if harness == "pi" {
				eventName = "agent_start"
			}

			// Act
			event := spooledNativeHook(t, root, dir, harness, eventName, recipient.SessionID)

			// Assert
			if !event.Recipient.Matches(recipient) || !event.Prompt {
				t.Errorf("real prompt recipient=%+v prompt=%t, want the caller's exact current registration", event.Recipient, event.Prompt)
			}
		})
	}
}

func TestNativeHookLeavesUnverifiedEventsUnbound(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    func(*testing.T, string)
		harness   string
		session   string
		eventName string
	}{
		{name: "missing registration", change: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "primary.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing custody", change: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, ".lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "reused native program pid", change: func(t *testing.T, dir string) {
			record, err := host.ReadRecord(dir, "cfo")
			if err != nil {
				t.Fatal(err)
			}
			record.ChildStart = record.ChildStart.Add(time.Nanosecond)
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hosts", "cfo.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dead native host", change: func(t *testing.T, dir string) {
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
		{name: "stale saved conversation", change: func(t *testing.T, dir string) {
			conversation, err := supervisor.ReadCFOConversation(dir)
			if err != nil {
				t.Fatal(err)
			}
			conversation.Updated = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
			data, err := json.Marshal(conversation)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cfo-conversation.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "foreign custody", change: func(t *testing.T, dir string) {
			info, err := lock.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			info.Hostname = "foreign-machine"
			data, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lock"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "reused custody pid", change: func(t *testing.T, dir string) {
			info, err := lock.Read(dir)
			if err != nil {
				t.Fatal(err)
			}
			info.Start = info.Start.Add(time.Nanosecond)
			data, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".lock"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "another host", change: func(t *testing.T, _ string) { t.Setenv(host.IDVariable, "another-cfo") }},
		{name: "herdr descendant", change: func(t *testing.T, _ string) { t.Setenv("HERDR_PANE_ID", "w1:p1") }},
		{name: "goblin", change: func(t *testing.T, _ string) {
			t.Setenv("CFO_ROLE", "goblin")
			t.Setenv("CFO_TASK_ID", "task-1")
			t.Setenv("CFO_SPAWN_GEN", "g1")
		}},
		{name: "child session", change: func(t *testing.T, _ string) {
			t.Setenv("CFO_PARENT_SESSION_ID", "parent-thread")
			t.Setenv("CFO_PARENT_HARNESS", "codex")
		}},
		{name: "delegated subagent", eventName: "SubagentStart"},
		{name: "another harness", harness: "claude"},
		{name: "another saved thread", session: "another-thread"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root, dir, recipient := nativeHookRegistration(t, "codex")
			if test.change != nil {
				test.change(t, dir)
			}
			harness, session := test.harness, test.session
			if harness == "" {
				harness = "codex"
			}
			if session == "" {
				session = recipient.SessionID
			}
			eventName := test.eventName
			if eventName == "" {
				eventName = "UserPromptSubmit"
			}

			// Act
			event := spooledNativeHook(t, root, dir, harness, eventName, session)

			// Assert: preserve the actual event, without inventing its recipient.
			if event.Recipient != (nativehook.CFORecipient{}) || event.Prompt != (eventName == "UserPromptSubmit") {
				t.Errorf("unverified event gained a recipient or lost its actual prompt: %+v", event)
			}
		})
	}
}

func TestNativeHookDoesNotBindAPromptFromAnotherHome(t *testing.T) {
	// Arrange
	root, dir, _ := nativeHookRegistration(t, "codex")
	otherHome := t.TempDir()
	input, err := json.Marshal(map[string]string{"hook_event_name": "UserPromptSubmit", "session_id": "saved-thread", "cwd": otherHome})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer

	// Act
	if exit := runNativeHook([]string{"codex", "--home", root, "--state", dir}, bytes.NewReader(input), &out, &errs, commandRuntime{}); exit != 0 {
		t.Fatalf("exit=%d diagnostics=%s", exit, errs.String())
	}
	files, err := os.ReadDir(nativehook.SpoolDir(dir))
	if err != nil || len(files) != 1 {
		t.Fatalf("spool count=%d error=%v", len(files), err)
	}
	data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}

	// Assert
	if event.Recipient != (nativehook.CFORecipient{}) || event.CWD != otherHome || !event.Prompt {
		t.Error("another home gained a recipient or its original event changed")
	}
}
