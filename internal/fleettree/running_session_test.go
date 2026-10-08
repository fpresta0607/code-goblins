package fleettree

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A running goblin's conversation is the one its harness records for itself
// when the board recorded none: Claude Code's record of its own process,
// proved by the process's creation time, and the Codex rollout of its
// worktree written last, never a child agent's. Without a running harness, or
// without that proof, there is none to name.
func TestRunningSessionIsTheConversationTheRunningHarnessRecords(t *testing.T) {
	home, worktree := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"own-session","procStart":"134357962423340710"}`, at)
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "07")
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-00-00-root.jsonl"), at.Add(-time.Minute), sessionMeta("root", worktree, "", "", "", at.Add(-time.Hour)))
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-10-00-c1.jsonl"), at, sessionMeta("c1", worktree, "root", "/root/review", "Tesla", at.Add(-30*time.Minute)))
	claude := Process{PID: 4242, ParentPID: 1, Exe: "claude.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	codex := Process{PID: 5151, ParentPID: 1, Exe: "codex.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	for name, test := range map[string]struct {
		meta state.TaskMeta
		pid  int
		want string
	}{
		"claude's own record":        {state.TaskMeta{ID: "tree", Harness: "claude"}, 4242, "own-session"},
		"codex's own rollout":        {state.TaskMeta{ID: "tree", Harness: "codex", Worktree: worktree}, 5151, "root"},
		"a harness that is not up":   {state.TaskMeta{ID: "tree", Harness: "claude"}, 0, ""},
		"a process another runs now": {state.TaskMeta{ID: "tree", Harness: "claude"}, 6262, ""},
		"pi, which records none":     {state.TaskMeta{ID: "tree", Harness: "pi"}, 4242, ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			reader := Reader{Home: home, Processes: func() ([]Process, error) { return []Process{claude, codex}, nil }, Now: func() time.Time { return at }}

			// Act
			session := reader.RunningSession(context.Background(), Goblin{Meta: test.meta, HarnessPID: test.pid})

			// Assert
			if session != test.want {
				t.Errorf("RunningSession = %q, want %q", session, test.want)
			}
		})
	}
}
