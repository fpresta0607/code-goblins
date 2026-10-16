package fleettree

import (
	"context"
	"errors"
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
func TestChildLinesOfASubagentAreItsOwnRecord(t *testing.T) {
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

	for _, tc := range []struct {
		task, node string
		want       []AgentEntry
	}{
		{"tree", "subagent:toolu_A", []AgentEntry{{Kind: EntryReply, Text: "reading"}}},
		{"tree", "subagent:toolu_B", []AgentEntry{{Kind: EntryReply, Text: "finding"}}},
		{"tree", "subagent:toolu_C", nil},
		{"tree", "subagent:toolu_X", nil},
		{"other", "subagent:toolu_A", nil},
	} {
		t.Run(tc.task+" "+tc.node, func(t *testing.T) {
			// Act
			output, err := reader.ChildOutput(tc.task, tc.node)

			// Assert
			if tc.want == nil && !errors.Is(err, ErrNoRecord) || tc.want != nil && (err != nil || !reflect.DeepEqual(output, Output{Entries: tc.want})) {
				t.Errorf("ChildOutput = %+v, %v; want %+v", output, err, tc.want)
			}
		})
	}
}

// A Codex child agent's own record is its rollout.
func TestChildLinesOfACodexChildAreItsRollout(t *testing.T) {
	// Arrange
	home := t.TempDir()
	worktree := t.TempDir()
	day := filepath.Join(home, ".codex", "sessions", "2026", "10", "06")
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-00-00-root.jsonl"), at, sessionMeta("root", worktree, "", "", "", at.Add(-time.Hour)))
	rollout := filepath.Join(day, "rollout-2026-10-06T15-10-00-c1.jsonl")
	writeLines(t, rollout, at, sessionMeta("c1", worktree, "root", "/root/map_monitor", "Curie", at.Add(-time.Hour)), codexEvent("task_started", at, nil),
		object{"type": "response_item", "payload": object{"type": "message", "role": "assistant", "content": []object{{"type": "output_text", "text": "Mapping the monitor"}}}})
	writeLines(t, filepath.Join(day, "rollout-2026-10-06T15-20-00-x9.jsonl"), at, sessionMeta("x9", worktree, "elsewhere", "/root/other", "Noether", at.Add(-time.Hour)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	if _, err := reader.Read(context.Background(), Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "codex", Backend: "native", Worktree: worktree}}); err != nil {
		t.Fatal(err)
	}

	// Act
	output, err := reader.ChildOutput("tree", "subagent:c1")
	other, otherErr := reader.ChildOutput("tree", "subagent:x9")

	// Assert
	if err != nil || !reflect.DeepEqual(output.Entries, []AgentEntry{{Kind: EntryReply, Text: "Mapping the monitor"}}) || !errors.Is(otherErr, ErrNoRecord) {
		t.Errorf("ChildOutput = %+v, %v and another thread's agent %+v, %v; want the child's own rollout only", output, err, other, otherErr)
	}
}

// What a dead goblin's reader kept is forgotten with it.
func TestForgetDropsAGoblinsChildRecords(t *testing.T) {
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
	if output, err := reader.ChildOutput("tree", "subagent:toolu_A"); !errors.Is(err, ErrNoRecord) {
		t.Errorf("ChildOutput after Forget = %+v, %v; want none kept", output, err)
	}
}

// A sub-agent's terminal shows its work as Claude Code draws it, so its
// record is read into the steps Claude Code draws: the task it was given,
// what it said, and each tool it called by the name Claude Code shows, on
// what, whether it is running, done or failed, and the first lines of what
// came back with how many more.
func TestAgentEntriesShowAClaudeSubagentsWork(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	failed := func(id, text string) object {
		return object{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "user", "content": []object{{"type": "tool_result", "tool_use_id": id, "is_error": true, "content": []object{{"type": "text", "text": text}}}}}}
	}
	writeLines(t, path, at,
		object{"type": "user", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "user", "content": "Find where the harness\nstarts sub-agents"}},
		object{"type": "assistant", "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{
			{"type": "thinking", "thinking": "private"},
			{"type": "text", "text": "I will search.\nThen read."},
			{"type": "tool_use", "id": "t1", "name": "Grep", "input": object{"pattern": "subagents/agent-"}},
		}}},
		result("t1", "Found 3 files\ninternal/a.go\ninternal/b.go", nil, at),
		object{"type": "assistant", "cwd": `C:\work`, "timestamp": at.Format(time.RFC3339Nano), "message": object{"role": "assistant", "content": []object{{"type": "tool_use", "id": "t2", "name": "Read", "input": object{"file_path": `C:\work\internal\fleettree\claude.go`}}}}},
		failed("t2", "<tool_use_error>File does not exist.</tool_use_error>"),
		call("t3", "Read", object{"file_path": `D:\elsewhere\notes.md`}, at),
		result("t3", "     1\tone\n     2\ttwo", nil, at),
		call("t4", "Bash", object{"command": "go test ./internal/fleettree/\necho done", "description": "Run the tree tests"}, at),
		result("t4", "=== RUN TestA\n--- PASS: TestA\n  indented\nok  fleettree 1.2s\nPASS", nil, at),
		call("t5", "Edit", object{"file_path": `C:\work\agent.go`}, at),
		`{"type":"assistant","message":`,
	)

	// Act
	entries, err := AgentEntries(path, "claude")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentEntry{
		{Kind: EntryPrompt, Text: "Find where the harness\nstarts sub-agents"},
		{Kind: EntryReply, Text: "I will search.\nThen read."},
		{Kind: EntryCall, Tool: "Search", Subject: "subagents/agent-", State: CallDone, Result: []string{"Found 3 files"}},
		{Kind: EntryCall, Tool: "Read", Subject: `internal\fleettree\claude.go`, State: CallFailed, Result: []string{"Error: File does not exist."}},
		{Kind: EntryCall, Tool: "Read", Subject: `D:\elsewhere\notes.md`, State: CallDone, Result: []string{"Read 2 lines"}},
		{Kind: EntryCall, Tool: "Bash", Subject: "go test ./internal/fleettree/", State: CallDone, Result: []string{"=== RUN TestA", "--- PASS: TestA", "  indented"}, More: 2},
		{Kind: EntryCall, Tool: "Update", Subject: `C:\work\agent.go`, State: CallRunning},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v", entries, want)
	}
}

// A Codex child's terminal shows the task it was given, its messages, its
// tool calls and the first lines of their output, each output under the call
// that made it.
func TestAgentEntriesShowACodexChildsWork(t *testing.T) {
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
		item(object{"type": "function_call", "name": "shell", "arguments": `{"command":["go","test"]}`, "call_id": "d"}),
		item(object{"type": "function_call_output", "call_id": "c", "output": "internal/monitor/stall.go:12\ninternal/monitor/stall.go:40"}),
		item(object{"type": "message", "role": "assistant", "content": []object{{"type": "output_text", "text": "The stall rule is in stall.go."}}}),
	)

	// Act
	entries, err := AgentEntries(path, "codex")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []AgentEntry{
		{Kind: EntryPrompt, Text: "Map the monitor"},
		{Kind: EntryCall, Tool: "shell", Subject: `{"command":["rg","-n","stall"]}`, State: CallDone, Result: []string{"internal/monitor/stall.go:12", "internal/monitor/stall.go:40"}},
		{Kind: EntryCall, Tool: "shell", Subject: `{"command":["go","test"]}`, State: CallRunning},
		{Kind: EntryReply, Text: "The stall rule is in stall.go."},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v", entries, want)
	}
}

// A long-running sub-agent's terminal holds its newest entries, each bounded.
func TestAgentEntriesKeepTheNewest(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	var records []any
	for i := range AgentEntriesKept + 50 {
		records = append(records, said(fmt.Sprintf("step %d %s", i, strings.Repeat("x", agentTextLimit)), at))
	}
	writeLines(t, path, at, records...)

	// Act
	entries, err := AgentEntries(path, "claude")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != AgentEntriesKept || !strings.HasPrefix(entries[len(entries)-1].Text, fmt.Sprintf("step %d ", AgentEntriesKept+49)) {
		t.Fatalf("kept %d entries ending %q, want the newest %d", len(entries), entries[len(entries)-1].Text[:20], AgentEntriesKept)
	}
	for _, entry := range entries {
		if len([]rune(entry.Text)) > agentTextLimit {
			t.Fatalf("entry of %d runes, want each bounded", len([]rune(entry.Text)))
		}
	}
}

// What came back for a call made before the read's reach has no call to go
// under, and is left out.
func TestAgentEntriesLeaveOutAResultWithoutItsCall(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "agent-a1.jsonl")
	writeLines(t, path, at, result("gone", "orphan output", nil, at), said("next", at))

	// Act
	entries, err := AgentEntries(path, "claude")

	// Assert
	if err != nil || !reflect.DeepEqual(entries, []AgentEntry{{Kind: EntryReply, Text: "next"}}) {
		t.Errorf("entries = %+v, %v; want only the reply", entries, err)
	}
}

func TestAgentEntriesOfAMissingRecordIsAnError(t *testing.T) {
	if _, err := AgentEntries(filepath.Join(t.TempDir(), "gone.jsonl"), "claude"); err == nil {
		t.Error("AgentEntries of a missing record succeeded")
	}
}
