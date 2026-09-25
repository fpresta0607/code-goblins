package main

import (
	"bytes"
	"encoding/json"
	"github.com/fpresta0607/code-goblins/internal/host"
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
	t.Setenv("CFO_ROLE", "goblin")
	t.Setenv("CFO_TASK_ID", "fixture")
	t.Setenv("CFO_SPAWN_GEN", "g1")
	t.Setenv("CFO_PARENT_SESSION_ID", "")
	t.Setenv("CFO_PARENT_HARNESS", "")
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

// A Codex CFO's SessionStart in a native terminal registers that terminal, as
// it registers a Herdr pane.
func TestACodexCFOStartingInANativeTerminalRegisters(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
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
	// The hook runs in this test process, so the record names this process as
	// the terminal's program, as it names the harness a real hook runs under.
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
	payload, err := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": "native-cfo", "cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer

	exit := runNativeHook([]string{"codex", "--home", root, "--state", dir}, bytes.NewReader(payload), &out, &errs, commandRuntime{})

	if exit != 0 {
		t.Fatalf("exit=%d %s", exit, errs.String())
	}
	data, err = os.ReadFile(filepath.Join(dir, "primary.json"))
	if err != nil || !strings.Contains(string(data), `"host":"cfo"`) || !strings.Contains(string(data), `"agent":"codex"`) {
		t.Fatalf("primary.json = %s, %v; want native terminal cfo registered for codex", data, err)
	}
}
