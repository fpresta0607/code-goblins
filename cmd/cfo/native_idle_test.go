package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// nativeCFOTerminal hosts native terminal cfo in the state at dir and records
// this test process as the program in it, as a real hook runs under the
// harness the terminal started.
func nativeCFOTerminal(t *testing.T, dir string) {
	t.Helper()
	shell, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(dir, host.Spec{ID: "cfo", Args: []string{shell, "/d", "/q", "/k"}, Cols: 80, Rows: 24})
	}()
	var record host.Record
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if record, err = host.ReadRecord(dir, "cfo"); err == nil {
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
		case <-time.After(15 * time.Second):
			t.Error("the terminal's host did not end")
		}
	})
	record.ChildPID = os.Getpid()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hosts", "cfo.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.IDVariable, "cfo")
	t.Setenv("HERDR_PANE_ID", "")
	for _, name := range []string{"CFO_ROLE", "CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
}

// A Codex or pi CFO has no Stop hook that reopens its turn, so its native
// hook raises the idle wake when its turn settles with no goblin at work
// while queued work waits and memory is free; the supervisor types it into
// the CFO's terminal, as it does every wake. A turn of a session that is not
// the registered CFO raises nothing.
func TestANativeCFOsIdleTurnRaisesTheIdleWake(t *testing.T) {
	cases := []struct {
		harness, start, settle string
		isRegistered           bool
	}{
		{harness: "codex", start: `{"hook_event_name":"SessionStart","session_id":"native-cfo"}`, settle: `{"hook_event_name":"Stop","session_id":"native-cfo"}`, isRegistered: true},
		{harness: "pi", start: `{"hook_event_name":"session_start","session_id":"native-cfo"}`, settle: `{"hook_event_name":"agent_settled","session_id":"native-cfo","pending_messages":false}`, isRegistered: true},
		{harness: "codex", settle: `{"hook_event_name":"Stop","session_id":"native-cfo"}`},
	}
	for _, testCase := range cases {
		name := testCase.harness
		if !testCase.isRegistered {
			name += " not registered"
		}
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			dir := filepath.Join(root, "state")
			nativeCFOTerminal(t, dir)
			queueWork(t, root)
			freeMemory(t, 8)
			hook := func(payload string) {
				t.Helper()
				event := map[string]any{}
				if err := json.Unmarshal([]byte(payload), &event); err != nil {
					t.Fatal(err)
				}
				event["cwd"] = root
				data, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				var out, errs bytes.Buffer
				if exit := runNativeHook([]string{testCase.harness, "--home", root, "--state", dir}, bytes.NewReader(data), &out, &errs, commandRuntime{}); exit != 0 {
					t.Fatalf("exit=%d %s", exit, errs.String())
				}
			}
			if testCase.isRegistered {
				hook(testCase.start)
			}

			// Act
			hook(testCase.settle)

			// Assert
			pending, err := wake.Pending(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !testCase.isRegistered {
				if len(pending) != 0 {
					t.Fatalf("wakes=%v, want none from a session that is not the CFO", pending)
				}
				return
			}
			if len(pending) != 1 || pending[0].Kind != "idle" || !strings.Contains(pending[0].Detail, "next: start next-task") {
				t.Fatalf("wakes=%v, want the idle wake naming next-task", pending)
			}
		})
	}
}
