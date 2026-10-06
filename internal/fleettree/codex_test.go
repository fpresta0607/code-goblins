package fleettree

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// sessionMeta is a Codex rollout's opening entry, as Codex 0.160 writes a
// goblin's own and a child agent's.
func sessionMeta(id, cwd, parent, agentPath, nickname string, when time.Time) object {
	payload := object{"id": id, "session_id": id, "cwd": cwd, "timestamp": when.Format(time.RFC3339Nano), "cli_version": "0.160.0"}
	if parent != "" {
		payload["parent_thread_id"] = parent
		payload["thread_source"] = "subagent"
		payload["agent_path"] = agentPath
		payload["agent_nickname"] = nickname
		payload["source"] = object{"subagent": object{"thread_spawn": object{"parent_thread_id": parent, "depth": 1, "agent_path": agentPath, "agent_nickname": nickname}}}
	}
	return object{"timestamp": when.Format(time.RFC3339Nano), "type": "session_meta", "payload": payload}
}

func codexEvent(kind string, when time.Time, fields object) object {
	payload := object{"type": kind}
	for key, value := range fields {
		payload[key] = value
	}
	return object{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg", "payload": payload}
}

func TestReadFindsCodexChildAgentsFromTheirOwnRollouts(t *testing.T) {
	// Arrange
	home := t.TempDir()
	worktree := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "06")
	start := at.Add(-time.Hour)
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-00-00-root.jsonl"), at.Add(-time.Minute),
		sessionMeta("root", worktree, "", "", "", start), codexEvent("task_started", at.Add(-time.Minute), nil))
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-10-00-c1.jsonl"), at.Add(-20*time.Minute),
		sessionMeta("c1", worktree, "root", "/root/review_hosted_producer", "Tesla", start.Add(10*time.Minute)),
		codexEvent("task_started", start.Add(10*time.Minute), nil),
		codexEvent("task_complete", at.Add(-20*time.Minute), object{"last_agent_message": "No Critical findings.\n\nDetails follow."}))
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-20-00-c2.jsonl"), at.Add(-2*time.Minute),
		sessionMeta("c2", worktree, "root", "/root/map_monitor", "Curie", start.Add(20*time.Minute)),
		codexEvent("task_started", start.Add(20*time.Minute), nil),
		object{"timestamp": at.Add(-2 * time.Minute).Format(time.RFC3339Nano), "type": "response_item", "payload": object{"type": "reasoning"}})
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-30-00-x9.jsonl"), at,
		sessionMeta("x9", worktree, "elsewhere", "/root/other", "Noether", start.Add(30*time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex", Backend: "native", Worktree: worktree}}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	if len(tree.Children) != 2 {
		t.Fatalf("children = %+v, want the two agents the goblin's thread spawned, and never its own rollout or another thread's agent", tree.Children)
	}
	done := child(t, tree, "subagent:c1")
	if done.State != Done || done.Label != "review hosted producer" || done.Detail != "Tesla" || done.LastLine != "No Critical findings." || !done.Finished.Equal(at.Add(-20*time.Minute)) {
		t.Errorf("finished child agent = %+v", done)
	}
	working := child(t, tree, "subagent:c2")
	if working.State != Working || !working.LastActivity.Equal(at.Add(-2*time.Minute)) {
		t.Errorf("working child agent = %+v, want working, active at its last entry", working)
	}
	if !tree.ConversationAt.Equal(at.Add(-time.Minute)) {
		t.Errorf("ConversationAt = %v, want the goblin's own rollout, not a child's", tree.ConversationAt)
	}
}

// pi 0.85 records no sub-agents and no background jobs: its tree shows none
// and guesses none.
func TestReadShowsNoChildrenForPi(t *testing.T) {
	// Arrange
	home := t.TempDir()
	writeLines(t, filepath.Join(home, ".pi", "agent", "sessions", "--C--work--", "2026-10-06T15-00-00-000Z_p1.jsonl"), at,
		object{"type": "session", "id": "p1", "timestamp": at.Add(-time.Hour).Format(time.RFC3339Nano)},
		object{"type": "message", "timestamp": at.Add(-time.Minute).Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{{"type": "toolCall", "name": "bash", "arguments": object{"command": "go test ./..."}}}}})
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "pi"}, Session: "p1"})

	// Assert
	if len(tree.Children) != 0 || !tree.ConversationAt.Equal(at) {
		t.Errorf("pi tree = %+v, want no children and its session's last write", tree)
	}
}

// The goblin's conversation is the one its own harness process records, or
// the board recorded for its generation: never the newest in its folder,
// where a forked CFO session worked on 2026-10-06.
func TestReadTakesOnlyTheConversationItsHarnessOrTheBoardProves(t *testing.T) {
	home := t.TempDir()
	folder := filepath.Join(home, ".claude", "projects", "C--work-gb-tree")
	writeLines(t, filepath.Join(folder, "own-session.jsonl"), at.Add(-time.Hour),
		call("toolu_A", "Agent", object{"description": "Own agent"}, at.Add(-time.Hour)))
	writeLines(t, filepath.Join(folder, "forked-cfo.jsonl"), at,
		call("toolu_X", "Agent", object{"description": "Someone else's agent"}, at))
	harness := Process{PID: 4242, ParentPID: 1, Exe: "claude.exe", Created: 134357962423340710, Started: filetime(134357962423340710)}
	processes := func() ([]Process, error) { return []Process{harness}, nil }
	meta := state.TaskMeta{ID: "tree", Harness: "claude", SpawnGen: "s1"}
	for name, test := range map[string]struct {
		record   string
		recorded func(state.TaskMeta) string
		want     string
	}{
		"its process's record":           {record: `{"pid":4242,"sessionId":"own-session","procStart":"134357962423340710"}`, want: "subagent:toolu_A"},
		"an earlier process with its id": {record: `{"pid":4242,"sessionId":"own-session","procStart":"134357960000000000"}`},
		"the board's record":             {recorded: func(state.TaskMeta) string { return "own-session" }, want: "subagent:toolu_A"},
		"no record at all":               {},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			sessions := filepath.Join(home, ".claude", "sessions", "4242.json")
			writeFile(t, sessions, test.record, at)
			reader := Reader{Home: home, Processes: processes, Recorded: test.recorded, Now: func() time.Time { return at }}

			// Act
			tree, _ := reader.Read(context.Background(), Goblin{Meta: meta, HarnessPID: 4242})

			// Assert
			if test.want == "" {
				if len(tree.Children) != 0 || len(tree.Unread) == 0 {
					t.Errorf("tree = %+v, want no conversation taken and that said in Unread", tree)
				}
				return
			}
			if len(tree.Children) != 1 || tree.Children[0].ID != test.want {
				t.Errorf("children = %+v, want only %s from the goblin's own conversation", tree.Children, test.want)
			}
		})
	}
}
