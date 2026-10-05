package nativehook

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNativeStartRetainsCompactSourceWithoutTakingAPrompt(t *testing.T) {
	root := t.TempDir()
	payload, err := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "source": "compact", "session_id": "same-thread", "turn_id": "active-turn", "cwd": root, "prompt": "secret-not-persisted"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := Normalize(strings.NewReader(string(payload)), Context{Harness: "codex", Role: "cfo"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Source string `json:"source"`
	}
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Source != "compact" || event.Kind != "started" || event.Prompt || event.SessionID != "same-thread" || event.TurnID != "active-turn" || strings.Contains(string(encoded), "secret-not-persisted") {
		t.Fatalf("compact identity = %s, want retained source without a manufactured prompt", encoded)
	}
}

func TestNativeStartSourceDoesNotTurnAStopIntoCompaction(t *testing.T) {
	payload, err := json.Marshal(map[string]string{"hook_event_name": "Stop", "source": "compact", "session_id": "same-thread", "cwd": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	event, err := Normalize(strings.NewReader(string(payload)), Context{Harness: "codex", Role: "cfo"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != "settled" || event.Prompt || strings.Contains(string(encoded), `"source"`) {
		t.Fatalf("Stop = %s, want only a settled notification", encoded)
	}
}

func TestNativeStartRejectsMalformedSource(t *testing.T) {
	for _, source := range []string{"compact\nStop", "compact\x00", strings.Repeat("x", 4097)} {
		payload, err := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "source": source, "session_id": "same-thread", "cwd": t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Normalize(strings.NewReader(string(payload)), Context{Harness: "codex", Role: "cfo"}); err == nil {
			t.Fatal("malformed start source was accepted")
		}
	}
}
