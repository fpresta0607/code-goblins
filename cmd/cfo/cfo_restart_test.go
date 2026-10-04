package main

import (
	"encoding/json"
	"errors"
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

// goblins resume, and cfo resume with no task named, restart a CFO running in
// its native terminal, its screen frozen or not, on its conversation, answer
// its startup dialogs, say so, and end on the same screen as goblins.
func TestGoblinsResumeRestartsARunningCFOOnItsConversation(t *testing.T) {
	for _, c := range []struct {
		name      string
		isGoblins bool
	}{
		{"goblins resume", true},
		{"cfo resume", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.runtime.goblins = c.isGoblins
			f.nativeCFO = supervisor.NativeCFOTerminal
			f.restartSession = "a1b2c3d4-session"
			f.settleNotes = []string{"Answered the update prompt in the CFO's terminal: Skip."}

			// Act
			exit, stdout, stderr := f.launch("resume")

			// Assert
			if exit != 0 || f.restarts != 1 || len(f.nativeStarts)+len(f.cfoStarts) != 0 {
				t.Fatalf("exit=%d restarts=%d nativeStarts=%q cfoStarts=%q stderr=%q, want one restart and no other start", exit, f.restarts, f.nativeStarts, f.cfoStarts, stderr)
			}
			if !strings.Contains(stdout, "CFO        restarted on its conversation a1b2c3d4-session, in native terminal cfo\n") || !strings.Contains(stdout, "Its current response was interrupted; goblins keep running.") {
				t.Errorf("stdout = %q, want the restart said", stdout)
			}
			if !strings.Contains(stdout, f.settleNotes[0]) {
				t.Errorf("stdout = %q, want the restarted CFO's startup dialogs answered and said", stdout)
			}
			if strings.Count(stdout, "CFO        ") != 1 {
				t.Errorf("stdout = %q, want the CFO said once, as restarted", stdout)
			}
			if len(f.screens) != 1 || !strings.HasPrefix(f.screens[0].title, "Your CFO is starting\n") || !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
				t.Errorf("screens=%+v nativeAttached=%q, want the final screen and the CFO's terminal", f.screens, f.nativeAttached)
			}
		})
	}
}

// A restarted CFO whose harness could not resume its conversation runs on a
// new one, and goblins resume says so, naming the conversation it left.
func TestGoblinsResumeSaysTheRestartedCFOStartedANewConversation(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCFO = supervisor.NativeCFOTerminal
	f.restartSession = "a1b2c3d4-session"
	f.resumeEnds = true

	// Act
	exit, stdout, stderr := f.launch("resume")

	// Assert
	if exit != 0 || f.restarts != 1 {
		t.Fatalf("exit=%d restarts=%d stderr=%q, want one restart", exit, f.restarts, stderr)
	}
	if !strings.Contains(stdout, "CFO        restarted as Claude Code on a new conversation, in native terminal cfo\n") || !strings.Contains(stdout, "Its conversation a1b2c3d4-session could not be resumed, so the CFO starts a new one.") {
		t.Errorf("stdout = %q, want the new conversation and the one left said", stdout)
	}
}

// goblins resume with no CFO running brings a closed one back, as goblins
// does: on its conversation, in its native terminal.
func TestGoblinsResumeBringsBackAClosedCFO(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCFOTerminal)

	// Act
	exit, _, stderr := f.launch("resume")

	// Assert
	if exit != 0 || f.restarts != 1 || !slices.Equal(f.nativeArgs, []string{"--resume a1b2c3d4-session"}) {
		t.Fatalf("exit=%d restarts=%d nativeArgs=%q stderr=%q, want the closed CFO brought back on its conversation", exit, f.restarts, f.nativeArgs, stderr)
	}
}

// A restart that would lose the CFO's conversation is refused with its
// reason, the CFO left running and nothing else started; anything after
// goblins resume is refused too.
func TestGoblinsResumeSaysWhyItLeftTheCFORunning(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCFO = supervisor.NativeCFOTerminal
	f.restartErr = errors.New("the CFO in native terminal cfo registered no conversation it can come back on, so it is left running")

	// Act
	exit, _, stderr := f.launch("resume")
	extra, _, extraErr := f.launch("resume", "now")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "so it is left running") || len(f.nativeStarts)+len(f.cfoStarts) != 0 || len(f.screens) != 0 {
		t.Errorf("exit=%d stderr=%q nativeStarts=%q screens=%+v, want the refusal and nothing started or shown", exit, stderr, f.nativeStarts, f.screens)
	}
	if extra != 2 || !strings.Contains(extraErr, "usage: goblins resume") {
		t.Errorf("goblins resume now: exit=%d stderr=%q, want its usage", extra, extraErr)
	}
}

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
	h.State = filepath.Join(h.Root, "state")
	return h
}

// nativeCFORunning starts the test binary as Claude Code in native terminal
// cfo of h, registers it as the home's CFO on conversation session, and
// returns its terminal's record.
func nativeCFORunning(t *testing.T, h home.Home, session string) host.Record {
	t.Helper()
	if err := startNativeCFO(h, h.Root, "claude", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) })
	waitForFakeClaudeEnvironment(t, h.Root)
	record, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	started, alive := proc.StartTime(record.ChildPID)
	if !alive {
		t.Fatalf("the CFO's program, pid %d, is not running", record.ChildPID)
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
	}{Agent: "claude", Host: supervisor.NativeCFOTerminal, Process: lock.Info{PID: record.ChildPID, OwnerPID: record.ChildPID, Start: started, Hostname: hostname, Acquired: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "primary.json"), registration, 0o600); err != nil {
		t.Fatal(err)
	}
	recordConversation(t, h.State, "claude", session, supervisor.NativeCFOTerminal)
	return record
}

// recordConversationOf records session in harness as the conversation of the
// program native terminal cfo runs, as that program's registration does.
func recordConversationOf(t *testing.T, stateDir string, record host.Record, harness, session string) {
	t.Helper()
	data, err := json.Marshal(supervisor.CFOConversation{Harness: harness, Session: session, Host: supervisor.NativeCFOTerminal, PID: record.ChildPID, Updated: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "cfo-conversation.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// waitForFakeClaudeArguments waits for the test binary, run as claude.exe in
// dir, to record arguments other than skip, and returns them.
func waitForFakeClaudeArguments(t *testing.T, dir string, skip []string) []string {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		raw, err := os.ReadFile(filepath.Join(dir, fakeClaudeArguments))
		if err != nil {
			continue
		}
		if got := strings.Split(string(raw), "\n"); !slices.Equal(got, skip) {
			return got
		}
	}
	t.Fatal("the CFO did not start again in native terminal cfo within 15s")
	return nil
}

// A CFO running in native terminal cfo is restarted there on its
// conversation: its harness ends with its terminal, and the terminal runs
// Claude Code again with --resume and that conversation.
func TestRestartCFOStartsTheCFOAgainOnItsConversation(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	before := nativeCFORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, before, "claude", "a1b2c3d4-session")
	if err := supervisor.RecordCFOConversationLeft(h.State, supervisor.CFOConversationLeft{Harness: "claude", Session: "older-session", Resume: []string{"--resume", "older-session"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, resumed, err := restartCFO(h)

	// Assert
	if err != nil || !resumed || conversation.Session != "a1b2c3d4-session" {
		t.Fatalf("restartCFO = %+v, %v, %v; want the conversation resumed", conversation, resumed, err)
	}
	if _, alive := proc.StartTime(before.ChildPID); alive {
		t.Errorf("the CFO's earlier program, pid %d, still runs", before.ChildPID)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, nil); !slices.Equal(got, []string{"--resume", "a1b2c3d4-session"}) {
		t.Errorf("the restarted CFO started with %q, want --resume and its conversation", got)
	}
	if !supervisor.NativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		t.Error("native terminal cfo does not run after the restart")
	}
	if notice := supervisor.CFOConversationLeftNotice(h.State); notice != "" {
		t.Errorf("the board says %q after the CFO came back on its conversation, want nothing", notice)
	}
}

// A restarted CFO whose harness ends at once, as one that cannot resume its
// conversation does, is started again there on a new conversation, so the
// fleet is never left without a CFO.
func TestRestartCFOStartsANewConversationWhenItsOwnCannotBeResumed(t *testing.T) {
	// Arrange
	h := fakeClaudeHome(t)
	before := nativeCFORunning(t, h, "a1b2c3d4-session")
	recordConversationOf(t, h.State, before, "claude", "a1b2c3d4-session")
	if err := os.WriteFile(filepath.Join(h.Root, fakeClaudeResumeEnds), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	conversation, resumed, err := restartCFO(h)

	// Assert
	if err != nil || resumed || conversation.Session != "a1b2c3d4-session" {
		t.Fatalf("restartCFO = %+v, %v, %v; want the conversation it could not resume named", conversation, resumed, err)
	}
	if got := waitForFakeClaudeArguments(t, h.Root, []string{"--resume", "a1b2c3d4-session"}); !slices.Equal(got, []string{""}) {
		t.Errorf("the CFO started again with %q, want a new conversation", got)
	}
	if !supervisor.NativeTerminalRuns(h.State, supervisor.NativeCFOTerminal) {
		t.Error("no CFO runs in native terminal cfo after the restart")
	}
	if notice := supervisor.CFOConversationLeftNotice(h.State); !strings.Contains(notice, "The CFO's conversation a1b2c3d4-session could not be resumed") || !strings.Contains(notice, "claude --resume a1b2c3d4-session") {
		t.Errorf("the board says %q, want the conversation it could not resume named with how to resume it by hand", notice)
	}
}

// A CFO whose conversation was not recorded for the program its terminal
// runs, or cannot be resumed, is left running, with why, and so is one asked
// to restart from inside its own terminal, which closing would end along with
// the restart: a restart never stops what it cannot bring back.
func TestRestartCFOLeavesACFOItCannotBringBackRunning(t *testing.T) {
	for _, c := range []struct {
		name, harness, hostID string
		isRecorded            bool
		why                   string
	}{
		{"a conversation another process registered", "claude", "", false, "registered no conversation it can come back on, so it is left running"},
		{"a pi conversation", "pi", "", true, "pi has no way to resume a conversation, so the CFO is left running"},
		{"a restart run inside the CFO's own terminal", "claude", supervisor.NativeCFOTerminal, true, "so the CFO is left running: run goblins resume in another terminal, or from the board"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := fakeClaudeHome(t)
			record := nativeCFORunning(t, h, "a1b2c3d4-session")
			if c.isRecorded {
				recordConversationOf(t, h.State, record, c.harness, "a1b2c3d4-session")
			}
			conversation, err := os.ReadFile(filepath.Join(h.State, "cfo-conversation.json"))
			if err != nil {
				t.Fatal(err)
			}
			if c.hostID != "" {
				t.Setenv(host.IDVariable, c.hostID)
			}

			// Act
			_, _, err = restartCFO(h)

			// Assert
			if err == nil || !strings.Contains(err.Error(), c.why) || strings.Contains(err.Error(), "starts a new one") {
				t.Fatalf("restartCFO error = %v, want %q and the CFO left running", err, c.why)
			}
			if _, alive := proc.StartTime(record.ChildPID); !alive || !host.Running(record) {
				t.Errorf("the CFO, pid %d, was stopped", record.ChildPID)
			}
			if after, err := os.ReadFile(filepath.Join(h.State, "cfo-conversation.json")); err != nil || string(after) != string(conversation) {
				t.Errorf("the CFO's conversation record = %q, %v after the refusal, want it as it was, %q", after, err, conversation)
			}
		})
	}
}
