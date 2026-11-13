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
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
)

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
