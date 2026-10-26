package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// transcriptLines writes entries as a JSON Lines transcript at path, each
// written a second after the last, so the newest file is the last written.
func transcriptLines(t *testing.T, path string, written time.Time, entries ...map[string]any) {
	t.Helper()
	var data []byte
	for _, entry := range entries {
		line, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		data = append(append(data, line...), '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}
}

func claudeEntry(kind, cwd string, content any) map[string]any {
	return map[string]any{"type": kind, "cwd": cwd, "timestamp": "2026-10-05T20:55:35Z", "message": map[string]any{"role": kind, "model": "claude-opus-5-5", "content": content}}
}

func claudeText(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

// claudeProjectFolder is the folder Claude Code keeps a conversation held in
// worktree under: its path with every character but a letter or a digit as a
// hyphen.
func claudeProjectFolder(home, worktree string) string {
	return filepath.Join(home, ".claude", "projects", strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, worktree))
}

// recordedAs is a prober whose board recorded session as every goblin's own
// conversation.
func recordedAs(home, session string) *HostProgress {
	return &HostProgress{Home: home, Tree: &fleettree.Reader{Home: home, Recorded: func(state.TaskMeta) string { return session }}}
}

// A native Claude Code goblin's last reply is the text that ended its last
// turn, read from its own conversation, and nothing while a turn is still
// running or ended on a tool call.
func TestANativeClaudeGoblinsLastReplyIsReadFromItsTranscript(t *testing.T) {
	cwd := t.TempDir()
	prompt := claudeEntry("user", cwd, "CFO: PR 369's failure is not yours.")
	thinking := claudeEntry("assistant", cwd, []map[string]any{{"type": "thinking", "thinking": ""}})
	reply := claudeEntry("assistant", cwd, claudeText(editorSyncReply))
	settled := []map[string]any{{"type": "system", "subtype": "stop_hook_summary", "cwd": cwd}, {"type": "system", "subtype": "turn_duration", "cwd": cwd}}
	toolUse := claudeEntry("assistant", cwd, []map[string]any{{"type": "tool_use", "name": "Bash"}})
	toolResult := claudeEntry("user", cwd, []map[string]any{{"type": "tool_result", "content": "ok"}})
	synthetic := claudeEntry("assistant", cwd, claudeText("No response requested."))
	synthetic["message"].(map[string]any)["model"] = "<synthetic>"
	sidechain := claudeEntry("assistant", cwd, claudeText("Want me to look deeper?"))
	sidechain["isSidechain"] = true
	for _, test := range []struct {
		name    string
		entries []map[string]any
		want    string
	}{
		{"a turn that ended", append([]map[string]any{prompt, thinking, reply}, settled...), editorSyncReply},
		{"a turn still running", []map[string]any{reply, prompt}, ""},
		{"a turn that stopped on a tool call", []map[string]any{prompt, reply, toolUse}, ""},
		{"a reply after a tool's result", []map[string]any{prompt, claudeEntry("assistant", cwd, claudeText("Checking.")), toolUse, toolResult, claudeEntry("assistant", cwd, claudeText("Tests pass.")), claudeEntry("assistant", cwd, claudeText("Should I open the PR?"))}, "Tests pass.\n\nShould I open the PR?"},
		{"Claude Code's own placeholder reply", []map[string]any{prompt, synthetic}, ""},
		{"a subagent's entry in the parent's file", []map[string]any{prompt, reply, sidechain}, editorSyncReply},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			transcriptLines(t, filepath.Join(claudeProjectFolder(home, cwd), "session-1.jsonl"), time.Now(), test.entries...)
			meta := nativeMeta("g1", "claude")
			meta.Worktree = cwd

			// Act
			got := recordedAs(home, "session-1").LastReply(context.Background(), meta, EndpointSample{Harness: "claude"})

			// Assert
			if got != test.want {
				t.Errorf("reply = %q, want %q", got, test.want)
			}
		})
	}
}

// The goblin's conversation is the one the board's record or its own harness
// proves, as the board reads it, never merely its folder's newest, where a
// forked CFO session worked on 2026-10-06; a Herdr goblin's is the session
// Herdr names, wherever Claude Code keeps it.
func TestTheClaudeConversationReadIsTheGoblinsOwn(t *testing.T) {
	// Arrange
	home, cwd := t.TempDir(), t.TempDir()
	folder := claudeProjectFolder(home, cwd)
	written := time.Now()
	transcriptLines(t, filepath.Join(folder, "own.jsonl"), written.Add(-time.Hour), claudeEntry("assistant", cwd, claudeText("PR 12 is open.")))
	transcriptLines(t, filepath.Join(folder, "forked.jsonl"), written, claudeEntry("assistant", cwd, claudeText("Should I open the PR?")))
	transcriptLines(t, filepath.Join(home, ".claude", "projects", "elsewhere", "herdr-session.jsonl"), written.Add(-2*time.Hour), claudeEntry("assistant", `C:\elsewhere`, claudeText("Which layout do you want?")))
	prober := recordedAs(home, "own")

	// Act
	meta := nativeMeta("g1", "claude")
	meta.Worktree = cwd
	native := prober.LastReply(context.Background(), meta, EndpointSample{Harness: "claude"})
	herdr := prober.LastReply(context.Background(), metaFor("g1"), EndpointSample{Harness: "claude", Session: "herdr-session"})

	// Assert
	if native != "PR 12 is open." {
		t.Errorf("native reply = %q, want its own conversation's, not the newer forked one's", native)
	}
	if herdr != "Which layout do you want?" {
		t.Errorf("Herdr reply = %q, want the named session's", herdr)
	}
}

// A Codex goblin's last reply is the last agent message its rollout records
// for a turn that completed, found by the worktree the rollout names; a turn
// started since has no reply yet.
func TestACodexGoblinsLastReplyIsReadFromItsRollout(t *testing.T) {
	completed := map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "last_agent_message": "Tests pass. Should I open the PR?"}}
	started := map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started"}}
	for _, test := range []struct {
		name    string
		entries []map[string]any
		want    string
	}{
		{"a turn that completed", []map[string]any{started, completed}, "Tests pass. Should I open the PR?"},
		{"a turn started since", []map[string]any{started, completed, started}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			opening := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": worktree}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T17-35-38-own.jsonl"), time.Now(), append([]map[string]any{opening}, test.entries...)...)
			foreign := map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": t.TempDir()}}
			transcriptLines(t, filepath.Join(home, ".codex", "sessions", "2026", "10", "02", "rollout-2026-10-02T18-00-00-foreign.jsonl"), time.Now().Add(time.Minute), foreign, completed)
			meta := nativeMeta("g1", "codex")
			meta.Worktree = worktree

			// Act
			got := (&HostProgress{Home: home}).LastReply(context.Background(), meta, EndpointSample{Harness: "codex"})

			// Assert
			if got != test.want {
				t.Errorf("reply = %q, want %q", got, test.want)
			}
		})
	}
}

func piTurnMessage(role, stopReason string, content ...map[string]any) map[string]any {
	return map[string]any{"type": "message", "timestamp": "2026-10-16T20:04:14Z", "message": map[string]any{"role": role, "stopReason": stopReason, "content": content}}
}

// piFolder is the folder pi 0.85 keeps the sessions of worktree in.
func piFolder(home, worktree string) string {
	return filepath.Join(home, ".pi", "agent", "sessions", "--"+strings.NewReplacer(`\`, "-", "/", "-", ":", "-").Replace(worktree)+"--")
}

// A pi goblin's last reply is the text of the assistant message that ended its
// last turn, read from its own session as Claude Code's and Codex's are, and
// nothing for a turn still running, ended on a tool call, cut off at pi's
// output cap, failed or aborted: on 2026-10-16 replies cut off at 16384
// tokens showed their thinking on screen, which the stale sweep read as
// questions.
func TestAPiGoblinsLastReplyIsReadFromItsSession(t *testing.T) {
	prompt := map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": "CFO: carry on"}}
	thought := map[string]any{"type": "thinking", "thinking": "Should I open the pull request now, or wait?"}
	text := func(text string) map[string]any { return map[string]any{"type": "text", "text": text} }
	toolCall := map[string]any{"type": "toolCall", "name": "bash"}
	toolResult := map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "content": []map[string]any{text("ok")}}}
	for _, test := range []struct {
		name    string
		entries []map[string]any
		want    string
	}{
		{"a turn that ended", []map[string]any{prompt, piTurnMessage("assistant", "stop", thought, text("Tests pass."), text("Should I open the PR?"))}, "Tests pass.\n\nShould I open the PR?"},
		{"a turn cut off at the output cap", []map[string]any{prompt, piTurnMessage("assistant", "length", thought)}, ""},
		{"a turn the provider refused", []map[string]any{prompt, piTurnMessage("assistant", "error")}, ""},
		{"a turn aborted", []map[string]any{prompt, piTurnMessage("assistant", "aborted", text("Should I"))}, ""},
		{"a turn that stopped on a tool call", []map[string]any{prompt, piTurnMessage("assistant", "toolUse", text("Checking."), toolCall)}, ""},
		{"a turn still running", []map[string]any{prompt, piTurnMessage("assistant", "toolUse", toolCall), toolResult}, ""},
		{"a reply an extension's message follows", []map[string]any{prompt, piTurnMessage("assistant", "stop", text("PR 12 is open.")), {"type": "custom_message", "customType": "herdr-state", "content": "herdr state", "display": true}}, "PR 12 is open."},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(piFolder(home, worktree), "2026-10-16T20-04-14-465Z_01a14650-ac01-7127-9115-4f3a806b967f.jsonl"), time.Now(), test.entries...)
			meta := nativeMeta("g1", "pi")
			meta.Worktree = worktree

			// Act
			recorded := recordedAs(home, "01a14650-ac01-7127-9115-4f3a806b967f").LastReply(context.Background(), meta, EndpointSample{Harness: "pi"})
			unrecorded := (&HostProgress{Home: home}).LastReply(context.Background(), meta, EndpointSample{Harness: "pi"})

			// Assert
			if recorded != test.want || unrecorded != test.want {
				t.Errorf("reply = %q from the recorded session and %q from the worktree's newest, want %q", recorded, unrecorded, test.want)
			}
		})
	}
}

// A pi session records the boundaries of its own turn as it runs, as a Codex
// rollout does: the prompt, the assistant message that stopped on a call and
// the tool result after it leave the turn open, and an assistant message that
// stopped, failed, was cut off at the output cap or was aborted ends it. A
// record that says nothing of a turn, such as a model change, proves nothing
// either way.
func TestPiTurnStateReadsTheSessionsOwnTurnBoundaries(t *testing.T) {
	prompt := map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": "CFO: carry on"}}
	toolCall := map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []map[string]any{{"type": "toolCall", "id": "call-1", "name": "bash"}}}}
	toolResult := map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolCallId": "call-1", "content": []map[string]any{{"type": "text", "text": "ok"}}}}
	shell := map[string]any{"type": "message", "message": map[string]any{"role": "bashExecution", "command": "git status", "output": "clean"}}
	modelChange := map[string]any{"type": "model_change", "provider": "acme", "modelId": "thinker-1"}
	for _, test := range []struct {
		name    string
		entries []map[string]any
		open    bool
		known   bool
	}{
		{name: "no turn boundary at all", entries: []map[string]any{modelChange}},
		{name: "a prompt handed to the model", entries: []map[string]any{modelChange, prompt}, open: true, known: true},
		{name: "a turn stopped on a tool call", entries: []map[string]any{prompt, toolCall}, open: true, known: true},
		{name: "a tool result waiting for the reply", entries: []map[string]any{prompt, toolCall, toolResult}, open: true, known: true},
		{name: "a turn that completed", entries: []map[string]any{prompt, piTurnMessage("assistant", "stop", map[string]any{"type": "text", "text": "Done."})}, known: true},
		{name: "a turn that was aborted", entries: []map[string]any{prompt, piTurnMessage("assistant", "aborted")}, known: true},
		{name: "a turn the provider refused", entries: []map[string]any{prompt, piTurnMessage("assistant", "error")}, known: true},
		{name: "a turn cut off at the output cap", entries: []map[string]any{prompt, piTurnMessage("assistant", "length")}, known: true},
		{name: "a shell command at the prompt", entries: []map[string]any{prompt, piTurnMessage("assistant", "stop", map[string]any{"type": "text", "text": "Done."}), shell}, known: true},
		{name: "a model change after a finished turn", entries: []map[string]any{prompt, piTurnMessage("assistant", "stop"), modelChange}, known: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			entries := make([][]byte, 0, len(test.entries))
			for _, entry := range test.entries {
				line, err := json.Marshal(entry)
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, line)
			}

			// Act
			open, known := piTurnState(entries)

			// Assert
			if open != test.open || known != test.known {
				t.Fatalf("piTurnState = open %t known %t, want open %t known %t", open, known, test.open, test.known)
			}
		})
	}
}

// The monitor reads whether a native pi goblin's turn is open through the same
// reader the board's tree uses, so a frame caught mid-redraw is not taken for
// the end of a turn. The session is the goblin's own, named by its worktree.
func TestANativePiTurnsOpenIsReadFromItsOwnSession(t *testing.T) {
	prompt := map[string]any{"type": "message", "message": map[string]any{"role": "user", "content": "CFO: carry on"}}
	toolCall := map[string]any{"type": "message", "message": map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []map[string]any{{"type": "toolCall", "id": "call-1", "name": "bash"}}}}
	reply := piTurnMessage("assistant", "stop", map[string]any{"type": "text", "text": "Done."})
	for _, test := range []struct {
		name    string
		entries []map[string]any
		open    bool
		known   bool
	}{
		{name: "a turn still running", entries: []map[string]any{prompt, toolCall}, open: true, known: true},
		{name: "a turn that ended", entries: []map[string]any{prompt, reply}, known: true},
		{name: "a session that says nothing of a turn", entries: []map[string]any{{"type": "model_change", "provider": "acme", "modelId": "thinker-1"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			home, worktree := t.TempDir(), t.TempDir()
			transcriptLines(t, filepath.Join(piFolder(home, worktree), "2026-10-26T20-04-14-465Z_01a14650-ac01-7127-9115-4f3a806b967f.jsonl"), time.Now(), test.entries...)
			meta := nativeMeta("g1", "pi")
			meta.Worktree = worktree

			// Act
			open, known := (&HostProgress{Home: home}).TurnOpen(context.Background(), meta, EndpointSample{Harness: "pi"})

			// Assert
			if open != test.open || known != test.known {
				t.Fatalf("TurnOpen = open %t known %t, want open %t known %t", open, known, test.open, test.known)
			}
		})
	}
}
