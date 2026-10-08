package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
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

// The board's Reopen brings a closed CFO back as goblins does: as the agent
// the home remembers, in native terminal cfo, on the conversation it last
// registered with where its harness resumes one, on a new one when the
// resumed terminal does not hold, and on a new one when there is none.
func TestReopenBringsAClosedCFOBackAsGoblinsDoes(t *testing.T) {
	type start struct {
		project, harness string
		args             []string
	}
	for _, tc := range []struct {
		name, remembered, conversation string
		holds                          bool
		want                           []start
	}{
		{"on its conversation", "claude", "a1b2c3d4-session", true, []start{{"", "claude", []string{"--resume", "a1b2c3d4-session"}}}},
		{"on a new one when the resumed terminal does not hold", "claude", "a1b2c3d4-session", false, []start{{"", "claude", []string{"--resume", "a1b2c3d4-session"}}, {"", "claude", nil}}},
		{"on a new one when it registered no conversation", "claude", "", true, []start{{"", "claude", nil}}},
		{"as the remembered agent, which a conversation of another harness does not resume", "codex", "a1b2c3d4-session", true, []start{{"", "codex", nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			if err := os.WriteFile(cfoHarnessPath(f.home.State), []byte(tc.remembered+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.conversation != "" {
				recordConversation(t, f.home.State, "claude", tc.conversation, supervisor.NativeCFOTerminal)
			}
			var starts []start
			started := func(_ home.Home, project, harness string, args []string) error {
				starts = append(starts, start{project, harness, args})
				return nil
			}

			// Act
			err := reopenCFO(f.home, started, func(string, string) bool { return len(starts) > 0 && tc.holds })

			// Assert
			for i := range tc.want {
				tc.want[i].project = f.home.Root
			}
			if err != nil || !slices.EqualFunc(starts, tc.want, func(a, b start) bool {
				return a.project == b.project && a.harness == b.harness && slices.Equal(a.args, b.args)
			}) {
				t.Fatalf("reopenCFO = %v, started %+v; want %+v", err, starts, tc.want)
			}
		})
	}
}

func TestReopenReportsTheConversationItActuallyCouldNotResume(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		isResumeHeld bool
		isStartError bool
		wantSession  string
	}{
		{name: "successful resume clears the old notice", isResumeHeld: true},
		{name: "new conversation names the failed resume", wantSession: "current-session"},
		{name: "failed fresh start preserves the old notice", isStartError: true, wantSession: "older-session"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			fixture := newSessionFixture(t)
			if err := os.WriteFile(cfoHarnessPath(fixture.home.State), []byte("claude\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			recordConversation(t, fixture.home.State, "claude", "current-session", supervisor.NativeCFOTerminal)
			if err := supervisor.RecordCFOConversationLeft(fixture.home.State, supervisor.CFOConversationLeft{Harness: "claude", Session: "older-session", Resume: []string{"--resume", "older-session"}}); err != nil {
				t.Fatal(err)
			}
			startError := errors.New("fresh start failed")
			isStarted := false
			start := func(_ home.Home, _, _ string, args []string) error {
				if len(args) == 0 && testCase.isStartError {
					return startError
				}
				isStarted = true
				return nil
			}

			// Act
			err := reopenCFO(fixture.home, start, func(string, string) bool { return isStarted && testCase.isResumeHeld })

			// Assert
			if testCase.isStartError != errors.Is(err, startError) || !testCase.isStartError && err != nil {
				t.Fatalf("reopen error=%v, want fresh start error=%v", err, testCase.isStartError)
			}
			notice := supervisor.CFOConversationLeftNotice(fixture.home.State)
			if testCase.wantSession == "" {
				if notice != "" {
					t.Fatalf("resumed its current conversation but kept notice %q", notice)
				}
			} else if !strings.Contains(notice, "claude --resume "+testCase.wantSession) {
				t.Fatalf("notice=%q, want the actual retained conversation %s", notice, testCase.wantSession)
			}
		})
	}
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
	if exit != 0 || !slices.Equal(f.nativeStarts, []string{f.home.Root}) || !slices.Equal(f.nativeArgs, []string{"--resume a1b2c3d4-session"}) {
		t.Fatalf("exit=%d nativeStarts=%q nativeArgs=%q stderr=%q, want one native start resuming the conversation", exit, f.nativeStarts, f.nativeArgs, stderr)
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

// The board names the conversation a closed CFO could not resume, as it
// does for a CFO restarted by goblins resume, and forgets an earlier one once
// the CFO comes back on its conversation.
func TestTheBoardNamesTheConversationAClosedCFOCouldNotResume(t *testing.T) {
	for _, c := range []struct {
		name         string
		isResumeEnds bool
		want         string
	}{
		{"a conversation that could not be resumed", true, "The CFO's conversation a1b2c3d4-session could not be resumed"},
		{"a conversation resumed", false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			f.resumeEnds = c.isResumeEnds
			if err := supervisor.RecordCFOConversationLeft(f.home.State, supervisor.CFOConversationLeft{Harness: "claude", Session: "older-session", Resume: []string{"--resume", "older-session"}}); err != nil {
				t.Fatal(err)
			}
			recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCFOTerminal)

			// Act
			exit, _, stderr := f.launch()

			// Assert
			if exit != 0 {
				t.Fatalf("exit=%d stderr=%q, want the CFO back", exit, stderr)
			}
			notice := supervisor.CFOConversationLeftNotice(f.home.State)
			if (c.want == "" && notice != "") || !strings.Contains(notice, c.want) {
				t.Errorf("the board says %q, want %q", notice, c.want)
			}
		})
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
			exit, stdout, stderr := f.launch()

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

// Each harness resumes with its own arguments. A CFO that ran as another
// harness, as pi, which cannot resume, or in Herdr starts a new conversation
// in its native terminal; only pi's case has something to say.
func TestACFOResumesOnlyAConversationItsHarnessCanResume(t *testing.T) {
	for _, c := range []struct {
		name, harness, host, agent string
		args                       []string
		said                       string
	}{
		{"Codex in its native terminal", "codex", supervisor.NativeCFOTerminal, "codex", []string{"resume a1b2c3d4-session"}, "CFO        back as Codex on its conversation a1b2c3d4-session"},
		{"another harness than the one chosen", "codex", supervisor.NativeCFOTerminal, "claude", []string{""}, "CFO        started as Claude Code in "},
		{"a CFO that ran in Herdr", "claude", "", "claude", []string{""}, "CFO        started as Claude Code in "},
		{"pi", "pi", supervisor.NativeCFOTerminal, "pi", []string{""}, "pi has no way to resume a conversation, so the CFO starts a new one."},
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
			if exit != 0 || !slices.Equal(f.nativeArgs, c.args) {
				t.Fatalf("exit=%d nativeArgs=%q stderr=%q, want native args %q", exit, f.nativeArgs, stderr, c.args)
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
	if exit != 0 || !slices.Equal(f.nativeArgs, []string{""}) {
		t.Fatalf("exit=%d nativeArgs=%q stderr=%q, want a new conversation in its native terminal and no resume", exit, f.nativeArgs, stderr)
	}
	if !strings.Contains(stdout, "Its last conversation is 21 MB, past the 20 MB a CFO resumes, so the CFO starts a new one.") {
		t.Errorf("stdout = %q, want why the conversation was not resumed", stdout)
	}
}
