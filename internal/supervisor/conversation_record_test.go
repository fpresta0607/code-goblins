package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// useConversations points the reader of the harnesses' records of their
// conversations, and every program the test starts, at a user home of the
// test's own, and returns it.
func useConversations(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	return home
}

// takenInRecord writes under home the record harness keeps of a
// conversation, showing text handed to its model now as it does when it
// takes text typed during a turn: Claude Code's queued_command attachment,
// Codex's user message. The record is the session's when session is set,
// else the one kept for the agent's folder dir.
func takenInRecord(t *testing.T, home, harness, session, dir, text string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var path string
	var entries []map[string]any
	switch harness {
	case "claude":
		folder := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(dir, "-")
		name := "conversation"
		if session != "" {
			folder, name = "C--cfo-home", session
		}
		path = filepath.Join(home, ".claude", "projects", folder, name+".jsonl")
		entries = []map[string]any{{"type": "attachment", "timestamp": now, "attachment": map[string]any{"type": "queued_command", "prompt": text}}}
	case "codex":
		if session == "" {
			session = "goblin-thread"
		}
		path = filepath.Join(home, ".codex", "sessions", "2026", "10", "06", "rollout-2026-10-06T21-00-00-"+session+".jsonl")
		entries = []map[string]any{
			{"type": "session_meta", "payload": map[string]any{"id": session, "cwd": dir}},
			{"timestamp": now, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}},
		}
	default:
		t.Fatalf("no record for harness %q", harness)
	}
	var data []byte
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
