package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// goblins resume, and goblins resume with no task named, restart a CEO running in
// its native terminal, its screen frozen or not, on its conversation, answer
// its startup dialogs, say so, and end on the same screen as goblins.
func TestGoblinsResumeRestartsARunningCEOOnItsConversation(t *testing.T) {
	for _, c := range []struct {
		name string
	}{
		{"goblins resume"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
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
		name        string
		withHarness bool
		fresh       string
		restartErr  error
		wantExit    int
	}{
		{name: "switch", withHarness: true, fresh: "The CEO starts a new conversation in codex because its harness changed."},
		{name: "restart", withHarness: true},
		{name: "goblins resume"},
		{name: "failed switch", withHarness: true, restartErr: errors.New("the CEO stopped but did not start again"), wantExit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			f.nativeCEO = supervisor.NativeCEOTerminal
			f.restartSession = "current-session"
			f.restartFresh = test.fresh
			f.restartErr = test.restartErr
			t.Setenv(host.IDVariable, "run")
			arguments := []string{"resume"}
			wantHarness := ""
			if test.withHarness {
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
// a task named resumes that task, as goblins resume does, and restarts no CEO.
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
