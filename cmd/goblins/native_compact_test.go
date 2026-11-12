package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestCodexCompactStartRehydratesOnlyTheCEO(t *testing.T) {
	for _, test := range []struct {
		name, harness, event, source, role, task string
		shouldRehydrate                          bool
	}{
		{"CEO compact", "codex", "SessionStart", "compact", "ceo", "", true},
		{"CEO startup", "codex", "SessionStart", "startup", "ceo", "", false},
		{"CEO resume", "codex", "SessionStart", "resume", "ceo", "", false},
		{"CEO Stop", "codex", "Stop", "compact", "ceo", "", false},
		{"goblin compact", "codex", "SessionStart", "compact", "goblin", "fixture", false},
		{"worker compact", "codex", "SessionStart", "compact", "worker", "", false},
		{"Claude compact", "claude", "SessionStart", "compact", "ceo", "", false},
		// A Codex session the Overlord opens himself carries no role and runs
		// in no native terminal: it is not the CEO's, and is told nothing.
		{"another Codex session's compact", "codex", "SessionStart", "compact", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			stateDir := filepath.Join(root, "state")
			for _, name := range []string{host.IDVariable, "HERDR_PANE_ID", "GOBLINS_SPAWN_GEN", "GOBLINS_PARENT_SESSION_ID", "GOBLINS_PARENT_HARNESS", "GOBLINS_ROOT_SESSION_ID"} {
				t.Setenv(name, "")
			}
			if test.role == "ceo" {
				asCEOSession(t, stateDir, os.Getpid())
			}
			t.Setenv("GOBLINS_ROLE", test.role)
			t.Setenv("GOBLINS_TASK_ID", test.task)
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
			for _, required := range []string{filepath.Join(root, "AGENTS.md"), "goblins session-start", "goblins memory retrieve", "handoff", "held", "goblins drain", "ack", "stow"} {
				if !strings.Contains(reply.Output.Context, required) {
					t.Errorf("compact context misses %q: %s", required, reply.Output.Context)
				}
			}
			if strings.Contains(reply.Output.Context, "overlord.md") || strings.Contains(reply.Output.Context, "MEMORY.md") {
				t.Fatal("compact recovery requested full cold history")
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

func TestCodexCompactRecoveryReadsFreshSelectiveAuthority(t *testing.T) {
	for _, isCorrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(isCorrupt), func(t *testing.T) {
			clearNativeCEOEnvironment(t)
			root := newCEOHome(t)
			t.Setenv("GOBLINS_ROLE", "ceo")
			t.Setenv("GOBLINS_TASK_ID", "")
			dataDir := filepath.Join(root, "data")
			writeMemoryCatalog(t, dataDir, "Old ruling.\n", "Approved cold history.\n")
			var previous, diagnostics bytes.Buffer
			if exit := run([]string{"session-start"}, &previous, &diagnostics); exit != 0 {
				t.Fatal(diagnostics.String())
			}
			if isCorrupt {
				if err := os.WriteFile(filepath.Join(dataDir, "overlord.md"), []byte("Changed ruling.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				writeMemoryCatalog(t, dataDir, "Changed ruling.\n", "Approved cold history.\n")
			}
			var reply bytes.Buffer
			payload, err := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "source": "compact", "session_id": "same-thread", "cwd": root})
			if err != nil {
				t.Fatal(err)
			}

			if exit := runNativeHook([]string{"codex", "--home", root, "--state", filepath.Join(root, "state")}, bytes.NewReader(payload), &reply, &diagnostics, commandRuntime{}); exit != 0 {
				t.Fatal(diagnostics.String())
			}
			var output bytes.Buffer
			if exit := run([]string{"session-start"}, &output, &diagnostics); exit != 0 {
				t.Fatal(diagnostics.String())
			}

			if !strings.Contains(reply.String(), "goblins session-start") || !strings.Contains(reply.String(), "goblins memory retrieve") {
				t.Fatalf("compact did not route through current composition: %s", reply.String())
			}
			if strings.Contains(output.String(), "Approved cold history.") || strings.Contains(output.String(), "Old ruling.") {
				t.Fatalf("compact recovery reread old or cold memory: %s", output.String())
			}
			if isCorrupt {
				if !strings.Contains(output.String(), "MEMORY UNREADABLE") || strings.Contains(output.String(), "Changed ruling.") {
					t.Fatal("compact recovery concealed invalid authority")
				}
			} else if !strings.Contains(output.String(), "Changed ruling.") {
				t.Fatal("compact recovery used the saved stale digest")
			}
		})
	}
}

// compactNatively hands compactNativeCEO one native compact event of the
// CEO's session, as the supervisor does once it ingests the event the hook
// spooled.
func compactNatively(t *testing.T, harness, event, root string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"hook_event_name": event, "session_id": "ceo-thread", "turn_id": "turn-1", "cwd": root, "trigger": "auto", "prompt": "must-not-persist"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := nativehook.Normalize(bytes.NewReader(payload), nativehook.Context{Harness: harness})
	if err != nil {
		t.Fatal(err)
	}
	if err := compactNativeCEO(home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}, e, time.Now()); err != nil {
		t.Fatalf("%s %s: %v", harness, event, err)
	}
}

func clearNativeCEOEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{host.IDVariable, "HERDR_PANE_ID", "GOBLINS_ROLE", "GOBLINS_TASK_ID", "GOBLINS_SPAWN_GEN", "GOBLINS_PARENT_SESSION_ID", "GOBLINS_PARENT_HARNESS", "GOBLINS_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
}

func TestNativeCEOCompactWritesACheckpointThenWakesOnce(t *testing.T) {
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
			if !strings.Contains(pending[0].Detail, checkpoint) || !strings.Contains(pending[0].Detail, "goblins drain") || !strings.Contains(pending[0].Detail, "goblins session-start") || strings.Contains(pending[0].Detail, "must-not-persist") {
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
	if !strings.Contains(pending[0].Detail, "no fresh checkpoint") || !strings.Contains(pending[0].Detail, "goblins drain") || !strings.Contains(pending[0].Detail, "goblins session-start") {
		t.Fatalf("post-compact wake without a checkpoint = %q", pending[0].Detail)
	}
}
