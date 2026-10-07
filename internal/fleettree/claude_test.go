package fleettree

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

const claudeSession = "6d90948b-7dd5-4524-8670-be159f965ebd"

// claudeGoblin writes a goblin's Claude Code conversation under home and
// returns the goblin to read and the session's folder. The file's write time
// is older than its entries, so their own timestamps say when it was written.
func claudeGoblin(t *testing.T, home string, entries ...any) (Goblin, string) {
	t.Helper()
	folder := filepath.Join(home, ".claude", "projects", "C--work-gb-tree")
	writeLines(t, filepath.Join(folder, claudeSession+".jsonl"), at.Add(-24*time.Hour), entries...)
	return Goblin{Meta: state.TaskMeta{ID: "tree", Harness: "claude", SpawnGen: "s1"}, Session: claudeSession}, filepath.Join(folder, claudeSession)
}

func TestReadFindsClaudeSubagentsFinishedAndRunning(t *testing.T) {
	// Arrange
	home := t.TempDir()
	start := at.Add(-30 * time.Minute)
	finished := at.Add(-12 * time.Minute)
	finishedNotice := notification("a2", "toolu_B", "completed", `Agent "Research MCP OAuth" finished`, "")
	goblin, session := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Map harness plumbing", "subagent_type": "Explore", "prompt": "map it"}, start),
		result("toolu_A", "Async agent launched successfully.", object{"isAsync": true, "status": "async_launched", "agentId": "a1", "description": "Map harness plumbing"}, start.Add(time.Second)),
		call("toolu_B", "Agent", object{"description": "Research MCP OAuth", "prompt": "research it"}, start.Add(time.Minute)),
		result("toolu_B", "Async agent launched successfully.", object{"isAsync": true, "status": "async_launched", "agentId": "a2"}, start.Add(time.Minute+time.Second)),
		queued(finishedNotice, finished),
		delivered(finishedNotice, finished.Add(20*time.Second)),
	)
	writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at.Add(-time.Minute), said("Reading internal/monitor/progress.go", at.Add(-time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, err := reader.Read(context.Background(), goblin)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	running := child(t, tree, "subagent:toolu_A")
	if running.Kind != KindSubagent || running.State != Working || running.Label != "Map harness plumbing" || running.Detail != "Explore" || !running.Started.Equal(start) {
		t.Errorf("running sub-agent = %+v, want working Explore agent started %v", running, start)
	}
	if !running.LastActivity.Equal(at.Add(-time.Minute)) || running.LastLine != "Reading internal/monitor/progress.go" {
		t.Errorf("running sub-agent's last activity = %v %q, want its own transcript's last entry", running.LastActivity, running.LastLine)
	}
	done := child(t, tree, "subagent:toolu_B")
	if done.State != Done || done.Detail != "general-purpose" || !done.Finished.Equal(finished) {
		t.Errorf("finished sub-agent = %+v, want done at the queued notice %v, not the delivered copy", done, finished)
	}
	if !tree.ConversationAt.Equal(finished.Add(20 * time.Second)) {
		t.Errorf("ConversationAt = %v, want the conversation's last entry", tree.ConversationAt)
	}
}

// A sub-agent whose result named no id is still tied to its own transcript
// through the .meta.json Claude Code writes beside it.
func TestReadTiesASubagentToItsTranscriptThroughItsMetaFile(t *testing.T) {
	// Arrange
	home := t.TempDir()
	goblin, session := claudeGoblin(t, home, call("toolu_A", "Task", object{"description": "Find specs"}, at.Add(-5*time.Minute)))
	writeFile(t, filepath.Join(session, "subagents", "agent-a9.meta.json"), `{"agentType":"Explore","description":"Find specs","toolUseId":"toolu_A"}`, at)
	writeLines(t, filepath.Join(session, "subagents", "agent-a9.jsonl"), at.Add(-2*time.Minute), call("x", "Grep", object{"pattern": "panelDefaults"}, at.Add(-2*time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	agent := child(t, tree, "subagent:toolu_A")
	if !agent.LastActivity.Equal(at.Add(-2*time.Minute)) || agent.LastLine != "Grep panelDefaults" {
		t.Errorf("sub-agent = %+v, want its transcript's last tool call", agent)
	}
}

func TestReadFindsClaudeBackgroundShellsAndMonitors(t *testing.T) {
	// Arrange
	home := t.TempDir()
	start := at.Add(-20 * time.Minute)
	output := filepath.Join(home, "tasks", "b1.output")
	writeFile(t, output, "=== RUN TestA\n\x1b[32mok  \x1b[0m internal/monitor 41.2s\n\n", at.Add(-2*time.Minute))
	failedNotice := notification("b2", "toolu_S2", "failed", `Background command "Build the board" failed with exit code 1`, "")
	killedNotice := notification("b3", "toolu_S3", "killed", `Background command "Watch memory" was stopped`, "")
	goblin, _ := claudeGoblin(t, home,
		call("toolu_S1", "Bash", object{"command": "go test ./internal/monitor/", "description": "Run the affected Go tests", "run_in_background": true}, start),
		result("toolu_S1", "Command running in background with ID: b1. Output is being written to: "+output, object{"backgroundTaskId": "b1"}, start.Add(time.Second)),
		call("toolu_S2", "Bash", object{"command": "npm run build", "description": "Build the board"}, start.Add(time.Minute)),
		result("toolu_S2", "Command did not complete within its 120s timeout and was moved to the background (ID: b2).", object{"backgroundTaskId": "b2", "timedOutAfterMs": 120000}, start.Add(3*time.Minute)),
		queued(failedNotice, start.Add(4*time.Minute)),
		call("toolu_S3", "PowerShell", object{"command": "Get-Counter", "description": "Watch memory", "run_in_background": true}, start.Add(5*time.Minute)),
		result("toolu_S3", "running", object{"backgroundTaskId": "b3"}, start.Add(5*time.Minute)),
		call("toolu_K", "TaskStop", object{"task_id": "b3"}, start.Add(6*time.Minute)),
		result("toolu_K", "Successfully stopped task: b3", object{"message": "stopped", "task_id": "b3", "task_type": "local_bash"}, start.Add(6*time.Minute)),
		queued(killedNotice, start.Add(6*time.Minute+time.Second)),
		call("toolu_F", "Bash", object{"command": "git status", "description": "Show status"}, start.Add(7*time.Minute)),
		result("toolu_F", "clean", object{"stdout": "clean", "interrupted": false}, start.Add(7*time.Minute)),
		call("toolu_M", "Monitor", object{"description": "CI checks on PR 398", "command": "gh pr checks 398 --watch", "timeout_ms": 1800000}, start.Add(8*time.Minute)),
		result("toolu_M", "monitoring", object{"taskId": "m1", "timeoutMs": 1800000, "persistent": false}, start.Add(8*time.Minute)),
		queued(notification("m1", "", "", `Monitor event: "CI checks on PR 398"`, "PR398 pending: 3\nmore"), at.Add(-3*time.Minute)),
	)
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	if len(tree.Children) != 4 {
		t.Fatalf("children = %+v, want three shells and a monitor, never the foreground command", tree.Children)
	}
	running := child(t, tree, "shell:b1")
	if running.Kind != KindShell || running.State != Working || running.Label != "Run the affected Go tests" || running.LastLine != "ok   internal/monitor 41.2s" || !running.LastActivity.Equal(at.Add(-2*time.Minute)) {
		t.Errorf("running shell = %+v, want working with its output's last line and write time", running)
	}
	failed := child(t, tree, "shell:b2")
	if failed.State != Failed || !failed.Started.Equal(start.Add(time.Minute)) || failed.LastLine != `Background command "Build the board" failed with exit code 1` {
		t.Errorf("backgrounded build = %+v, want failed, started when it was called", failed)
	}
	stopped := child(t, tree, "shell:b3")
	if stopped.State != Done || stopped.LastLine != "stopped by the goblin" {
		t.Errorf("stopped shell = %+v, want done: the goblin stopped it, and the killed notice after does not undo that", stopped)
	}
	monitor := child(t, tree, "monitor:m1")
	if monitor.Kind != KindMonitor || monitor.State != Working || monitor.LastLine != "PR398 pending: 3" || !monitor.LastActivity.Equal(at.Add(-3*time.Minute)) {
		t.Errorf("monitor = %+v, want working with its last event", monitor)
	}
}

// A working child quiet for the stale mark shows as silent with its last
// line, and the goblin's tree still holds it.
func TestReadShowsAQuietChildAsSilentWithItsLastLine(t *testing.T) {
	// Arrange
	home := t.TempDir()
	output := filepath.Join(home, "tasks", "b1.output")
	writeFile(t, output, "waiting for 5 GB free\n", at.Add(-SilentAfter-time.Minute))
	goblin, session := claudeGoblin(t, home,
		call("toolu_S1", "Bash", object{"command": "until free; do sleep 30; done", "description": "Wait for memory", "run_in_background": true}, at.Add(-time.Hour)),
		result("toolu_S1", "Output is being written to: "+output, object{"backgroundTaskId": "b1"}, at.Add(-time.Hour)),
		call("toolu_A", "Agent", object{"description": "Map plumbing"}, at.Add(-time.Hour)),
		result("toolu_A", "launched", object{"status": "async_launched", "agentId": "a1"}, at.Add(-time.Hour)),
	)
	writeLines(t, filepath.Join(session, "subagents", "agent-a1.jsonl"), at.Add(-SilentAfter+time.Minute), said("Still reading", at.Add(-SilentAfter+time.Minute)))
	reader := Reader{Home: home, Now: func() time.Time { return at }}

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	shell := child(t, tree, "shell:b1")
	if shell.State != Silent || shell.LastLine != "waiting for 5 GB free" {
		t.Errorf("quiet shell = %+v, want silent with its last line", shell)
	}
	if agent := child(t, tree, "subagent:toolu_A"); agent.State != Working {
		t.Errorf("sub-agent active %v ago = %+v, want working until the stale mark", SilentAfter-time.Minute, agent)
	}
}

// Each read parses only what was written since the last: a notice appended
// later ends the child it names.
func TestReadParsesOnlyNewEntriesAndKeepsWhatItRead(t *testing.T) {
	// Arrange
	home := t.TempDir()
	goblin, _ := claudeGoblin(t, home,
		call("toolu_A", "Agent", object{"description": "Map plumbing"}, at.Add(-time.Hour)),
		result("toolu_A", "launched", object{"status": "async_launched", "agentId": "a1"}, at.Add(-time.Hour)),
	)
	reader := Reader{Home: home, Now: func() time.Time { return at }}
	if tree, _ := reader.Read(context.Background(), goblin); child(t, tree, "subagent:toolu_A").State != Silent {
		t.Fatalf("first read = %+v, want the agent silent after an hour", tree.Children)
	}
	path := filepath.Join(home, ".claude", "projects", "C--work-gb-tree", claudeSession+".jsonl")
	appendLines(t, path, queued(notification("a1", "toolu_A", "completed", "finished", ""), at))

	// Act
	tree, _ := reader.Read(context.Background(), goblin)

	// Assert
	if agent := child(t, tree, "subagent:toolu_A"); agent.State != Done || agent.Label != "Map plumbing" {
		t.Errorf("after the notice = %+v, want done, with what the first read found kept", agent)
	}
}
