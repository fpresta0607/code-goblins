package nativehook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeContracts(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ harness, event, want string }{
		{"claude", "SessionStart", "started"}, {"claude", "Stop", "settled"},
		{"codex", "Stop", "settled"}, {"codex", "SessionEnd", "ended"},
		{"codex", "UserPromptSubmit", "active"}, {"pi", "agent_settled", "settled"},
		{"pi", "session_shutdown", "ended"},
	} {
		t.Run(tc.harness+tc.event, func(t *testing.T) {
			input, _ := json.Marshal(map[string]string{"session_id": "session-1", "cwd": t.TempDir(), "hook_event_name": tc.event, "turn_id": "turn-1"})
			e, err := Normalize(bytes.NewReader(input), Context{Harness: tc.harness, Role: "goblin", TaskID: "task-1", Generation: "spawn-1", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if e.Kind != tc.want || e.Schema != 1 || e.SessionID != "session-1" || e.TaskID != "task-1" || e.TurnID != "turn-1" || e.Generation != "spawn-1" {
				t.Fatalf("event = %+v", e)
			}
			stateDir := t.TempDir()
			if err := Spool(stateDir, e); err != nil {
				t.Fatal(err)
			}
			if err := Spool(stateDir, e); err != nil {
				t.Fatal(err)
			}
			files, _ := os.ReadDir(SpoolDir(stateDir))
			if len(files) != 1 {
				t.Fatalf("replayed event created %d files", len(files))
			}
			data, _ := os.ReadFile(filepath.Join(SpoolDir(stateDir), files[0].Name()))
			var roundTrip Event
			if err := json.Unmarshal(data, &roundTrip); err != nil || roundTrip != e {
				t.Fatalf("persisted event: %s, %v", data, err)
			}
		})
	}
}

func TestHookRejectsMalformedAndPrematureEvents(t *testing.T) {
	for _, input := range []string{
		`{`, `{ "session_id": "s", "cwd": "relative", "hook_event_name": "Stop" }`,
		`{"session_id":"s","cwd":"C:\\project","hook_event_name":"agent_end"}`,
		`{"session_id":"s","cwd":"C:\\project","hook_event_name":"agent_settled","pending_messages":true}`,
		strings.Repeat("x", MaxInputBytes+1),
	} {
		if _, err := Normalize(strings.NewReader(input), Context{Harness: "pi", Role: "cfo", Now: time.Now()}); err == nil {
			t.Fatalf("accepted %q", input[:min(len(input), 100)])
		}
	}
}

func TestHookDoesNotPersistPromptOrToolCredentials(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"session_id": "s", "cwd": t.TempDir(), "hook_event_name": "PostToolUse", "tool_response": "secret", "last_assistant_message": "secret"})
	e, err := Normalize(bytes.NewReader(input), Context{Harness: "codex", Role: "cfo", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(e)
	if strings.Contains(string(data), "secret") {
		t.Fatalf("payload leaked: %s", data)
	}
}

func TestStopInvocationsHaveIndependentIdentityWithinOneTurn(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"session_id": "s", "cwd": t.TempDir(), "hook_event_name": "Stop", "turn_id": "turn-1"})
	c := Context{Harness: "codex", Role: "cfo", Now: time.Now()}
	a, err := Normalize(bytes.NewReader(input), c)
	if err != nil {
		t.Fatal(err)
	}
	c.Now = c.Now.Add(time.Second)
	b, err := Normalize(bytes.NewReader(input), c)
	if err != nil || a.ID == b.ID {
		t.Fatalf("separate invocations collapsed: %s %s %v", a.ID, b.ID, err)
	}
}
