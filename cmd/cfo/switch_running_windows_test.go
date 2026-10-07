package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runningClaudeGoblin records task-7 as a native Claude Code goblin whose
// terminal runs this test's own process, and has that process record its
// conversation as Claude Code 2.1 does, in a user home of its own, which no
// native session hook ever told the board of.
func runningClaudeGoblin(t *testing.T, h home.Home, session string) state.TaskMeta {
	t.Helper()
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	processes, err := fleettree.Processes()
	if err != nil {
		t.Fatal(err)
	}
	var self fleettree.Process
	for _, process := range processes {
		if process.PID == os.Getpid() {
			self = process
		}
	}
	if self.PID == 0 {
		t.Fatal("this test's own process is not among the running processes")
	}
	record, err := json.Marshal(map[string]any{"pid": self.PID, "sessionId": session, "procStart": strconv.FormatInt(self.Created, 10)})
	if err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Join(userHome, ".claude", "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessions, strconv.Itoa(self.PID)+".json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := json.Marshal(map[string]any{"id": "task-7", "pipe": `\\.\pipe\stand-in`, "token": "stand-in", "version": 1, "host_pid": self.PID, "child_pid": self.PID, "child_start": self.Started, "started": time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "hosts", "task-7.json"), host, 0o600); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "task-7", Harness: "claude", Model: "opus", Effort: "xhigh", Backend: "native", SpawnGen: "s1"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// A switch that keeps a running goblin's harness resumes the conversation
// its harness records for itself when the board recorded none, as a home
// without native session hooks records none: a model change and a restart
// onto a harness update alike, the restart keeping the goblin's own values.
func TestSwitchResumesTheConversationTheRunningGoblinRecords(t *testing.T) {
	for name, args := range map[string][]string{
		"a model change":           {"--model", "sonnet"},
		"a restart onto an update": {"--restart"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			if err := os.MkdirAll(h.State, 0o755); err != nil {
				t.Fatal(err)
			}
			meta := runningClaudeGoblin(t, h, "own-session-7")
			deps := testCommandRuntimeForHome(h)
			var received spawn.SwitchRequest
			deps.switchTask = func(_ context.Context, _ home.Home, request spawn.SwitchRequest) (spawn.SwitchResult, error) {
				received = request
				return spawn.SwitchResult{Output: "switched task-7"}, nil
			}
			deps.speedHint = func(context.Context, string) string { return "" }
			var stdout, stderr bytes.Buffer

			// Act
			exit := runWithRuntime(append([]string{"switch", meta.ID}, args...), &stdout, &stderr, deps)

			// Assert
			if exit != 0 || received.ID != meta.ID || received.ResumeSession != "own-session-7" {
				t.Fatalf("exit=%d request=%+v stderr=%s; want own-session-7 resumed", exit, received, stderr.String())
			}
			if isRestart := args[0] == "--restart"; received.Restart != isRestart || isRestart && (received.Harness != "" || received.Model != "" || received.Effort != "") {
				t.Errorf("request = %+v; want a restart only when asked, keeping the goblin's own values", received)
			}
		})
	}
}
