package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// recordConversation records the conversation the home's CFO last registered
// with, as its registration does.
func recordConversation(t *testing.T, stateDir, harness, session, host string) {
	t.Helper()
	data, err := json.Marshal(supervisor.CFOConversation{Harness: harness, Session: session, Host: host, PID: 4242, Updated: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "cfo-conversation.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// noResumeWait makes the look at a resumed CFO immediate.
func noResumeWait(t *testing.T) {
	t.Helper()
	wait := cfoResumeWait
	cfoResumeWait = func(time.Duration) {}
	t.Cleanup(func() { cfoResumeWait = wait })
}

// The CFO was closed, however it ended. goblins brings it back on the same
// conversation, in the native terminal it ran in, with nothing to run or
// relink: Claude Code with --resume and its session.
func TestAClosedCFOComesBackOnItsConversation(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCFOTerminal)

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeStarts, []string{f.home.Root}) || !slices.Equal(f.nativeArgs, []string{"--resume a1b2c3d4-session"}) || len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q nativeArgs=%q cfoStarts=%q stderr=%q, want one native start resuming the conversation", exit, f.nativeStarts, f.nativeArgs, f.cfoStarts, stderr)
	}
	if !strings.Contains(stdout, "CFO        back as Claude Code on its conversation a1b2c3d4-session, in native terminal cfo") {
		t.Errorf("stdout = %q, want it to say the CFO comes back on its conversation", stdout)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
		t.Errorf("native terminals shown = %q, want the CFO's", f.nativeAttached)
	}
}

// A conversation the harness cannot resume ends the harness at once; the CFO
// then starts on a new conversation and says so, rather than leaving its
// terminal ended.
func TestACFOWhoseConversationCannotBeResumedStartsANewOne(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	f.resumeEnds = true
	recordConversation(t, f.home.State, "claude", "gone-session", supervisor.NativeCFOTerminal)

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeArgs, []string{"--resume gone-session", ""}) {
		t.Fatalf("exit=%d nativeArgs=%q stderr=%q, want a resume, then a new conversation", exit, f.nativeArgs, stderr)
	}
	if !strings.Contains(stdout, "Its conversation gone-session could not be resumed, so the CFO starts a new one.") || !strings.Contains(stdout, "CFO        started as Claude Code in "+f.home.Root+", in native terminal cfo") {
		t.Errorf("stdout = %q, want the failed resume and the new start said", stdout)
	}
}

func TestAResumedCFOThatEndsDuringStartupSettlingStartsANewConversation(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			f.agent = agent
			recordConversation(t, f.home.State, agent, "old-conversation", supervisor.NativeCFOTerminal)
			settles := 0
			isFreshRunning := false
			f.runtime.settleCFO = func(context.Context, string, string) []string {
				settles++
				isFreshRunning = settles > 1
				f.cfoTerminalRuns = isFreshRunning
				return nil
			}

			// Act
			exit, stdout, stderr := f.launch()

			// Assert
			if exit != 0 || len(f.nativeArgs) != 2 || f.nativeArgs[1] != "" || settles != 2 || !isFreshRunning {
				t.Fatalf("exit=%d starts=%q settles=%d runs=%v stderr=%q, want a failed resume followed by a running fresh CFO", exit, f.nativeArgs, settles, isFreshRunning, stderr)
			}
			if !strings.Contains(stdout, "Its conversation old-conversation could not be resumed, so the CFO starts a new one.") || strings.Contains(stdout, "CFO        back as") {
				t.Fatalf("stdout=%q, want the old conversation and fresh start stated without claiming resume succeeded", stdout)
			}
		})
	}
}

func TestANewNativeCFOThatEndsDuringStartupSettlingReportsFailure(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "pi"} {
		t.Run(agent, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.agent = agent
			f.runtime.settleCFO = func(context.Context, string, string) []string {
				f.cfoTerminalRuns = false
				return nil
			}

			// Act
			exit, stdout, stderr := f.launch("--native")

			// Assert
			if exit != 1 || !strings.Contains(stderr, "the CFO's native terminal ended during startup") || len(f.nativeAttached) != 0 || len(f.screens) != 0 {
				t.Fatalf("exit=%d attached=%q screens=%v stdout=%q stderr=%q, want the ended CFO reported before the final screen", exit, f.nativeAttached, f.screens, stdout, stderr)
			}
		})
	}
}

func TestAFailedResumeAndFreshStartNameTheConversationThatWasLeft(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	f.agent = "codex"
	f.resumeEnds = true
	recordConversation(t, f.home.State, "codex", "old-conversation", supervisor.NativeCFOTerminal)
	f.runtime.settleCFO = func(context.Context, string, string) []string {
		f.cfoTerminalRuns = false
		return []string{"Answered the directory trust prompt in the CFO's terminal: 1. Yes, continue."}
	}

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "the CFO's native terminal ended during startup") || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d attached=%q stderr=%q, want the failed fresh start reported", exit, f.nativeAttached, stderr)
	}
	if !strings.Contains(stdout, "Its conversation old-conversation could not be resumed") || !strings.Contains(stdout, "Answered the directory trust prompt") {
		t.Fatalf("stdout=%q, want the old conversation and answered startup dialog preserved in the failure output", stdout)
	}
}

// Each harness resumes with its own arguments. A CFO that ran in its native
// terminal comes back in it on a new conversation when it ran as another
// harness or as pi, which cannot resume, and one that ran in Herdr starts a
// new one there; only pi's case has something to say.
func TestACFOResumesOnlyAConversationItsHarnessCanResume(t *testing.T) {
	for _, c := range []struct {
		name, harness, host, agent string
		args                       []string
		herdr                      bool
		said                       string
	}{
		{"Codex in its native terminal", "codex", supervisor.NativeCFOTerminal, "codex", []string{"resume a1b2c3d4-session"}, false, "CFO        back as Codex on its conversation a1b2c3d4-session"},
		{"another harness than the one chosen", "codex", supervisor.NativeCFOTerminal, "claude", []string{""}, false, "CFO        started as Claude Code in "},
		{"a CFO that ran in Herdr", "claude", "", "claude", nil, true, "CFO        started as Claude Code in "},
		{"pi", "pi", supervisor.NativeCFOTerminal, "pi", []string{""}, false, "pi has no way to resume a conversation, so the CFO starts a new one."},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			f.agent = c.agent
			recordConversation(t, f.home.State, c.harness, "a1b2c3d4-session", c.host)

			// Act
			exit, stdout, stderr := f.launch()

			// Assert
			if exit != 0 || !slices.Equal(f.nativeArgs, c.args) || (len(f.cfoStarts) == 1) != c.herdr {
				t.Fatalf("exit=%d nativeArgs=%q cfoStarts=%q stderr=%q, want native args %q and a Herdr start %v", exit, f.nativeArgs, f.cfoStarts, stderr, c.args, c.herdr)
			}
			if !strings.Contains(stdout, c.said) {
				t.Errorf("stdout = %q, want %q", stdout, c.said)
			}
		})
	}
}

// A conversation past the size a CFO resumes is not resumed: the Overlord's
// rule keeps CFO sessions small, so the CFO starts a new one and says why.
func TestACFOWhoseConversationIsTooLargeStartsANewOne(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	userHome := t.TempDir()
	t.Setenv("USERPROFILE", userHome)
	recordConversation(t, f.home.State, "claude", "large-session", supervisor.NativeCFOTerminal)
	folder := filepath.Join(userHome, ".claude", "projects", claudeProjectFolder.ReplaceAllString(f.home.Root, "-"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(folder, "large-session.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(transcript, cfoTranscriptLimit+1<<20); err != nil {
		t.Fatal(err)
	}

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeArgs, []string{""}) || len(f.cfoStarts) != 0 {
		t.Fatalf("exit=%d nativeArgs=%q cfoStarts=%q stderr=%q, want a new conversation in its native terminal and no resume", exit, f.nativeArgs, f.cfoStarts, stderr)
	}
	if !strings.Contains(stdout, "Its last conversation is 21 MB, past the 20 MB a CFO resumes, so the CFO starts a new one.") {
		t.Errorf("stdout = %q, want why the conversation was not resumed", stdout)
	}
}
