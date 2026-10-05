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

func TestInstallIncludesNativeCompactLifecycle(t *testing.T) {
	for _, harness := range []string{"codex", "pi"} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			configuration := InstallConfig{Harness: harness, ConfigDir: directory, Executable: filepath.Join(directory, "cfo.exe"), Home: directory, State: filepath.Join(directory, "state")}
			if harness == "codex" {
				original := []byte(`{"model":"unchanged","hooks":{"PreCompact":[{"hooks":[{"type":"command","command":"echo unrelated pre"}]}],"PostCompact":[{"hooks":[{"type":"command","command":"echo unrelated post"}]}]}}`)
				if err := os.WriteFile(filepath.Join(directory, "hooks.json"), original, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			path, err := Install(configuration)
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			if harness == "pi" {
				for _, event := range []string{"session_before_compact", "session_compact"} {
					if !strings.Contains(string(content), `pi.on("`+event+`"`) {
						t.Errorf("installed Pi extension has no %s handler", event)
					}
				}
				return
			}
			var document struct {
				Model string `json:"model"`
				Hooks map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
						Timeout int    `json:"timeout"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal(content, &document); err != nil {
				t.Fatal(err)
			}
			_, ownedCommand := helperCommand(directory)
			for _, event := range []string{"PreCompact", "PostCompact"} {
				ownedCount, unrelatedCount := 0, 0
				for _, group := range document.Hooks[event] {
					for _, handler := range group.Hooks {
						if handler.Command == ownedCommand {
							ownedCount++
							if handler.Timeout != 3 {
								t.Errorf("%s timeout = %d, want the unchanged 3-second contract", event, handler.Timeout)
							}
						} else if strings.HasPrefix(handler.Command, "echo unrelated ") {
							unrelatedCount++
						}
					}
				}
				if ownedCount != 1 || unrelatedCount != 1 {
					t.Errorf("%s handlers: owned=%d unrelated=%d, want one of each", event, ownedCount, unrelatedCount)
				}
			}
			if document.Model != "unchanged" {
				t.Error("install changed the unrelated model setting")
			}
		})
	}
}

func TestNativeCompactEventsNeverTakeAPrompt(t *testing.T) {
	for _, test := range []struct {
		harness, event, kind string
	}{
		{"codex", "PreCompact", "compacting"},
		{"codex", "PostCompact", "compacted"},
		{"pi", "session_before_compact", "compacting"},
		{"pi", "session_compact", "compacted"},
	} {
		t.Run(test.harness+"/"+test.event, func(t *testing.T) {
			// Arrange
			payload, err := json.Marshal(map[string]string{"hook_event_name": test.event, "session_id": "saved-thread", "cwd": t.TempDir(), "turn_id": "busy-turn", "prompt": "must-not-persist"})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			event, err := Normalize(bytes.NewReader(payload), Context{Harness: test.harness, Role: "cfo", HostID: "cfo", Now: time.Now()})
			if err != nil {
				t.Fatalf("missing native compact contract: %v", err)
			}

			// Assert
			if event.Kind != test.kind || event.Prompt || event.SessionID != "saved-thread" || event.TurnID != "busy-turn" {
				t.Fatalf("compact event changed turn or prompt identity: %+v", event)
			}
			serialized, err := json.Marshal(event)
			if err != nil || bytes.Contains(serialized, []byte("must-not-persist")) {
				t.Fatal("compact event retained prompt content")
			}
			event.Prompt = true
			if err := event.Validate(); err == nil {
				t.Fatal("compact lifecycle manufactured prompt acceptance")
			}
		})
	}
}
