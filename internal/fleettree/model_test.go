package fleettree

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestChildModelComesFromItsOwnHarnessRecord(t *testing.T) {
	for _, kind := range []string{"claude", "codex"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			goblin := Goblin{Meta: state.TaskMeta{ID: "tree", Harness: kind, Model: "parent-model", Worktree: worktree}}
			var id string
			if kind == "claude" {
				var session string
				goblin, session = claudeGoblin(t, home,
					call("call1", "Agent", object{"subagent_type": "cfo-search", "model": "requested-model"}, at),
					result("call1", "Started", object{"isAsync": true, "agentId": "a1"}, at))
				writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at,
					object{"type": "assistant", "timestamp": at, "message": object{"model": "recorded-model", "content": []object{{"type": "text", "text": "OK"}}}})
				id = "subagent:call1"
			} else {
				day := filepath.Join(home, ".codex", "sessions", "2026", "10", "15")
				writeLines(t, filepath.Join(day, "rollout-root.jsonl"), at, sessionMeta("root", worktree, "", "", "", at))
				writeLines(t, filepath.Join(day, "rollout-child.jsonl"), at,
					sessionMeta("child", worktree, "root", "/root/search", "", at),
					codexEvent("task_started", at, nil),
					object{"type": "turn_context", "timestamp": at, "payload": object{"model": "recorded-model"}},
					codexEvent("task_complete", at, object{"last_agent_message": "OK"}))
				id = "subagent:child"
			}
			reader := Reader{Home: home, Now: func() time.Time { return at }}
			// Act
			tree, err := reader.Read(context.Background(), goblin)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(child(t, tree, id))
			if err != nil {
				t.Fatal(err)
			}
			var record struct {
				Model string `json:"model"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			// Assert
			if record.Model != "recorded-model" {
				t.Fatalf("child=%s, want its recorded model", data)
			}
		})
	}
}
