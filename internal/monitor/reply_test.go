package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A native Claude Code goblin's last reply is the text that ended its last
// turn, read from the newest conversation Claude Code keeps for its worktree,
// and nothing while a turn is still running or ended on a tool call.
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
			got := (&HostProgress{Home: home}).LastReply(context.Background(), meta, EndpointSample{Harness: "claude"})

			// Assert
			if got != test.want {
				t.Errorf("reply = %q, want %q", got, test.want)
			}
		})
	}
}

// The goblin's current conversation is its folder's newest, and a Herdr
// goblin's is the session Herdr names, wherever Claude Code keeps it.
func TestTheClaudeConversationReadIsTheGoblinsCurrentOne(t *testing.T) {
	// Arrange
	home, cwd := t.TempDir(), t.TempDir()
	folder := claudeProjectFolder(home, cwd)
	written := time.Now()
	transcriptLines(t, filepath.Join(folder, "older.jsonl"), written.Add(-time.Hour), claudeEntry("assistant", cwd, claudeText("Should I open the PR?")))
	transcriptLines(t, filepath.Join(folder, "newer.jsonl"), written, claudeEntry("assistant", cwd, claudeText("PR 12 is open.")))
	transcriptLines(t, filepath.Join(home, ".claude", "projects", "elsewhere", "herdr-session.jsonl"), written.Add(-2*time.Hour), claudeEntry("assistant", `C:\elsewhere`, claudeText("Which layout do you want?")))
	prober := &HostProgress{Home: home}

	// Act
	meta := nativeMeta("g1", "claude")
	meta.Worktree = cwd
	native := prober.LastReply(context.Background(), meta, EndpointSample{Harness: "claude"})
	herdr := prober.LastReply(context.Background(), metaFor("g1"), EndpointSample{Harness: "claude", Session: "herdr-session"})

	// Assert
	if native != "PR 12 is open." {
		t.Errorf("native reply = %q, want the newest conversation's", native)
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

// pi's reply is read off its screen, the rows above the editor, since its
// transcript is not one this reads.
func TestAPiGoblinsLastReplyIsReadFromItsScreen(t *testing.T) {
	// Arrange
	rule := strings.Repeat("─", 40)
	screen := strings.Join([]string{
		"The tests pass and the branch is pushed.",
		"",
		"I'll open the pull request next unless you want",
		"the screenshots first.",
		rule,
		"",
		rule,
		"↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)",
	}, "\n")

	// Act
	got := (&HostProgress{}).LastReply(context.Background(), state.TaskMeta{ID: "g1", Harness: "pi"}, EndpointSample{Harness: "pi", Capture: []byte(screen)})

	// Assert
	if want := "The tests pass and the branch is pushed.\n\nI'll open the pull request next unless you want the screenshots first."; got != want {
		t.Errorf("reply = %q, want %q", got, want)
	}
}
