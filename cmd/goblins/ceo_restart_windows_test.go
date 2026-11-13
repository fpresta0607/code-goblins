package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// fakeClaudeHome is a home whose PATH finds only the test binary, as
// claude.exe.
func fakeClaudeHome(t *testing.T) home.Home {
	t.Helper()
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(bin, "claude.exe"))
	t.Setenv("PATH", bin)
	h := home.Home{Root: t.TempDir()}
	useTranscriptHome(t, h.Root)
	h.State = filepath.Join(h.Root, "state")
	return h
}

// nativeCEORunning starts the test binary as Claude Code in native terminal
// ceo of h, registers it as the home's CEO on conversation session, and
// returns its terminal's record.
func nativeCEORunning(t *testing.T, h home.Home, session string) host.Record {
	t.Helper()
	if err := startNativeCEO(h, h.Root, "claude", nil, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCEOTerminal) })
	waitForFakeClaudeEnvironment(t, h.Root)
	record, err := host.ReadRecord(h.State, supervisor.NativeCEOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	registerRestartCEO(t, h, record, "claude", session)
	// A CEO at work has begun a turn, so Claude Code has written its
	// transcript.
	claudeTranscript(t, h.Root, session, 1<<10)
	return record
}

func registerRestartCEO(t *testing.T, h home.Home, record host.Record, harness, session string) {
	t.Helper()
	started, alive := proc.StartTime(record.ChildPID)
	if !alive {
		t.Fatalf("the CEO's program, pid %d, is not running", record.ChildPID)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	registration, err := json.Marshal(struct {
		Target    herdr.Target `json:"target"`
		Workspace string       `json:"workspace"`
		Tab       string       `json:"tab"`
		Agent     string       `json:"agent"`
		Terminal  string       `json:"terminal"`
		Host      string       `json:"host,omitempty"`
		Process   lock.Info    `json:"process"`
	}{Agent: harness, Host: supervisor.NativeCEOTerminal, Process: lock.Info{PID: record.ChildPID, OwnerPID: record.ChildPID, Start: started, Hostname: hostname, Acquired: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), registration, 0o600); err != nil {
		t.Fatal(err)
	}
	recordConversationOf(t, h.State, record, harness, session)
}

// recordConversationOf records session in harness as the conversation of the
// program native terminal ceo runs, as that program's registration does.
func recordConversationOf(t *testing.T, stateDir string, record host.Record, harness, session string) {
	t.Helper()
	data, err := json.Marshal(supervisor.CEOConversation{Harness: harness, Session: session, Host: supervisor.NativeCEOTerminal, PID: record.ChildPID, Updated: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "ceo-conversation.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A CEO running in native terminal ceo is restarted there on its
// conversation: its harness ends with its terminal, and the terminal runs
// Claude Code again with --resume and that conversation.
func TestRestartCEOStartsTheCEOAgainOnItsConversation(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	before := nativeCEORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, before, "claude", "a1b2c3d4-session")
	if err := supervisor.RecordCEOConversationLeft(h.State, supervisor.CEOConversationLeft{Harness: "claude", Session: "older-session", Resume: []string{"--resume", "older-session"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, fresh, err := restartCEO(h, "")

	// Assert
	if err != nil || fresh != "" || conversation.Session != "a1b2c3d4-session" {
		t.Fatalf("restartCEO = %+v, %q, %v; want the conversation resumed", conversation, fresh, err)
	}
	if _, alive := proc.StartTime(before.ChildPID); alive {
		t.Errorf("the CEO's earlier program, pid %d, still runs", before.ChildPID)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, nil); !slices.Equal(got, []string{"--resume", "a1b2c3d4-session", ceoBackPrompt("claude", restartedWhy, "", true)}) {
		t.Errorf("the restarted CEO started with %q, want --resume and its conversation, then why it started again", got)
	}
	if !supervisor.NativeTerminalRuns(h.State, supervisor.NativeCEOTerminal) {
		t.Error("native terminal ceo does not run after the restart")
	}
	if notice := supervisor.CEOConversationLeftNotice(h.State); notice != "" {
		t.Errorf("the board says %q after the CEO came back on its conversation, want nothing", notice)
	}
}

// Issue #1582: a CEO started as claude over a saved codex engine came back
// from a harness Update, which restarts it naming no harness, as codex on a
// new conversation. A restart that names no harness keeps the harness the CEO
// runs, on its conversation, and a later start that names none keeps it too.
func TestRestartCEONamingNoHarnessKeepsTheHarnessAnExplicitStartChose(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	selection := `{"harness":"codex","model":"selected-model","effort":"xhigh"}`
	if err := os.WriteFile(filepath.Join(h.State, "ceo-engine.json"), []byte(selection), 0o600); err != nil {
		t.Fatal(err)
	}
	before := nativeCEORunning(t, h, "a1b2c3d4-session")
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, fresh, err := restartCEO(h, "")

	// Assert
	if err != nil || fresh != "" || conversation.Harness != "claude" || conversation.Session != "a1b2c3d4-session" {
		t.Fatalf("restartCEO = %+v, %q, %v; want the claude CEO resumed on its conversation", conversation, fresh, err)
	}
	if _, alive := proc.StartTime(before.ChildPID); alive {
		t.Errorf("the CEO's earlier program, pid %d, still runs", before.ChildPID)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, nil); !slices.Equal(got, []string{"--resume", "a1b2c3d4-session", ceoBackPrompt("claude", restartedWhy, "", true)}) {
		t.Errorf("the restarted CEO started with %q, want --resume and its conversation, without the codex selection's flags", got)
	}
	if harness, err := ceoHarness(h.State); err != nil || harness != "claude" {
		t.Errorf("the next start that names no harness starts as %q (%v), want claude", harness, err)
	}
	if choice, err := supervisor.ReadCEOEngine(h.State); err != nil || choice.Harness != "codex" || choice.Model != "selected-model" || choice.Effort != "xhigh" {
		t.Errorf("saved selection = %+v, %v; want the codex selection kept for when the CEO runs as codex again", choice, err)
	}
}

// A restarted CEO whose harness ends at once, as one that cannot resume its
// conversation does, is started again there on a new conversation, so the
// fleet is never left without a CEO.
func TestRestartCEOStartsANewConversationWhenItsOwnCannotBeResumed(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	before := nativeCEORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, before, "claude", "a1b2c3d4-session")
	if err := os.WriteFile(filepath.Join(h.Root, fakeClaudeResumeEnds), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, fresh, err := restartCEO(h, "")

	// Assert
	if err != nil || fresh != "Its conversation a1b2c3d4-session could not be resumed, so the CEO starts a new one." || conversation.Session != "a1b2c3d4-session" {
		t.Fatalf("restartCEO = %+v, %q, %v; want the conversation it could not resume named", conversation, fresh, err)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, []string{"--resume", "a1b2c3d4-session", ceoBackPrompt("claude", restartedWhy, "", true)}); !slices.Equal(got, []string{ceoBackPrompt("claude", restartedWhy, fresh, false)}) {
		t.Errorf("the CEO started again with %q, want a new conversation, told why", got)
	}
	if !supervisor.NativeTerminalRuns(h.State, supervisor.NativeCEOTerminal) {
		t.Error("no CEO runs in native terminal ceo after the restart")
	}
	if notice := supervisor.CEOConversationLeftNotice(h.State); !strings.Contains(notice, "The CEO's conversation a1b2c3d4-session could not be resumed") || !strings.Contains(notice, "claude --resume a1b2c3d4-session") {
		t.Errorf("the board says %q, want the conversation it could not resume named with how to resume it by hand", notice)
	}
}

// The Overlord, 2026-10-09, after Restart the CEO refused with "The CEO could
// not be restarted: Its last conversation is 33…": "this makes no sense".
// Restart always restarts the CEO: a conversation past the context a CEO
// resumes is left as it is, and the CEO starts again in its terminal on a new one,
// which takes the home's digest as a CEO reopened after a close does, and
// says why.
func TestRestartCEOOnAConversationOverTheLimitStartsANewOne(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	before := nativeCEORunning(t, h, "large-session")
	recordConversationOf(t, h.State, before, "claude", "large-session")
	writeClaudeTranscript(t, h.Root, "large-session", requestLine(6, 2_300, 800_000, 4))
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, fresh, err := restartCEO(h, "")

	// Assert
	if err != nil || conversation.Session != "large-session" || fresh != "Its last conversation holds 802K tokens of context, past the 400K a CEO resumes, so the CEO starts a new one." {
		t.Fatalf("restartCEO = %+v, %q, %v; want the CEO restarted on a new conversation, with why", conversation, fresh, err)
	}
	if _, alive := proc.StartTime(before.ChildPID); alive {
		t.Errorf("the CEO's earlier program, pid %d, still runs", before.ChildPID)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, nil); !slices.Equal(got, []string{ceoBackPrompt("claude", restartedWhy, fresh, false)}) {
		t.Errorf("the restarted CEO started with %q, want a new conversation, told why", got)
	}
	if !supervisor.NativeTerminalRuns(h.State, supervisor.NativeCEOTerminal) {
		t.Error("native terminal ceo does not run after the restart")
	}
}

// A CEO whose terminal runs a program its registered conversation is not the
// conversation of is restarted all the same, on a new conversation, and
// never on that one: a conversation another process registered, one older
// than the program, one with no registration time and one of another
// harness.
func TestRestartCEONeverResumesAConversationNotOwnedByItsRunningHarness(t *testing.T) {
	for _, testCase := range []struct {
		name, harness      string
		isBeforeChildStart bool
		hasUpdate          bool
		pid                int
	}{
		{"a conversation another process registered", "claude", false, true, 1},
		{"a conversation predating the current child", "claude", true, true, 0},
		{"a conversation without a registration time", "claude", false, false, 0},
		{"a conversation naming another supported harness", "codex", false, true, 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			h := fakeClaudeHome(t)
			record := nativeCEORunning(t, h, "current-session")
			updated := record.ChildStart
			if testCase.isBeforeChildStart {
				updated = updated.Add(-time.Nanosecond)
			}
			if !testCase.hasUpdate {
				updated = time.Time{}
			}
			pid := record.ChildPID + testCase.pid
			conversation := supervisor.CEOConversation{Harness: testCase.harness, Session: "earlier-session", Host: record.ID, PID: pid, Updated: updated}
			conversationBytes, err := json.Marshal(conversation)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.State, "ceo-conversation.json"), conversationBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
				t.Fatal(err)
			}

			// Act
			_, fresh, err := restartCEO(h, "")

			// Assert
			if err != nil || fresh != "The CEO in native terminal ceo registered no conversation it can come back on, so the CEO starts a new one." {
				t.Fatalf("restartCEO = %q, %v; want the CEO restarted on a new conversation, with why", fresh, err)
			}
			if _, alive := proc.StartTime(record.ChildPID); alive {
				t.Errorf("the CEO's earlier program, pid %d, still runs", record.ChildPID)
			}
			if got := waitForFakeClaudeArguments(t, h.Root, nil); !slices.Equal(got, []string{ceoBackPrompt("claude", restartedWhy, fresh, false)}) {
				t.Errorf("the restarted CEO started with %q, want a new conversation, told why, and never earlier-session", got)
			}
		})
	}
}

// A restart asked for from inside the CEO's own terminal, which closing would
// end along with the restart, leaves the CEO running, with why: a restart
// never stops what it cannot bring back.
func TestRestartCEOLeavesACEOItCannotBringBackRunning(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	record := nativeCEORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, record, "claude", "a1b2c3d4-session")
	conversation, err := os.ReadFile(filepath.Join(h.State, "ceo-conversation.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.IDVariable, supervisor.NativeCEOTerminal)

	// Act
	_, _, err = restartCEO(h, "")

	// Assert
	if why := "so the CEO is left running: run goblins resume in another terminal, or from the board"; err == nil || !strings.Contains(err.Error(), why) {
		t.Fatalf("restartCEO error = %v, want %q and the CEO left running", err, why)
	}
	if _, alive := proc.StartTime(record.ChildPID); !alive || !host.Running(record) {
		t.Errorf("the CEO, pid %d, was stopped", record.ChildPID)
	}
	if after, err := os.ReadFile(filepath.Join(h.State, "ceo-conversation.json")); err != nil || string(after) != string(conversation) {
		t.Errorf("the CEO's conversation record = %q, %v after the refusal, want it as it was, %q", after, err, conversation)
	}
}

// A restart never closes a CEO it cannot start again: with the CEO's program
// gone from the restarting process's PATH, the CEO is left running, with why.
func TestRestartCEOLeavesTheCEORunningWhenItsProgramCannotBeFound(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	record := nativeCEORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, record, "claude", "a1b2c3d4-session")
	t.Setenv("PATH", t.TempDir())

	// Act
	_, _, err := restartCEO(h, "")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "so the CEO is left running") {
		t.Fatalf("restartCEO error = %v, want the CEO left running", err)
	}
	if _, alive := proc.StartTime(record.ChildPID); !alive || !host.Running(record) {
		t.Errorf("the CEO, pid %d, was stopped", record.ChildPID)
	}
}
