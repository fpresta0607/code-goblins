package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/digest"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// nativeCFOTerminal hosts native terminal cfo in the state at dir and records
// this test process as the program in it, as a real hook runs under the
// harness the terminal started.
func nativeCFOTerminal(t *testing.T, dir string) {
	t.Helper()
	hostThisProcess(t, dir, "cfo")
	t.Setenv(host.IDVariable, "cfo")
	t.Setenv("HERDR_PANE_ID", "")
	for _, name := range []string{"CFO_ROLE", "CFO_TASK_ID", "CFO_SPAWN_GEN", "CFO_PARENT_SESSION_ID", "CFO_PARENT_HARNESS", "CFO_ROOT_SESSION_ID"} {
		t.Setenv(name, "")
	}
}

// hostThisProcess hosts native terminal id in the state at dir and records
// this test process as the program in it, until the test ends.
func hostThisProcess(t *testing.T, dir, id string) {
	t.Helper()
	shell, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		_ = host.Run(dir, host.Spec{ID: id, Args: []string{shell, "/d", "/q", "/k"}, Cols: 80, Rows: 24})
	}()
	var record host.Record
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(10 * time.Millisecond) {
		if record, err = host.ReadRecord(dir, id); err == nil {
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
	hosttest.Rewrite(t, dir, id, func(record *host.Record) { programIsThisProcess(t, record) })
}

// A registered Codex or pi CFO's native hook spools each event and returns,
// as a Claude Code session's does: its turn end and its compaction raise no
// wake and write no checkpoint inside the harness's wait, while work waits,
// memory is free and the CFO holds the primary home, since the supervisor
// does that as it ingests them. Only its SessionStart still registers it,
// which only a process in its own session can (issue #1213).
func TestARegisteredNativeCFOsHookOnlySpoolsItsTurnEndAndCompaction(t *testing.T) {
	cases := []struct {
		harness, start string
		later          []string
	}{
		{harness: "codex", start: "SessionStart", later: []string{"Stop", "PreCompact", "PostCompact"}},
		{harness: "pi", start: "session_start", later: []string{"agent_settled", "session_before_compact", "session_compact"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.harness, func(t *testing.T) {
			// Arrange
			root := newPrimaryHome(t)
			dir := filepath.Join(root, "state")
			nativeCFOTerminal(t, dir)
			queueWork(t, root)
			freeMemory(t, 8)
			hook := func(name string) {
				t.Helper()
				data, err := json.Marshal(map[string]string{"hook_event_name": name, "session_id": "native-cfo", "cwd": root})
				if err != nil {
					t.Fatal(err)
				}
				var out, errs bytes.Buffer
				if exit := runNativeHook([]string{testCase.harness, "--home", root, "--state", dir}, bytes.NewReader(data), &out, &errs, commandRuntime{}); exit != 0 || strings.TrimSpace(out.String()) != "{}" {
					t.Fatalf("%s exit=%d reply=%q %s", name, exit, out.String(), errs.String())
				}
			}
			hook(testCase.start)
			if !supervisor.RunsUnderRegisteredCFO(dir) {
				t.Fatal("the CFO's SessionStart did not register it, so the hooks after it prove nothing")
			}

			// Act
			for _, name := range testCase.later {
				hook(name)
			}

			// Assert
			if pending, err := wake.Pending(dir); err != nil || len(pending) != 0 {
				t.Fatalf("wakes=%+v, %v; want none raised inside the harness's wait", pending, err)
			}
			if _, err := os.Stat(filepath.Join(dir, digest.CheckpointFile)); !os.IsNotExist(err) {
				t.Fatalf("the hook wrote the compact checkpoint inside the harness's wait: %v", err)
			}
			entries, err := os.ReadDir(nativehook.SpoolDir(dir))
			if err != nil {
				t.Fatal(err)
			}
			var kinds []string
			for _, entry := range entries {
				data, err := os.ReadFile(filepath.Join(nativehook.SpoolDir(dir), entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				var event nativehook.Event
				if err := json.Unmarshal(data, &event); err != nil {
					t.Fatal(err)
				}
				kinds = append(kinds, event.Kind)
			}
			slices.Sort(kinds)
			if want := []string{"compacted", "compacting", "settled", "started"}; !slices.Equal(kinds, want) {
				t.Fatalf("spooled %v, want %v for the supervisor to ingest", kinds, want)
			}
		})
	}
}
