package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
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

// runNativeCompact feeds one native compact event to the hook entry as the
// harness's own hook would.
func runNativeCompact(t *testing.T, harness, event, root string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"hook_event_name": event, "session_id": "cfo-thread", "turn_id": "turn-1", "cwd": root, "trigger": "auto", "prompt": "must-not-persist"})
	if err != nil {
		t.Fatal(err)
	}
	var output, diagnostics bytes.Buffer
	if exit := runNativeHook([]string{harness, "--home", root, "--state", filepath.Join(root, "state")}, bytes.NewReader(payload), &output, &diagnostics, commandRuntime{}); exit != 0 {
		t.Fatalf("%s %s exit=%d: %s", harness, event, exit, diagnostics.String())
	}
	if strings.TrimSpace(output.String()) != "{}" {
		t.Fatalf("%s %s reply = %q, want {} so compaction proceeds unchanged", harness, event, output.String())
	}
	return diagnostics.String()
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
			clearNativeCFOEnvironment(t)
			setAncestorPID(t, os.Getpid())
			if _, err := lock.AcquireOwner(stateDirectory, os.Getpid(), "cfo-thread"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.Release(stateDirectory) })
			checkpoint := filepath.Join(stateDirectory, digest.CheckpointFile)

			// Act
			runNativeCompact(t, test.harness, test.before, root)
			data, checkpointErr := os.ReadFile(checkpoint)
			runNativeCompact(t, test.harness, test.after, root)
			runNativeCompact(t, test.harness, test.after, root)

			// Assert
			if checkpointErr != nil || !strings.Contains(string(data), "== FLEET ==") {
				t.Fatalf("checkpoint before compaction = %q, %v", data, checkpointErr)
			}
			pending, err := wake.Pending(stateDirectory)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("pending wakes = %+v, want exactly one after a replayed post-compact event", pending)
			}
			if !strings.Contains(pending[0].Detail, checkpoint) || !strings.Contains(pending[0].Detail, "cfo drain") || strings.Contains(pending[0].Detail, "must-not-persist") {
				t.Fatalf("post-compact wake = %q, want the checkpoint path and the next step without prompt text", pending[0].Detail)
			}
			if entries, err := os.ReadDir(nativehook.SpoolDir(stateDirectory)); err == nil && len(entries) != 0 {
				t.Fatalf("compact events entered the session spool: %v", entries)
			}

			// A later compaction writes a fresh checkpoint and wakes again.
			runNativeCompact(t, test.harness, test.before, root)
			runNativeCompact(t, test.harness, test.after, root)
			if pending, err = wake.Pending(stateDirectory); err != nil || len(pending) != 2 {
				t.Fatalf("pending wakes after a second compaction = %+v, %v; want two", pending, err)
			}
		})
	}
}

func TestNativeCompactActsOnlyForTheHomesCustodian(t *testing.T) {
	for _, test := range []struct {
		name                   string
		role, task, generation string
		isPrimary, isLocked    bool
		isForeignHolder        bool
	}{
		{name: "goblin", role: "goblin", task: "fixture", generation: "g1", isPrimary: true, isLocked: true},
		{name: "worker", role: "worker", isPrimary: true, isLocked: true},
		{name: "no custody", isPrimary: true},
		{name: "another process holds the home", isPrimary: true, isLocked: true, isForeignHolder: true},
		{name: "not the primary home", isLocked: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := newPrimaryHome(t)
			stateDirectory := filepath.Join(root, "state")
			if !test.isPrimary {
				if err := os.Remove(filepath.Join(root, "AGENTS.md")); err != nil {
					t.Fatal(err)
				}
			}
			clearNativeCFOEnvironment(t)
			t.Setenv("CFO_ROLE", test.role)
			t.Setenv("CFO_TASK_ID", test.task)
			t.Setenv("CFO_SPAWN_GEN", test.generation)
			setAncestorPID(t, os.Getpid())
			if test.isLocked {
				holder := os.Getpid()
				if test.isForeignHolder {
					holder = startLiveForeignProcess(t).Process.Pid
				}
				if _, err := lock.AcquireOwner(stateDirectory, holder, "cfo-thread"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lock.Release(stateDirectory) })
			}

			// Act
			runNativeCompact(t, "codex", "PreCompact", root)
			runNativeCompact(t, "codex", "PostCompact", root)

			// Assert
			if _, err := os.Stat(filepath.Join(stateDirectory, digest.CheckpointFile)); !os.IsNotExist(err) {
				t.Fatalf("a session without the home's custody wrote the CFO checkpoint: %v", err)
			}
			if pending, err := wake.Pending(stateDirectory); err != nil || len(pending) != 0 {
				t.Fatalf("a session without the home's custody woke the CFO: %+v, %v", pending, err)
			}
		})
	}
}

func TestNativeCompactWithoutACheckpointSaysSo(t *testing.T) {
	// Arrange
	root := newPrimaryHome(t)
	stateDirectory := filepath.Join(root, "state")
	clearNativeCFOEnvironment(t)
	setAncestorPID(t, os.Getpid())
	if _, err := lock.AcquireOwner(stateDirectory, os.Getpid(), "cfo-thread"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Release(stateDirectory) })

	// Act
	runNativeCompact(t, "codex", "PostCompact", root)

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
	if exit == 0 || !strings.Contains(diagnostics.String(), "PreCompact") {
		t.Fatalf("Claude PreCompact through the native entry: exit=%d %q, want an unsupported-event error", exit, diagnostics.String())
	}
}
