package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// claudeTranscript writes Claude Code's transcript of session, a
// conversation the CFO of the home rooted at root held, size bytes long,
// under the test's own user home, as Claude Code writes it once the
// conversation's first turn begins, and returns its path.
func claudeTranscript(t *testing.T, root, session string, size int64) string {
	t.Helper()
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.ToLower(userHome), strings.ToLower(os.TempDir())) {
		t.Fatalf("the user home %s is not the test's own", userHome)
	}
	folder := filepath.Join(userHome, ".claude", "projects", claudeProjectFolder.ReplaceAllString(root, "-"))
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(folder, session+".jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(transcript, size); err != nil {
		t.Fatal(err)
	}
	return transcript
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
// resumed terminal does not hold, and on a new one when there is none. A CFO
// that ran before is told why it started again and on which conversation; a
// home's first CFO waits for the Overlord.
func TestReopenBringsAClosedCFOBackAsGoblinsDoes(t *testing.T) {
	type start struct {
		project, harness string
		args             []string
		prompt           string
	}
	const why = "the Overlord reopened it from the board"
	unresumed := "You start on a new conversation. Your conversation a1b2c3d4-session could not be resumed."
	for _, tc := range []struct {
		name, remembered, conversation string
		holds                          bool
		want                           []start
	}{
		{"on its conversation", "claude", "a1b2c3d4-session", true, []start{{"", "claude", []string{"--resume", "a1b2c3d4-session"}, cfoBackPrompt("claude", why, "")}}},
		{"on a new one when the resumed terminal does not hold", "claude", "a1b2c3d4-session", false, []start{{"", "claude", []string{"--resume", "a1b2c3d4-session"}, cfoBackPrompt("claude", why, "")}, {"", "claude", nil, cfoBackPrompt("claude", why, unresumed)}}},
		{"on a new one when it registered no conversation", "claude", "", true, []start{{"", "claude", nil, ""}}},
		{"as the remembered agent, which a conversation of another harness does not resume", "codex", "a1b2c3d4-session", true, []start{{"", "codex", nil, cfoBackPrompt("codex", why, "")}}},
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
				claudeTranscript(t, f.home.Root, tc.conversation, 1<<10)
			}
			var starts []start
			started := func(_ home.Home, project, harness string, args []string, prompt string) error {
				starts = append(starts, start{project, harness, args, prompt})
				return nil
			}

			// Act
			err := reopenCFO(f.home, why, started, func(string, string) bool { return len(starts) > 0 && tc.holds })

			// Assert
			for i := range tc.want {
				tc.want[i].project = f.home.Root
			}
			if err != nil || !slices.EqualFunc(starts, tc.want, func(a, b start) bool {
				return a.project == b.project && a.harness == b.harness && slices.Equal(a.args, b.args) && a.prompt == b.prompt
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
			claudeTranscript(t, fixture.home.Root, "current-session", 1<<10)
			if err := supervisor.RecordCFOConversationLeft(fixture.home.State, supervisor.CFOConversationLeft{Harness: "claude", Session: "older-session", Resume: []string{"--resume", "older-session"}}); err != nil {
				t.Fatal(err)
			}
			startError := errors.New("fresh start failed")
			isStarted := false
			start := func(_ home.Home, _, _ string, args []string, _ string) error {
				if len(args) == 0 && testCase.isStartError {
					return startError
				}
				isStarted = true
				return nil
			}

			// Act
			err := reopenCFO(fixture.home, "the Overlord reopened it from the board", start, func(string, string) bool { return isStarted && testCase.isResumeHeld })

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
	claudeTranscript(t, f.home.Root, "a1b2c3d4-session", 1<<10)

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
	if want := cfoBackPrompt("claude", "goblins started it again after it closed", ""); !slices.Equal(f.nativePrompts, []string{want}) {
		t.Errorf("the CFO was told %q, want %q", f.nativePrompts, want)
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
	claudeTranscript(t, f.home.Root, "gone-session", 1<<10)

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
			claudeTranscript(t, f.home.Root, "a1b2c3d4-session", 1<<10)

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
			claudeTranscript(t, f.home.Root, "old-conversation", 1<<10)
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
// harness or in Herdr starts a new conversation in its native terminal.
func TestACFOResumesOnlyAConversationItsHarnessCanResume(t *testing.T) {
	for _, c := range []struct {
		name, harness, host, agent string
		args                       []string
		said                       string
	}{
		{"Codex in its native terminal", "codex", supervisor.NativeCFOTerminal, "codex", []string{"resume a1b2c3d4-session"}, "CFO        back as Codex on its conversation a1b2c3d4-session"},
		{"another harness than the one chosen", "codex", supervisor.NativeCFOTerminal, "claude", []string{""}, "CFO        started as Claude Code in "},
		{"a CFO that ran in Herdr", "claude", "", "claude", []string{""}, "CFO        started as Claude Code in "},
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

// writePiSession writes the file pi 0.85.1 keeps session in under the user's
// home userHome, which it writes when its first reply ends.
func writePiSession(t *testing.T, userHome, session string) {
	t.Helper()
	folder := filepath.Join(userHome, ".pi", "agent", "sessions", "--C--cfo-home--")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "2026-10-17T02-44-00-000Z_"+session+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A pi CFO comes back on its own session with pi --session, as a pi goblin
// does, once pi has written that session's file: pi 0.85.1 writes it when its
// first reply ends and exits on a --session it finds no file for, so until
// then the CFO starts a new conversation and says why.
func TestAPiCFOComesBackOnItsOwnSessionOncePiHasWrittenIt(t *testing.T) {
	for _, c := range []struct {
		name             string
		isSessionWritten bool
		args             []string
		said             string
	}{
		{"on its session", true, []string{"--session a1b2c3d4-session"}, "CFO        back as pi on its conversation a1b2c3d4-session, in native terminal cfo"},
		{"on a new one before pi wrote its session", false, []string{""}, "pi has written no file for its conversation a1b2c3d4-session, which it writes when its first reply ends, so the CFO starts a new one."},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			f.agent = "pi"
			userHome := t.TempDir()
			t.Setenv("USERPROFILE", userHome)
			if c.isSessionWritten {
				writePiSession(t, userHome, "a1b2c3d4-session")
			}
			recordConversation(t, f.home.State, "pi", "a1b2c3d4-session", supervisor.NativeCFOTerminal)

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

// requestLine is a line of a Claude Code transcript that records one request
// of the conversation and the tokens it carried.
func requestLine(input, cacheCreation, cacheRead, output int) string {
	return fmt.Sprintf(`{"parentUuid":"p","isSidechain":false,"type":"assistant","message":{"id":"msg_%d","role":"assistant","usage":{"input_tokens":%d,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"output_tokens":%d}}}`, cacheRead, input, cacheCreation, cacheRead, output)
}

// compactionLine is the line a Claude Code compaction leaves in a transcript,
// with the tokens it left the conversation.
func compactionLine(post int) string {
	return fmt.Sprintf(`{"parentUuid":null,"isSidechain":false,"type":"system","subtype":"compact_boundary","content":"Conversation compacted","compactMetadata":{"trigger":"auto","preTokens":882109,"postTokens":%d}}`, post)
}

// requestRecordLine is the record Claude Code keeps of a request beside the
// conversation, which a resume never sends the model, size bytes long: such
// records were 94 percent of a CFO transcript's bytes.
func requestRecordLine(size int) string {
	return `{"type":"api-request","params":{"safeguards":["` + strings.Repeat("x", size) + `"]}}`
}

// writeClaudeTranscript writes Claude Code's transcript of session, a
// conversation the CFO of the home rooted at root held, as lines, under the
// test's own user home.
func writeClaudeTranscript(t *testing.T, root, session string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(claudeTranscript(t, root, session, 0), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A Claude Code CFO resumes a conversation by the context the resume loads,
// which Claude Code takes from its last compaction on, and not by its
// transcript's size on disk, which keeps every line from before: a compacted
// conversation past 20 MB resumes, and one whose last request carried more
// than the 400K window the CFO compacts in starts a new conversation, told
// why. Until 2026-10 one past 20 MB on disk never resumed, so a CFO that
// compacted could not either.
func TestACFOResumesItsConversationByTheContextTheResumeLoads(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lines    []string
		wantArgs []string
		wantSaid string
	}{
		{
			name:     "a compacted one past 20 MB on disk",
			lines:    []string{requestLine(1, 2, 882_000, 3), requestRecordLine(21 << 20), compactionLine(19_459), requestLine(6, 1_200, 84_065, 400)},
			wantArgs: []string{"--resume large-session"},
			wantSaid: "CFO        back as Claude Code on its conversation large-session, in native terminal cfo",
		},
		{
			name:     "a new one in place of one past the window",
			lines:    []string{requestLine(6, 2_300, 800_000, 4)},
			wantArgs: []string{""},
			wantSaid: "Its last conversation holds 802K tokens of context, past the 400K a CFO resumes, so the CFO starts a new one.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			noResumeWait(t)
			f := newSessionFixture(t)
			t.Setenv("USERPROFILE", t.TempDir())
			recordConversation(t, f.home.State, "claude", "large-session", supervisor.NativeCFOTerminal)
			writeClaudeTranscript(t, f.home.Root, "large-session", tc.lines...)

			// Act
			exit, stdout, stderr := f.launch()

			// Assert
			if exit != 0 || !slices.Equal(f.nativeArgs, tc.wantArgs) {
				t.Fatalf("exit=%d nativeArgs=%q stderr=%q, want native args %q", exit, f.nativeArgs, stderr, tc.wantArgs)
			}
			if !strings.Contains(stdout, tc.wantSaid) {
				t.Errorf("stdout = %q, want %q", stdout, tc.wantSaid)
			}
		})
	}
}

// The context a resume of a Claude Code conversation loads is what its last
// request carried, or what its last compaction left where no request
// followed it; a subagent's request, a reply Claude Code wrote itself with no
// request behind it, and everything before the last compaction do not count.
func TestTheContextAClaudeCodeResumeLoads(t *testing.T) {
	for _, tc := range []struct {
		name          string
		lines         []string
		wantContext   int64
		wantIsWritten bool
	}{
		{name: "no transcript"},
		{name: "a first turn not yet answered", lines: []string{`{"type":"user","message":{"role":"user","content":"hello"}}`}, wantIsWritten: true},
		{name: "the last request", lines: []string{requestLine(1, 2, 300, 4), requestLine(10, 20, 3_000, 40)}, wantContext: 3_070, wantIsWritten: true},
		{name: "a compaction no request followed", lines: []string{requestLine(1, 2, 882_000, 3), compactionLine(15_745)}, wantContext: 15_745, wantIsWritten: true},
		{name: "a request after the last compaction", lines: []string{requestLine(1, 2, 882_000, 3), compactionLine(15_745), requestLine(5, 5, 69_500, 8), requestRecordLine(1 << 10)}, wantContext: 69_518, wantIsWritten: true},
		{
			name: "not a subagent's request or a reply with no request",
			lines: []string{
				requestLine(1, 2, 90_000, 3),
				strings.Replace(requestLine(1, 2, 700_000, 3), `"isSidechain":false`, `"isSidechain":true`, 1),
				`{"type":"assistant","isSidechain":false,"message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`,
			},
			wantContext:   90_006,
			wantIsWritten: true,
		},
		{name: "a request before a record longer than a read", lines: []string{requestLine(1, 2, 120_000, 3), requestRecordLine(3 << 20)}, wantContext: 120_006, wantIsWritten: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			t.Setenv("USERPROFILE", t.TempDir())
			root := t.TempDir()
			if tc.lines != nil {
				writeClaudeTranscript(t, root, "a1b2c3d4-session", tc.lines...)
			}

			// Act
			context, isWritten := claudeTranscriptContext(root, "a1b2c3d4-session")

			// Assert
			if context != tc.wantContext || isWritten != tc.wantIsWritten {
				t.Errorf("claudeTranscriptContext = %d, %v; want %d, %v", context, isWritten, tc.wantContext, tc.wantIsWritten)
			}
		})
	}
}

// A home whose CFO never registered starts its first CFO as it always has: a
// Claude Code CFO with no prompt, which waits for the Overlord.
func TestAHomesFirstCFOStartsWithNoInstruction(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeArgs, []string{""}) || !slices.Equal(f.nativePrompts, []string{""}) {
		t.Fatalf("exit=%d nativeArgs=%q prompts=%q stderr=%q, want one new CFO and no instruction", exit, f.nativeArgs, f.nativePrompts, stderr)
	}
}
