package fleettree

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A running goblin's conversation is the one its harness records for itself
// when the board recorded none: Claude Code's record of its own process,
// proved by the process's creation time, and the Codex rollout of its
// worktree written last, never a child agent's, and the pi session of its
// worktree written last since pi started, never one an earlier pi wrote while
// this one has written none yet. Without a running harness, or without that
// proof, there is none to name.
func TestRunningSessionIsTheConversationTheRunningHarnessRecords(t *testing.T) {
	home, worktree := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "sessions", "4242.json"), `{"pid":4242,"sessionId":"own-session","procStart":"134357962423340710"}`, at)
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "07")
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-00-00-root.jsonl"), at.Add(-time.Minute), sessionMeta("root", worktree, "", "", "", at.Add(-time.Hour)))
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-10-00-c1.jsonl"), at, sessionMeta("c1", worktree, "root", "/root/review", "Tesla", at.Add(-30*time.Minute)))
	piSessions := filepath.Join(home, ".pi", "agent", "sessions", "--"+strings.NewReplacer(`\`, "-", "/", "-", ":", "-").Replace(worktree)+"--")
	writeLines(t, filepath.Join(piSessions, "2026-10-07T14-00-00-000Z_01a1-older.jsonl"), at.Add(-time.Hour), object{"type": "session"})
	writeLines(t, filepath.Join(piSessions, "2026-10-07T15-00-00-000Z_01a1-newest.jsonl"), at, object{"type": "session"})
	claude := Process{PID: 4242, ParentPID: 1, Exe: "claude.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	codex := Process{PID: 5151, ParentPID: 1, Exe: "codex.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	pi := Process{PID: 7373, ParentPID: 1, Exe: "node.exe", Started: at.Add(-30 * time.Minute)}
	laterPi := Process{PID: 7474, ParentPID: 1, Exe: "node.exe", Started: at.Add(time.Minute)}
	for name, test := range map[string]struct {
		meta state.TaskMeta
		pid  int
		want string
	}{
		"claude's own record":           {state.TaskMeta{ID: "tree", Harness: "claude"}, 4242, "own-session"},
		"codex's own rollout":           {state.TaskMeta{ID: "tree", Harness: "codex", Worktree: worktree}, 5151, "root"},
		"a harness that is not up":      {state.TaskMeta{ID: "tree", Harness: "claude"}, 0, ""},
		"a process another runs now":    {state.TaskMeta{ID: "tree", Harness: "claude"}, 6262, ""},
		"pi's newest session":           {state.TaskMeta{ID: "tree", Harness: "pi", Worktree: worktree}, 7373, "01a1-newest"},
		"a session an earlier pi wrote": {state.TaskMeta{ID: "tree", Harness: "pi", Worktree: worktree}, 7474, ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			reader := Reader{Home: home, Processes: func() ([]Process, error) { return []Process{claude, codex, pi, laterPi}, nil }, Now: func() time.Time { return at }}

			// Act
			session := reader.RunningSession(context.Background(), Goblin{Meta: test.meta, HarnessPID: test.pid})

			// Assert
			if session != test.want {
				t.Errorf("RunningSession = %q, want %q", session, test.want)
			}
		})
	}
}

// A running pi goblin whose terminal and board name no session is in its
// worktree's newest pi session only once that session was written since pi
// started: pi writes a session only when its first reply ends, so until then
// the newest is an earlier pi's, whose last question is not this goblin's
// and whose last write is not its activity.
func TestAPiGoblinsConversationIsNeverOneAnEarlierPiWrote(t *testing.T) {
	home, worktree := t.TempDir(), t.TempDir()
	newest := filepath.Join(home, ".pi", "agent", "sessions", "--"+strings.NewReplacer(`\`, "-", "/", "-", ":", "-").Replace(worktree)+"--", "2026-10-07T15-00-00-000Z_01a1-newest.jsonl")
	writeLines(t, newest, at, object{"type": "session"})
	for name, test := range map[string]struct {
		started time.Time
		want    string
		wantAt  time.Time
	}{
		"one this pi wrote":       {at.Add(-30 * time.Minute), newest, at},
		"one an earlier pi wrote": {at.Add(time.Minute), "", time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pi := Process{PID: 7373, ParentPID: 1, Exe: "node.exe", Started: test.started}
			reader := Reader{Home: home, Processes: func() ([]Process, error) { return []Process{pi}, nil }, Listeners: func() (map[int][]int, error) { return nil, nil }, Now: func() time.Time { return at }}
			goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "pi", Worktree: worktree}, HarnessPID: 7373}

			// Act
			path := reader.Conversation(context.Background(), goblin)
			tree, _ := reader.Read(context.Background(), goblin)

			// Assert
			if path != test.want || !tree.ConversationAt.Equal(test.wantAt) {
				t.Errorf("conversation = %q written %v, want %q written %v", path, tree.ConversationAt, test.want, test.wantAt)
			}
		})
	}
}

// A goblin's conversation is the thread it runs at its prompt, never one a
// codex exec it ran in its worktree wrote later, nor a child agent's whose
// source names its spawning in a shape this reader does not know: the entry
// was read whole, so that rollout is read, and it is a child's.
func TestRunningSessionIsNeverACodexExecOrAChildOfAnUnknownShape(t *testing.T) {
	// Arrange
	home, worktree := t.TempDir(), t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "07")
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-00-00-root.jsonl"), at.Add(-time.Hour), sessionMeta("root", worktree, "", "", "", at.Add(-2*time.Hour)))
	exec := sessionMeta("exec1", worktree, "", "", "", at.Add(-30*time.Minute))
	exec["payload"].(object)["source"] = "exec"
	writeLines(t, filepath.Join(day, "rollout-2026-10-07T15-30-00-exec1.jsonl"), at.Add(-time.Minute), exec)
	review := sessionMeta("review1", worktree, "", "", "", at.Add(-20*time.Minute))
	review["payload"].(object)["source"] = object{"subagent": "review"}
	path := filepath.Join(day, "rollout-2026-10-07T15-40-00-review1.jsonl")
	writeLines(t, path, at, review)
	codex := Process{PID: 5151, ParentPID: 1, Exe: "codex.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	reader := Reader{Home: home, Processes: func() ([]Process, error) { return []Process{codex}, nil }, Now: func() time.Time { return at }}

	// Act
	session := reader.RunningSession(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex", Worktree: worktree}, HarnessPID: 5151})
	child := readRolloutMeta(path)

	// Assert
	if session != "root" {
		t.Errorf("RunningSession = %q, want the goblin's own thread root", session)
	}
	if !child.read || !child.subagent {
		t.Errorf("a child whose source is {\"subagent\": \"review\"} reads as %+v, want it read and a child agent's", child)
	}
}
