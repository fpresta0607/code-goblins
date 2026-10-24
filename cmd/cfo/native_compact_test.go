package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
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
		// A Codex session the Overlord opens himself carries no role and runs
		// in no native terminal: it is not the CFO's, and is told nothing.
		{"another Codex session's compact", "codex", "SessionStart", "compact", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			for _, name := range []string{host.IDVariable, "HERDR_PANE_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
				t.Setenv(name, "")
			}
			if test.role == "cfo" {
				asCFOSession(t, stateDir, os.Getpid())
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

// compactNatively hands compactNativeCFO one native compact event of the
// CFO's session, as the supervisor does once it ingests the event the hook
// spooled.
func compactNatively(t *testing.T, harness, event, root string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"hook_event_name": event, "session_id": "cfo-thread", "turn_id": "turn-1", "cwd": root, "trigger": "auto", "prompt": "must-not-persist"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := nativehook.Normalize(bytes.NewReader(payload), nativehook.Context{Harness: harness})
	if err != nil {
		t.Fatal(err)
	}
	if err := compactNativeCFO(home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}, e, time.Now()); err != nil {
		t.Fatalf("%s %s: %v", harness, event, err)
	}
}

func clearNativeCFOEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{host.IDVariable, "HERDR_PANE_ID", "CFO_ROLE", "CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
}

func TestNativeCFOCompactWritesACheckpointThenWakesOnce(t *testing.T) {
	for _, test := range []struct{ harness, before, after string }{
		{"codex", "PreCompact", "PostCompact"},
		{"pi", "session_before_compact", "session_compact"},
	} {
		t.Run(test.harness, func(t *testing.T) {
			// Arrange
			root := newPrimaryHome(t)
			stateDirectory := filepath.Join(root, "state")
			checkpoint := filepath.Join(stateDirectory, digest.CheckpointFile)

			// Act
			compactNatively(t, test.harness, test.before, root)
			data, checkpointErr := os.ReadFile(checkpoint)
			compactNatively(t, test.harness, test.after, root)
			compactNatively(t, test.harness, test.after, root)

			// Assert
			if checkpointErr != nil || !strings.Contains(string(data), "== FLEET ==") {
				t.Fatalf("checkpoint before compaction = %q, %v", data, checkpointErr)
			}
			pending, err := wake.Pending(stateDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("pending wakes = %+v, want exactly one after a second post-compact event", pending)
			}
			if !strings.Contains(pending[0].Detail, checkpoint) || !strings.Contains(pending[0].Detail, "cfo drain") || strings.Contains(pending[0].Detail, "must-not-persist") {
				t.Fatalf("post-compact wake = %q, want the checkpoint path and the next step without prompt text", pending[0].Detail)
			}

			// A later compaction writes a fresh checkpoint and wakes again.
			compactNatively(t, test.harness, test.before, root)
			compactNatively(t, test.harness, test.after, root)
			if pending, err = wake.Pending(stateDirectory); err != nil || len(pending) != 2 {
				t.Fatalf("pending wakes after a second compaction = %+v, %v; want two", pending, err)
			}
		})
	}
}

func TestNativeCompactWithoutACheckpointSaysSo(t *testing.T) {
	// Arrange
	root := newPrimaryHome(t)
	stateDirectory := filepath.Join(root, "state")

	// Act
	compactNatively(t, "codex", "PostCompact", root)

	// Assert
	pending, err := wake.Pending(stateDirectory)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending wakes = %+v, %v; want one", pending, err)
	}
	if !strings.Contains(pending[0].Detail, "no fresh checkpoint") || !strings.Contains(pending[0].Detail, "cfo drain") {
		t.Fatalf("post-compact wake without a checkpoint = %q", pending[0].Detail)
	}
}

// Claude Code compacts through its own pre-compact hook and SessionStart
// digest, so its PreCompact never reaches the native entry.
func TestClaudeCompactEventsStayOnTheClaudeHook(t *testing.T) {
	// Arrange
	root := t.TempDir()
	clearNativeCFOEnvironment(t)
	payload, err := json.Marshal(map[string]string{"hook_event_name": "PreCompact", "session_id": "claude-session", "cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer

	// Act
	exit := runNativeHook([]string{"claude", "--home", root, "--state", filepath.Join(root, "state")}, bytes.NewReader(payload), &output, &diagnostics, commandRuntime{})

	// Assert
	if exit == 0 || strings.TrimSpace(diagnostics.String()) != "native hook event is invalid" {
		t.Fatalf("Claude PreCompact through the native entry: exit=%d %q, want an unsupported-event error", exit, diagnostics.String())
	}
}
