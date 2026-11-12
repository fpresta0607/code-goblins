package main

import (
	"bytes"
	"encoding/json"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeHookEntrySpoolsIdentityWithoutPayloadSecrets(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	t.Setenv("GOBLINS_ROLE", "goblin")
	t.Setenv("GOBLINS_TASK_ID", "fixture")
	t.Setenv("GOBLINS_SPAWN_GEN", "g1")
	t.Setenv("GOBLINS_PARENT_SESSION_ID", "")
	t.Setenv("GOBLINS_PARENT_HARNESS", "")
	payload, _ := json.Marshal(map[string]string{"hook_event_name": "Stop", "session_id": "native-session", "turn_id": "t1", "cwd": root, "prompt": "secret-should-not-persist"})
	var out, errs bytes.Buffer
	if exit := runNativeHook([]string{"codex", "--home", root, "--state", dir}, bytes.NewReader(payload), &out, &errs, commandRuntime{}); exit != 0 {
		t.Fatalf("exit=%d %s", exit, errs.String())
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatal(out.String())
	}
	files, err := os.ReadDir(nativehook.SpoolDir(dir))
	if err != nil || len(files) != 1 {
		t.Fatalf("spool %v %v", files, err)
	}
	data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event nativehook.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.TaskID != "fixture" || event.Kind != "settled" || event.Generation != "g1" || bytes.Contains(data, []byte("secret-should")) {
		t.Fatalf("wrong event %s", data)
	}
}

// Claude Code adds what a PreCompact hook prints to the instructions of its
// summary, so the native hook reports that compaction and prints nothing into
// it.
func TestTheClaudeHookReportsACompactionAndPrintsNothingIntoItsSummary(t *testing.T) {
	for _, test := range []struct{ event, kind, printed string }{
		{"PreCompact", "compacting", ""},
		{"PostCompact", "compacted", "{}"},
	} {
		t.Run(test.event, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			dir := filepath.Join(root, "state")
			t.Setenv("GOBLINS_ROLE", "goblin")
			t.Setenv("GOBLINS_TASK_ID", "fixture")
			t.Setenv("GOBLINS_SPAWN_GEN", "g1")
			t.Setenv("GOBLINS_PARENT_SESSION_ID", "")
			t.Setenv("GOBLINS_PARENT_HARNESS", "")
			payload, err := json.Marshal(map[string]string{"hook_event_name": test.event, "session_id": "native-session", "cwd": root, "trigger": "auto"})
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer

			// Act
			exit := runNativeHook([]string{"claude", "--home", root, "--state", dir}, bytes.NewReader(payload), &out, &errs, commandRuntime{})

			// Assert
			if exit != 0 || strings.TrimSpace(out.String()) != test.printed {
				t.Fatalf("exit=%d printed %q (%s), want it to print %q", exit, out.String(), errs.String(), test.printed)
			}
			files, err := os.ReadDir(nativehook.SpoolDir(dir))
			if err != nil || len(files) != 1 {
				t.Fatalf("spool %v %v", files, err)
			}
			data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			var event nativehook.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind != test.kind {
				t.Fatalf("spooled %s, want a %s event", data, test.kind)
			}
		})
	}
}

// Claude Code raises SubagentStart only as its Agent tool starts a subagent,
// with that agent's type, and SubagentStop as any agent ends, its own forked
// agents with no type among them. The supervisor never accepts a stop no
// start preceded, so the hook spools none for an untyped agent and still
// spools a typed subagent's (issue #1212).
func TestTheClaudeHookSpoolsASubagentStopOnlyForASubagentItStarted(t *testing.T) {
	for _, test := range []struct {
		name, agentType string
		isSpooled       bool
	}{
		{"Claude Code's own agent", "", false},
		{"an Agent tool subagent", "Explore", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			dir := filepath.Join(root, "state")
			clearNativeCEOEnvironment(t)
			t.Setenv("GOBLINS_ROLE", "goblin")
			t.Setenv("GOBLINS_TASK_ID", "fixture")
			t.Setenv("GOBLINS_SPAWN_GEN", "g1")
			payload, err := json.Marshal(map[string]any{"hook_event_name": "SubagentStop", "session_id": "parent-session", "agent_id": "a0123456789abcdef", "agent_type": test.agentType, "stop_hook_active": false, "cwd": root})
			if err != nil {
				t.Fatal(err)
			}
			var output, diagnostics bytes.Buffer

			// Act
			exit := runNativeHook([]string{"claude", "--home", root, "--state", dir}, bytes.NewReader(payload), &output, &diagnostics, commandRuntime{})

			// Assert
			if exit != 0 || strings.TrimSpace(output.String()) != "{}" {
				t.Fatalf("exit=%d output=%q diagnostics=%q, want {} and exit 0", exit, output.String(), diagnostics.String())
			}
			files, err := os.ReadDir(nativehook.SpoolDir(dir))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if !test.isSpooled {
				if len(files) != 0 {
					t.Fatalf("spooled %d events for a stop no start preceded, want none", len(files))
				}
				return
			}
			if len(files) != 1 {
				t.Fatalf("spooled %d events for a typed subagent's stop, want one", len(files))
			}
			data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), files[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			var event nativehook.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			if event.Kind != "settled" || event.Role != "subagent" || event.SessionID != "a0123456789abcdef" || event.ParentSessionID != "parent-session" || event.AgentType != test.agentType {
				t.Fatalf("wrong event %s", data)
			}
		})
	}
}

// A Codex CEO's SessionStart in a native terminal registers that terminal, as
// it registers a Herdr pane.
func TestACodexCEOStartingInANativeTerminalRegisters(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	shell, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(dir, host.Spec{ID: "ceo", Args: []string{shell, "/d", "/q", "/k"}, Cols: 80, Rows: 24})
	}()
	var record host.Record
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if record, err = host.ReadRecord(dir, "ceo"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the host never recorded itself")
		}
	}
	t.Cleanup(func() {
		if client, err := host.Dial(record); err == nil {
			_ = client.CloseTerminal()
			_ = client.Close()
		}
		select {
		case <-ended:
		case <-time.After(time.Minute):
			t.Error("the terminal's host did not end")
		}
	})
	// The hook runs in this test process, so the record names this process as
	// the terminal's program, as it names the harness a real hook runs under.
	hosttest.Rewrite(t, dir, "ceo", func(record *host.Record) { programIsThisProcess(t, record) })
	t.Setenv(host.IDVariable, "ceo")
	t.Setenv("HERDR_PANE_ID", "")
	for _, name := range []string{"GOBLINS_ROLE", "GOBLINS_TASK_ID", "GOBLINS_SPAWN_GEN", "GOBLINS_PARENT_SESSION_ID", "GOBLINS_PARENT_HARNESS", "GOBLINS_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
	payload, err := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "native-ceo", "cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer

	exit := runNativeHook([]string{"codex", "--home", root, "--state", dir}, bytes.NewReader(payload), &out, &errs, commandRuntime{})

	if exit != 0 {
		t.Fatalf("exit=%d %s", exit, errs.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "primary.json"))
	if err != nil || !strings.Contains(string(data), `"host":"ceo"`) || !strings.Contains(string(data), `"agent":"codex"`) {
		t.Fatalf("primary.json = %s, %v; want native terminal ceo registered for codex", data, err)
	}
}
