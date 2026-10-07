package monitor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const steer = "CFO: stop the steps now and run no more of them. Reply with exactly the word STEER461700 and end your turn."

// The entries below are the shapes each harness wrote live on 2026-10-06,
// when a steer typed during a tool call reached a goblin at its next one.
func claudeQueued(at, text string) map[string]any {
	return map[string]any{"type": "attachment", "timestamp": at, "attachment": map[string]any{"type": "queued_command", "prompt": text, "commandMode": "prompt", "origin": map[string]any{"kind": "human"}}}
}

func claudeEnqueued(at, text string) map[string]any {
	return map[string]any{"type": "queue-operation", "operation": "enqueue", "timestamp": at, "content": text}
}

func claudePrompt(at string, content any) map[string]any {
	return map[string]any{"type": "user", "timestamp": at, "message": map[string]any{"role": "user", "content": content}}
}

func codexMessage(at, role, text string) map[string]any {
	return map[string]any{"timestamp": at, "type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []map[string]any{{"type": "input_text", "text": text}}}}
}

func piMessage(at, role, text string) map[string]any {
	return map[string]any{"type": "message", "timestamp": at, "message": map[string]any{"role": role, "content": []map[string]any{{"type": "text", "text": text}}}}
}

// A goblin has a steer only once its harness's own record shows the steer
// handed to its model after it was sent: Claude Code's queued_command
// attachment or a prompt, Codex's user message, pi's user message. The
// record of the steer waiting in the harness's queue, a tool's result or a
// subagent that holds the same words, the agent quoting it, and an earlier
// copy of it are not.
func TestAGoblinHasASteerOnlyOnceItsHarnessHandedItToTheModel(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	after, before := "2026-10-06T21:51:59.044Z", "2026-10-06T21:40:00Z"
	sidechain := claudePrompt(after, steer)
	sidechain["isSidechain"] = true
	for _, test := range []struct {
		name    string
		harness string
		entries []map[string]any
		want    bool
	}{
		{"Claude Code took it into the running turn", "claude", []map[string]any{claudeEnqueued("2026-10-06T21:51:46.106Z", steer), claudeQueued("2026-10-06T21:51:46.106Z", steer)}, true},
		{"Claude Code started a turn with it", "claude", []map[string]any{claudePrompt(after, steer)}, true},
		{"Claude Code started a turn with it as text blocks", "claude", []map[string]any{claudePrompt(after, []map[string]any{{"type": "text", "text": steer}})}, true},
		{"Claude Code holds it in its queue", "claude", []map[string]any{claudeEnqueued("2026-10-06T21:51:46.106Z", steer)}, false},
		{"a tool's result holds its words", "claude", []map[string]any{claudePrompt(after, []map[string]any{{"type": "tool_result", "content": steer}})}, false},
		{"a subagent was given its words", "claude", []map[string]any{sidechain}, false},
		{"the agent quoted it", "claude", []map[string]any{{"type": "assistant", "timestamp": after, "message": map[string]any{"role": "assistant", "content": claudeText(steer)}}}, false},
		{"an earlier copy was taken before it was sent", "claude", []map[string]any{claudeQueued(before, steer)}, false},
		{"Claude Code took it wrapped across lines", "claude", []map[string]any{claudeQueued(after, strings.Replace(steer, " Reply", "\n  Reply", 1))}, true},
		// Claude Code 2.1.292 kept a 1,136-character answer typed during a
		// turn this way, live on 2026-10-06.
		{"Claude Code took it as a paste", "claude", []map[string]any{claudeQueued(after, "<pasted_content id=\"072c\">\n"+steer+"\n</pasted_content id=\"072c\">")}, true},
		{"Codex took it into the running turn", "codex", []map[string]any{codexMessage("2026-10-06T21:54:55.560Z", "user", steer)}, true},
		{"Codex's agent said it", "codex", []map[string]any{codexMessage(after, "assistant", steer)}, false},
		{"a Codex tool's output holds its words", "codex", []map[string]any{{"timestamp": after, "type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "output": steer}}}, false},
		{"pi took it", "pi", []map[string]any{piMessage(after, "user", steer)}, true},
		{"pi's agent said it", "pi", []map[string]any{piMessage(after, "assistant", steer)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			writeConversation(t, home, test.harness, worktree, test.entries...)

			// Act
			got := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: test.harness, Dir: worktree}, TextDigest(steer), sent)

			// Assert
			if got != test.want {
				t.Errorf("Took = %v, want %v", got, test.want)
			}
		})
	}
}

// Claude Code cuts a folder name past 200 characters and adds a hash of the
// path: a goblin's worktree under a long scratch path was kept here, live on
// 2026-10-06, and its record must be found there.
func TestClaudeCodesFolderForALongWorktreeIsTheOneItMade(t *testing.T) {
	for _, test := range []struct{ dir, want string }{
		{`C:\dev\code-goblins\.worktrees\gb-cg-send-lands-now`, "C--dev-code-goblins--worktrees-gb-cg-send-lands-now"},
		{`C:\Users\fpres\AppData\Local\Temp\claude\C--dev-code-goblins--worktrees-gb-cg-send-lands-now\a56e84f9-16d1-47cc-a093-4d4164fc59a0\scratchpad\steer-proof\claude-goblin\home\worktrees\project\steer-claude`,
			"C--Users-fpres-AppData-Local-Temp-claude-C--dev-code-goblins--worktrees-gb-cg-send-lands-now-a56e84f9-16d1-47cc-a093-4d4164fc59a0-scratchpad-steer-proof-claude-goblin-home-worktrees-project-steer-clau-xrudd0"},
	} {
		if got := claudeFolder(test.dir); got != test.want {
			t.Errorf("claudeFolder(%q) = %q, want %q", test.dir, got, test.want)
		}
	}
}

// Every record kept for a goblin's folder is searched: a newer conversation
// in the same folder, such as one the goblin started itself, does not hide
// the steer its own conversation took. A Codex message handing over several
// texts at once holds each of them.
func TestASteerIsFoundInEveryRecordAndEveryTextHandedOver(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	t.Run("an older conversation in the folder", func(t *testing.T) {
		home, worktree := t.TempDir(), t.TempDir()
		folder := filepath.Join(home, ".claude", "projects", claudeFolder(worktree))
		transcriptLines(t, filepath.Join(folder, "goblin.jsonl"), time.Now(), claudeQueued("2026-10-06T21:51:46Z", steer))
		transcriptLines(t, filepath.Join(folder, "helper.jsonl"), time.Now().Add(time.Minute), claudePrompt("2026-10-06T21:52:30Z", "summarize the diff"))

		if !(&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "claude", Dir: worktree}, TextDigest(steer), sent) {
			t.Error("Took = false, want the goblin's own conversation found beside a newer one")
		}
	})
	t.Run("several texts in one message", func(t *testing.T) {
		home, worktree := t.TempDir(), t.TempDir()
		opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": worktree}}
		both := map[string]any{"timestamp": "2026-10-06T21:54:55Z", "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": "CFO: main moved, merge it first."}, {"type": "input_text", "text": steer}}}}
		transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "06", "rollout-2026-10-06T16-53-30.jsonl"), time.Now(), opening, both)

		if !(&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "codex", Dir: worktree}, TextDigest(steer), sent) {
			t.Error("Took = false, want the steer found among the texts one message handed over")
		}
	})
	t.Run("a harness it does not know", func(t *testing.T) {
		if (&HostProgress{Home: t.TempDir()}).Took(context.Background(), Conversation{Harness: "aider", Session: "0be78a0f-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent) {
			t.Error("Took = true for a harness whose record is not read")
		}
	})
}

// A machine keeps thousands of Codex rollouts, whose openings are read to
// find a goblin's, and a fresh cfo send read them all for minutes: only the
// rollouts Codex filed since the day the goblin started are read.
func TestOnlyRolloutsFiledSinceTheGoblinStartedAreRead(t *testing.T) {
	sent := time.Date(2026, 10, 6, 21, 54, 52, 0, time.UTC)
	for _, test := range []struct {
		day  string
		want bool
	}{{"06", true}, {"01", false}} {
		t.Run("filed on day "+test.day, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": worktree}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", test.day, "rollout-2026-10-"+test.day+"T16-53-39.jsonl"), time.Now(), opening, codexMessage("2026-10-06T21:54:55Z", "user", steer))

			// Act
			got := (&HostProgress{Home: home}).Took(context.Background(), Conversation{Harness: "codex", Dir: worktree, Started: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}, TextDigest(steer), sent)

			// Assert
			if got != test.want {
				t.Errorf("Took = %v, want %v", got, test.want)
			}
		})
	}
}

// The CFO's record is found by the session its registration names, in
// whichever project folder holds it; a goblin's by the folder it works in.
func TestTheCFOsRecordIsFoundByItsSession(t *testing.T) {
	// Arrange
	home := t.TempDir()
	sent := time.Date(2026, 10, 6, 21, 51, 45, 0, time.UTC)
	transcriptLines(t, filepath.Join(home, ".claude", "projects", "C--dev-code-goblins", "0be78a0f-1dfb-4139-8326-064c28e6a843.jsonl"), time.Now(), claudeQueued("2026-10-06T21:52:00Z", steer))
	progress := &HostProgress{Home: home}

	// Act
	own := progress.Took(context.Background(), Conversation{Harness: "claude", Session: "0be78a0f-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent)
	other := progress.Took(context.Background(), Conversation{Harness: "claude", Session: "11111111-1dfb-4139-8326-064c28e6a843"}, TextDigest(steer), sent)

	// Assert
	if !own || other {
		t.Errorf("Took for its own session = %v and another = %v, want true and false", own, other)
	}
}

// writeConversation writes entries where harness keeps the record of an
// agent working in worktree, beside one for another folder that holds the
// same entries, which must never count.
func writeConversation(t *testing.T, home, harness, worktree string, entries ...map[string]any) {
	t.Helper()
	other := t.TempDir()
	taken := map[string]map[string]any{"claude": claudeQueued("2026-10-06T21:59:00Z", steer), "codex": codexMessage("2026-10-06T21:59:00Z", "user", steer), "pi": piMessage("2026-10-06T21:59:00Z", "user", steer)}[harness]
	for i, dir := range []string{worktree, other} {
		written := time.Now().Add(time.Duration(i) * time.Minute)
		if dir == other {
			entries = []map[string]any{taken}
		}
		switch harness {
		case "claude":
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, dir), "conversation.jsonl"), written, entries...)
		case "codex":
			opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": dir}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "06", fmt.Sprintf("rollout-2026-10-06T16-53-3%d.jsonl", i)), written, append([]map[string]any{opening}, entries...)...)
		case "pi":
			folder := "--" + strings.NewReplacer(`\`, "-", ":", "-").Replace(dir) + "--"
			opening := map[string]any{"type": "session", "cwd": dir}
			transcriptLines(t, filepath.Join(home, ".pi", "agent", "sessions", folder, "2026-10-06T21-50-00-000Z_session.jsonl"), written, append([]map[string]any{opening}, entries...)...)
		}
	}
}
