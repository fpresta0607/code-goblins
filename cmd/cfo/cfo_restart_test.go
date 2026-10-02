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

// goblins resume restarts a CFO running in its native terminal, its screen
// frozen or not, on its conversation, says so, and ends on the same screen
// as goblins.
func TestGoblinsResumeRestartsARunningCFOOnItsConversation(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCFO = supervisor.NativeCFOTerminal
	f.restartSession = "a1b2c3d4-session"

	// Act
	exit, stdout, stderr := f.launch("resume")

	// Assert
	if exit != 0 || f.restarts != 1 || len(f.nativeStarts)+len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d restarts=%d nativeStarts=%q cfoStarts=%q stderr=%q, want one restart and no other start", exit, f.restarts, f.nativeStarts, f.cfoStarts, stderr)
	}
	if !strings.Contains(stdout, "The CFO restarts in native terminal cfo on its conversation a1b2c3d4-session. Its current response was interrupted; goblins keep running.") {
		t.Errorf("stdout = %q, want the restart said", stdout)
	}
	if len(f.screens) != 1 || !strings.HasPrefix(f.screens[0].title, "Your CFO is starting\n") || !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
		t.Errorf("screens=%+v nativeAttached=%q, want the final screen and the CFO's terminal", f.screens, f.nativeAttached)
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
// resume is refused too.
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

// A CFO running in native terminal cfo is restarted there on its
// conversation: its harness ends with its terminal, and the terminal runs
// Claude Code again with --resume and that conversation.
func TestRestartCFOStartsTheCFOAgainOnItsConversation(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(bin, "claude.exe"))
	t.Setenv("PATH", bin)
	h := home.Home{Root: t.TempDir()}
	h.State = filepath.Join(h.Root, "state")
	before := nativeCFORunning(t, h, "a1b2c3d4-session")
	data, err := os.ReadFile(filepath.Join(h.State, "cfo-conversation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var conversation supervisor.CFOConversation
	if err := json.Unmarshal(data, &conversation); err != nil {
		t.Fatal(err)
	}
	conversation.PID = before.ChildPID
	if data, err = json.Marshal(conversation); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.State, "cfo-conversation.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.Root, fakeClaudeArguments)); err != nil {
		t.Fatal(err)
	}

	// Act
	session, err := restartCFO(h)

	// Assert
	if err != nil || session != "a1b2c3d4-session" {
		t.Fatalf("restartCFO = %q, %v; want the conversation restarted", session, err)
	}
	if _, alive := proc.StartTime(before.ChildPID); alive {
		t.Errorf("the CFO's earlier program, pid %d, still runs", before.ChildPID)
	}
	arguments := filepath.Join(h.Root, fakeClaudeArguments)
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if raw, err := os.ReadFile(arguments); err == nil && len(raw) > 0 {
			if got := strings.Split(string(raw), "\n"); !slices.Equal(got, []string{"--resume", "a1b2c3d4-session"}) {
				t.Errorf("the restarted CFO started with %q, want --resume and its conversation", got)
			}
			return
		}
	}
	t.Fatal("the CFO did not start again in native terminal cfo within 15s")
}

// A CFO whose conversation was not recorded for the program its terminal
// runs is left running: a restart never stops what it cannot bring back.
func TestRestartCFOLeavesACFOItCannotBringBackRunning(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(bin, "claude.exe"))
	t.Setenv("PATH", bin)
	h := home.Home{Root: t.TempDir()}
	h.State = filepath.Join(h.Root, "state")
	record := nativeCFORunning(t, h, "a1b2c3d4-session")

	// Act
	_, err = restartCFO(h)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "so it is left running") {
		t.Fatalf("restartCFO error = %v, want the CFO left running", err)
	}
	if _, alive := proc.StartTime(record.ChildPID); !alive || !host.Running(record) {
		t.Errorf("the CFO, pid %d, was stopped", record.ChildPID)
	}
}
