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

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// goblins resume, and ceo resume with no task named, restart a CEO running in
// its native terminal, its screen frozen or not, on its conversation, answer
// its startup dialogs, say so, and end on the same screen as goblins.
func TestGoblinsResumeRestartsARunningCEOOnItsConversation(t *testing.T) {
	for _, c := range []struct {
		name      string
		isGoblins bool
	}{
		{"goblins resume", true},
		{"ceo resume", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.runtime.goblins = c.isGoblins
			f.nativeCEO = supervisor.NativeCEOTerminal
			f.restartSession = "a1b2c3d4-session"
			f.settleNotes = []string{"Answered the update prompt in the CEO's terminal: Skip."}

			// Act
			exit, stdout, stderr := f.launch("resume")

			// Assert
			if exit != 0 || f.restarts != 1 || len(f.nativeStarts) != 0 {
				t.Fatalf("exit=%d restarts=%d nativeStarts=%q stderr=%q, want one restart and no other start", exit, f.restarts, f.nativeStarts, stderr)
			}
			if !strings.Contains(stdout, "CEO        restarted on its conversation a1b2c3d4-session, in native terminal ceo\n") || !strings.Contains(stdout, "Its current response was interrupted; goblins keep running.") {
				t.Errorf("stdout = %q, want the restart said", stdout)
			}
			if !strings.Contains(stdout, f.settleNotes[0]) {
				t.Errorf("stdout = %q, want the restarted CEO's startup dialogs answered and said", stdout)
			}
			if strings.Count(stdout, "CEO        ") != 1 {
				t.Errorf("stdout = %q, want the CEO said once, as restarted", stdout)
			}
			if len(f.screens) != 1 || !strings.HasPrefix(f.screens[0].title, "Your CEO is starting\n") || !slices.Equal(f.nativeAttached, []string{supervisor.NativeCEOTerminal}) {
				t.Errorf("screens=%+v nativeAttached=%q, want the final screen and the CEO's terminal", f.screens, f.nativeAttached)
			}
		})
	}
}

// A restarted CEO that did not come back on its conversation runs on a new
// one, and goblins resume says so, with why the restart said.
func TestGoblinsResumeSaysTheRestartedCEOStartedANewConversation(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCEO = supervisor.NativeCEOTerminal
	f.restartSession = "a1b2c3d4-session"
	f.restartFresh = "Its last conversation holds 802K tokens of context, past the 400K a CEO resumes, so the CEO starts a new one."

	// Act
	exit, stdout, stderr := f.launch("resume")

	// Assert
	if exit != 0 || f.restarts != 1 {
		t.Fatalf("exit=%d restarts=%d stderr=%q, want one restart", exit, f.restarts, stderr)
	}
	if !strings.Contains(stdout, "CEO        restarted as Claude Code on a new conversation, in native terminal ceo\n") || !strings.Contains(stdout, f.restartFresh) {
		t.Errorf("stdout = %q, want the new conversation and why said", stdout)
	}
}

func TestResumeFromARunCardReturnsWithoutOpeningTheCEO(t *testing.T) {
	for _, test := range []struct {
		name       string
		isGoblins  bool
		fresh      string
		restartErr error
		wantExit   int
	}{
		{name: "switch", isGoblins: true, fresh: "The CEO starts a new conversation in codex because its harness changed."},
		{name: "restart", isGoblins: true},
		{name: "ceo resume"},
		{name: "failed switch", isGoblins: true, restartErr: errors.New("the CEO stopped but did not start again"), wantExit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.runtime.goblins = test.isGoblins
			f.nativeCEO = supervisor.NativeCEOTerminal
			f.restartSession = "current-session"
			f.restartFresh = test.fresh
			f.restartErr = test.restartErr
			t.Setenv(host.IDVariable, "run")
			arguments := []string{"resume"}
			wantHarness := ""
			if test.isGoblins {
				arguments = append(arguments, "--harness", "codex")
				wantHarness = "codex"
			}

			// Act
			exit, stdout, stderr := f.launch(arguments...)

			// Assert
			if exit != test.wantExit || f.restarts != 1 || f.restartHarness != wantHarness {
				t.Fatalf("exit=%d restarts=%d harness=%q stderr=%q", exit, f.restarts, f.restartHarness, stderr)
			}
			if len(f.screens) != 0 || len(f.nativeAttached) != 0 || len(f.attached) != 0 || len(f.opened) != 0 || len(f.windows) != 0 {
				t.Errorf("resume opened a viewer: screens=%v native=%q herdr=%q browser=%q windows=%q", f.screens, f.nativeAttached, f.attached, f.opened, f.windows)
			}
			if test.wantExit == 0 && !strings.Contains(stdout, "CEO        restarted") {
				t.Errorf("stdout=%q, want the completed restart reported", stdout)
			}
			if test.restartErr != nil && !strings.Contains(stderr, test.restartErr.Error()) {
				t.Errorf("stderr=%q, want the launch failure reported", stderr)
			}
		})
	}
}

// A restarted CEO can end while its startup dialogs are answered, as a fresh
// one can; goblins resume then reports it ended rather than saying it was
// restarted, and offers no terminal to attach to.
func TestGoblinsResumeReportsARestartedCEOThatEndsWhileItsDialogsAreAnswered(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCEO = supervisor.NativeCEOTerminal
	f.restartSession = "a1b2c3d4-session"
	f.runtime.settleCEO = func(context.Context, string, string) []string {
		f.ceoTerminalRuns = false
		return []string{"Answered the update prompt in the CEO's terminal: Skip."}
	}

	// Act
	exit, stdout, stderr := f.launch("resume")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "the restarted CEO's native terminal ended during startup") {
		t.Fatalf("exit=%d stderr=%q, want the ended restart reported", exit, stderr)
	}
	if strings.Contains(stdout, "CEO        restarted") || len(f.screens) != 0 || len(f.nativeAttached) != 0 {
		t.Errorf("stdout=%q screens=%v attached=%q, want no restart claimed and nothing to attach to", stdout, f.screens, f.nativeAttached)
	}
}

// goblins resume with no CEO running brings a closed one back, as goblins
// does: on its conversation, in its native terminal.
func TestGoblinsResumeBringsBackAClosedCEO(t *testing.T) {
	// Arrange
	noResumeWait(t)
	f := newSessionFixture(t)
	recordConversation(t, f.home.State, "claude", "a1b2c3d4-session", supervisor.NativeCEOTerminal)
	claudeTranscript(t, f.home.Root, "a1b2c3d4-session", 1<<10)

	// Act
	exit, _, stderr := f.launch("resume")

	// Assert
	if exit != 0 || f.restarts != 1 || !slices.Equal(f.nativeArgs, []string{"--resume a1b2c3d4-session"}) {
		t.Fatalf("exit=%d restarts=%d nativeArgs=%q stderr=%q, want the closed CEO brought back on its conversation", exit, f.restarts, f.nativeArgs, stderr)
	}
}

// A restart that would lose the CEO's conversation is refused with its
// reason, the CEO left running and nothing else started; goblins resume with
// a task named resumes that task, as ceo resume does, and restarts no CEO.
func TestGoblinsResumeSaysWhyItLeftTheCEORunning(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.nativeCEO = supervisor.NativeCEOTerminal
	f.restartErr = errors.New("the CEO in native terminal ceo registered no conversation it can come back on, so it is left running")

	// Act
	exit, _, stderr := f.launch("resume")
	restarts := f.restarts
	extra, _, extraErr := f.launch("resume", "now")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "so it is left running") || len(f.nativeStarts) != 0 || len(f.screens) != 0 {
		t.Errorf("exit=%d stderr=%q nativeStarts=%q screens=%+v, want the refusal and nothing started or shown", exit, stderr, f.nativeStarts, f.screens)
	}
	if extra == 0 || f.restarts != restarts || strings.Contains(extraErr, "usage: goblins resume") {
		t.Errorf("goblins resume now: exit=%d restarts=%d stderr=%q, want task now's resume refused and no CEO restarted", extra, f.restarts-restarts, extraErr)
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

// waitForFakeClaudeArguments waits for the test binary, run as claude.exe in
// dir, to record arguments other than skip, and returns them, without the
// pair every start of the CEO opens with, which hands it its hooks
// (ceoHookArguments). The stand-in writes the file and then what it holds,
// so an empty one is not written yet, as waitForFakeClaudeEnvironment reads
// its file: every start of the CEO has arguments.
func waitForFakeClaudeArguments(t *testing.T, dir string, skip []string) []string {
	t.Helper()
	hooks := "--settings\n" + filepath.Join(dir, "state", ceoSettingsFile)
	isEmpty := false
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		raw, err := os.ReadFile(filepath.Join(dir, fakeClaudeArguments))
		isEmpty = err == nil && len(raw) == 0
		if err != nil || isEmpty {
			continue
		}
		rest, hasHooks := strings.CutPrefix(string(raw), hooks)
		if !hasHooks {
			t.Fatalf("the CEO started with %q, want it to open with --settings and the file of its hooks", raw)
		}
		if got := strings.Split(strings.TrimPrefix(rest, "\n"), "\n"); !slices.Equal(got, skip) {
			return got
		}
	}
	if isEmpty {
		t.Fatal("the CEO started with no arguments at all, want it to open with --settings and the file of its hooks")
	}
	t.Fatal("the CEO did not start again in native terminal ceo within a minute")
	return nil
}

// The stand-in claude.exe writes its arguments in two steps, the file and
// then what it holds. On 2026-10-10 a restart test on a CI runner read the
// file between the two and took the empty read for a CEO that started with
// no arguments at all, though every start of the CEO has some. A file that
// is not written yet is waited for.
func TestTheStandInsArgumentsAreReadOnceTheyAreWritten(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	path := filepath.Join(dir, fakeClaudeArguments)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := "--settings\n" + filepath.Join(dir, "state", ceoSettingsFile) + "\n--resume\na1b2c3d4-session"
	written := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		written <- os.WriteFile(path, []byte(arguments), 0o600)
	}()

	// Act
	got := waitForFakeClaudeArguments(t, dir, nil)

	// Assert
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if want := []string{"--resume", "a1b2c3d4-session"}; !slices.Equal(got, want) {
		t.Fatalf("the stand-in's arguments read as %q, want %q", got, want)
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
