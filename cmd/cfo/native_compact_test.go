package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

func TestCodexCompactStartRehydratesOnlyTheCFO(t *testing.T) {
	for _, test := range []struct {
		name, harness, event, source, role, task string
		shouldRehydrate                          bool
	}{
		{"CFO compact", "codex", "SessionStart", "compact", "cfo", "", true},
		{"CFO startup", "codex", "SessionStart", "startup", "cfo", "", false},
		{"CFO resume", "codex", "SessionStart", "resume", "cfo", "", false},
		{"CFO Stop", "codex", "Stop", "compact", "cfo", "", false},
		{"goblin compact", "codex", "SessionStart", "compact", "goblin", "fixture", false},
		{"worker compact", "codex", "SessionStart", "compact", "worker", "", false},
		{"Claude compact", "claude", "SessionStart", "compact", "cfo", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			for _, name := range []string{host.IDVariable, "HERDR_PANE_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
				t.Setenv(name, "")
			}
			t.Setenv("CFO_ROLE", test.role)
			t.Setenv("CFO_TASK_ID", test.task)
			payload, err := json.Marshal(map[string]string{"hook_event_name": test.event, "source": test.source, "session_id": "same-thread", "cwd": root})
			if err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer
			if exit := runNativeHook([]string{test.harness, "--home", root, "--state", stateDir}, bytes.NewReader(payload), &output, &diagnostics, commandRuntime{}); exit != 0 {
				t.Fatalf("native compact entry exit=%d: %s", exit, diagnostics.String())
			}
			var reply struct {
				Output struct {
					Event   string `json:"hookEventName"`
					Context string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(output.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			if !test.shouldRehydrate {
				if strings.TrimSpace(output.String()) != "{}" {
					t.Fatalf("unrelated lifecycle reply changed: %s", output.String())
				}
				return
			}
			if reply.Output.Event != "SessionStart" {
				t.Fatalf("compact reply = %s, want context for the immediate SessionStart continuation", output.String())
			}
			for _, required := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "data", "overlord.md"), filepath.Join(root, "data", "memory", "MEMORY.md"), "handoff", "held", "cfo drain", "ack", "stow"} {
				if !strings.Contains(reply.Output.Context, required) {
					t.Errorf("compact context misses %q: %s", required, reply.Output.Context)
				}
			}
			if bytes.Contains(output.Bytes(), []byte(`"decision"`)) || bytes.Contains(output.Bytes(), []byte(`"continue"`)) {
				t.Fatalf("rehydration changed continuation policy: %s", output.String())
			}
			files, err := os.ReadDir(nativehook.SpoolDir(stateDir))
			if err != nil || len(files) != 1 {
				t.Fatalf("native spool = %v, %v", files, err)
			}
			for _, name := range []string{"primary.json", ".session-start-complete", "session-digest.md"} {
				if _, err := os.Stat(filepath.Join(stateDir, name)); !os.IsNotExist(err) {
					t.Fatalf("compact context changed custody or digest file %s: %v", name, err)
				}
			}
		})
	}
}
