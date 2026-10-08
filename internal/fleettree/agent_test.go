package fleettree

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A sub-agent's task is the prompt it was given, on one line, which the board
// shows when the Overlord rests the pointer on its baby goblin.
func TestReadGivesASubagentItsPromptAsItsTask(t *testing.T) {
	// Arrange
	home := t.TempDir()
	prompt := "Find where the harness starts sub-agents\n\nand what   each one records." + strings.Repeat(" More.", 100)
	goblin, _ := claudeGoblin(t, home, call("toolu_A", "Agent", object{"description": "Map harness plumbing", "subagent_type": "Explore", "prompt": prompt}, at.Add(-time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	agent := child(t, tree, "subagent:toolu_A")
	if !strings.HasPrefix(agent.Task, "Find where the harness starts sub-agents and what each one records. More.") || len([]rune(agent.Task)) > 300 || strings.Contains(agent.Task, "\n") {
		t.Errorf("sub-agent task = %q, want its prompt on one line, bounded", agent.Task)
	}
}

// The board opens a sub-agent's own record as its terminal: Claude Code's
// transcript of that sub-agent, found by the id its result named or by the
// .meta.json beside it, and only for a sub-agent of that goblin's tree.
func TestAgentTranscriptIsTheSubagentsOwnRecord(t *testing.T) {
	// Arrange
	home := t.TempDir()
	goblin, session := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Map harness plumbing", "prompt": "map it"}, at.Add(-5*time.Minute)),
		result("toolu_A", "Async agent launched successfully.", object{"status": "async_launched", "agentId": "a1"}, at.Add(-5*time.Minute)),
		call("toolu_B", "Task", object{"description": "Find specs", "prompt": "find them"}, at.Add(-4*time.Minute)),
		call("toolu_C", "Agent", object{"description": "Escape", "prompt": "x"}, at.Add(-4*time.Minute)),
		result("toolu_C", "Async agent launched successfully.", object{"status": "async_launched", "agentId": `..\..\secrets`}, at.Add(-4*time.Minute)),
		call("toolu_S", "Bash", object{"command": "go test ./...", "run_in_background": true}, at.Add(-3*time.Minute)),
		result("toolu_S", "Command running in background with ID: b1", object{"backgroundTaskId": "b1"}, at.Add(-3*time.Minute)),
	)
	writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at, said("reading", at))
	writeFile(t, filepath.Join(session, "subagents", "agent-a9.meta.json"), `{"agentType":"Explore","description":"Find specs","toolUseId":"toolu_B"}`, at)
	writeLines(t, filepath.Join(session, "subagents", "agent-a9.jsonl"), at, said("finding", at))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ task, node, want string }{
		{"tree", "subagent:toolu_A", filepath.Join(session, "subagents", "agent-a1.jsonl")},
		{"tree", "subagent:toolu_B", filepath.Join(session, "subagents", "agent-a9.jsonl")},
		{"tree", "subagent:toolu_C", ""},
		{"tree", "shell:b1", ""},
		{"tree", "subagent:toolu_X", ""},
		{"other", "subagent:toolu_A", ""},
	} {
		t.Run(tc.task+" "+tc.node, func(t *testing.T) {
			// Act
			path, harness := reader.AgentTranscript(tc.task, tc.node)

			// Assert
			if path != tc.want || (path != "" && harness != "claude") {
				t.Errorf("AgentTranscript = %q, %q; want %q", path, harness, tc.want)
			}
		})
	}
}

// A Codex child agent's own record is its rollout.
func TestAgentTranscriptOfACodexChildIsItsRollout(t *testing.T) {
	// Arrange
	home := t.TempDir()
	worktree := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "06")
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-00-00-root.jsonl"), at, sessionMeta("root", worktree, "", "", "", at.Add(-time.Hour)))
	rollout := filepath.Join(day, "rollout-2026-10-06T15-10-00-c1.jsonl")
	writeLines(t, rollout, at, sessionMeta("c1", worktree, "root", "/root/map_monitor", "Curie", at.Add(-time.Hour)), codexEvent("task_started", at, nil))
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-20-00-x9.jsonl"), at, sessionMeta("x9", worktree, "elsewhere", "/root/other", "Noether", at.Add(-time.Hour)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	if _, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex", Backend: "native", Worktree: worktree}}); err != nil {
		t.Fatal(err)
	}

	// Act
	path, harness := reader.AgentTranscript("tree", "subagent:c1")
	other, _ := reader.AgentTranscript("tree", "subagent:x9")

	// Assert
	if path != rollout || harness != "codex" || other != "" {
		t.Errorf("AgentTranscript = %q, %q and another thread's agent %q; want the child's own rollout only", path, harness, other)
	}
}

// What a dead goblin's reader kept is forgotten with it.
func TestForgetDropsAGoblinsAgentTranscripts(t *testing.T) {
	// Arrange
	home := t.TempDir()
	goblin, session := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Map", "prompt": "map it"}, at),
		result("toolu_A", "launched", object{"status": "async_launched", "agentId": "a1"}, at))
	writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at, said("reading", at))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	if _, err := reader.Read(context.Background(), goblin); err != nil {
		t.Fatal(err)
	}

	// Act
	reader.Forget(map[string]bool{})

	// Assert
	if path, _ := reader.AgentTranscript("tree", "subagent:toolu_A"); path != "" {
		t.Errorf("AgentTranscript after Forget = %q, want none", path)
	}
}

// A sub-agent's terminal shows its work as Claude Code draws it: the task it
// was given, what it said, each tool it called with what it called it on,
// and the first line of each result, an error marked.
func TestAgentLinesShowAClaudeSubagentsWork(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	writeLines(t, path, at,
		object{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "user", "content": "Find where the harness\nstarts sub-agents"}},
		object{"type": "assistant", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{
			{"type": "thinking", "thinking": "private"},
			{"type": "text", "text": "I will search.\nThen read."},
			{"type": "tool_use", "id": "t1", "name": "Grep", "input": object{"pattern": "subagents/agent-"}},
		}}},
		result("t1", "Found 3 files\ninternal/a.go\ninternal/b.go", nil, at),
		call("t2", "Read", object{"file_path": `C:\work\internal\fleettree\claude.go`}, at),
		object{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "user", "content": []object{{"type": "tool_result", "tool_use_id": "t2", "is_error": true, "content": []object{{"type": "text", "text": "File does not exist."}}}}}},
		call("t3", "Bash", object{"command": "go test ./internal/fleettree/\necho done", "description": "Run the tree tests"}, at),
		`{"type":"assistant","message":`,
	)

	// Act
	lines, err := AgentLines(path, "claude")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"> Find where the harness",
		"  starts sub-agents",
		"● I will search.",
		"  Then read.",
		"● Grep(subagents/agent-)",
		"  ⎿ Found 3 files",
		"     … 2 more lines",
		`● Read(C:\work\internal\fleettree\claude.go)`,
		"  ⎿ Error: File does not exist.",
		"● Bash(Run the tree tests)",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// A Codex child's terminal shows the task it was given, its messages, its
// tool calls and the first line of their output.
func TestAgentLinesShowACodexChildsWork(t *testing.T) {
	// Arrange
	item := func(payload object) object {
		return object{"timestamp": at.Format(time.RFC3339Nano), "type": "response_item", "payload": payload}
	}
	path := filepath.Join(t.TempDir(), "rollout-c1.jsonl")
	writeLines(t, path, at,
		sessionMeta("c1", `C:\work`, "root", "/root/map_monitor", "Curie", at),
		item(object{"type": "message", "role": "developer", "content": []object{{"type": "input_text", "text": "rules"}}}),
		item(object{"type": "message", "role": "user", "content": []object{{"type": "input_text", "text": "Map the monitor"}}}),
		codexEvent("task_started", at, nil),
		item(object{"type": "reasoning"}),
		item(object{"type": "function_call", "name": "shell", "arguments": `{"command":["rg","-n","stall"]}`, "call_id": "c"}),
		item(object{"type": "function_call_output", "call_id": "c", "output": "internal/monitor/stall.go:12\ninternal/monitor/stall.go:40"}),
		item(object{"type": "message", "role": "assistant", "content": []object{{"type": "output_text", "text": "The stall rule is in stall.go."}}}),
	)

	// Act
	lines, err := AgentLines(path, "codex")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"> Map the monitor",
		`● shell({"command":["rg","-n","stall"]})`,
		"  ⎿ internal/monitor/stall.go:12",
		"     … 1 more line",
		"● The stall rule is in stall.go.",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// A long-running sub-agent's terminal holds its newest lines, each bounded.
func TestAgentLinesKeepTheNewestLines(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	var entries []any
	for i := range AgentLinesKept + 50 {
		entries = append(entries, said(fmt.Sprintf("step %d %s", i, strings.Repeat("x", 600)), at))
	}
	writeLines(t, path, at, entries...)

	// Act
	lines, err := AgentLines(path, "claude")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != AgentLinesKept || !strings.HasPrefix(lines[len(lines)-1], fmt.Sprintf("● step %d ", AgentLinesKept+49)) {
		t.Fatalf("kept %d lines ending %q, want the newest %d", len(lines), lines[len(lines)-1], AgentLinesKept)
	}
	for _, line := range lines {
		if len([]rune(line)) > 400 {
			t.Fatalf("line of %d runes, want each bounded", len([]rune(line)))
		}
	}
}

func TestAgentLinesOfAMissingRecordIsAnError(t *testing.T) {
	if _, err := AgentLines(filepath.Join(t.TempDir(), "gone.jsonl"), "claude"); err == nil {
		t.Error("AgentLines of a missing record succeeded")
	}
}
