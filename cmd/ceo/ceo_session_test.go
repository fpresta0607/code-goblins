package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// newSessionFixture is a launcher fixture with no live CEO whose supervisor
// is already serving a snapshot whose registration is empty, as a
// supervisor's is before its first check, so each test starts at the CEO
// session.
func newSessionFixture(t *testing.T) *launcherFixture {
	t.Helper()
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	f.ceoLive = false
	f.record(fakeBoard(t, busySnapshot))
	return f
}

// withLiveCEO registers a live CEO in a session other than the fleet's.
func (f *launcherFixture) withLiveCEO() {
	f.ceo = herdr.Endpoint{Target: herdr.Target{Session: "ceo-session", Pane: "w9:p2"}, WorkspaceID: "w9", TabID: "w9:t2", PaneID: "w9:p2"}
	f.ceoLive = true
}

// A CEO whose registration names a live process is never started twice:
// goblins runs no agent steps, brings its registered tab to the front and
// attaches to the session it registered in.
func TestGoblinsBringsALiveCEOToTheFrontWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCEO()

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q stderr=%q, want no agent steps and no CEO started", exit, f.setups, f.nativeStarts, stderr)
	}
	if len(f.focused) != 1 || f.focused[0] != f.ceo {
		t.Errorf("focused %+v, want the registered CEO %+v", f.focused, f.ceo)
	}
	if len(f.attached) != 1 || f.attached[0] != "ceo-session" {
		t.Errorf("attached %q, want the registered session", f.attached)
	}
}

// A CEO registered in a native terminal is shown in this terminal: goblins
// starts no CEO beside it and touches nothing in Herdr.
func TestGoblinsShowsANativeCEOInThisTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.nativeCEO = "ceo"

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q focused=%+v attached=%q stderr=%q, want nothing started and nothing in Herdr", exit, f.setups, f.nativeStarts, f.focused, f.attached, stderr)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != "ceo" {
		t.Errorf("native terminals shown = %q, want the CEO's, ceo", f.nativeAttached)
	}
}

// With no CEO running, goblins starts one in the Code Goblins home, whatever
// folder it is run from, asks for no project, starts it in native terminal
// ceo and shows it here.
func TestGoblinsStartsTheCEOInItsHome(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeStarts, []string{f.home.Root}) || !slices.Equal(f.nativeAttached, []string{supervisor.NativeCEOTerminal}) || len(f.attached) != 0 {
		t.Errorf("exit=%d nativeStarts=%q nativeAttached=%q attached=%q stderr=%q, want the CEO started natively in the home %s and shown, nothing in Herdr", exit, f.nativeStarts, f.nativeAttached, f.attached, stderr, f.home.Root)
	}
	if !strings.Contains(stdout, "CEO        started as Claude Code in "+f.home.Root+", in native terminal ceo\n") {
		t.Errorf("stdout = %q, want it to say where the CEO starts", stdout)
	}
}

// A CEO that cannot start is reported, and nothing is shown.
func TestGoblinsReportsACEOThatCannotStart(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.startNativeCEO = func(home.Home, string, string, []string, string) error { return errors.New("claude is not on PATH") }

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "could not be started in a native terminal: claude is not on PATH") || len(f.nativeAttached) != 0 || len(f.screens) != 0 {
		t.Errorf("exit=%d stderr=%q nativeAttached=%q screens=%+v, want the failure reported and nothing shown", exit, stderr, f.nativeAttached, f.screens)
	}
}

// With no CEO registered, a CEO already running in native terminal ceo, which
// may not have registered yet, is shown rather than started a second time.
func TestGoblinsShowsAnUnregisteredCEOInNativeTerminalCEO(t *testing.T) {
	f := newSessionFixture(t)
	f.ceoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q attached=%q stderr=%q, want nothing started", exit, f.setups, f.nativeStarts, f.attached, stderr)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCEOTerminal}) {
		t.Errorf("native terminals shown = %q, want ceo", f.nativeAttached)
	}
}

// A CEO registered in Herdr is brought to the front even while native
// terminal ceo runs: the registration decides which CEO goblins shows.
func TestGoblinsPrefersARegisteredHerdrCEOToAnUnregisteredNativeTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCEO()
	f.ceoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.focused) != 1 || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d focused=%+v nativeAttached=%q stderr=%q, want the Herdr CEO in front and no native terminal shown", exit, f.focused, f.nativeAttached, stderr)
	}
}

// A live CEO that cannot be brought to the front is reported, and nothing is
// attached.
func TestGoblinsReportsALiveCEOItCannotBringToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCEO()
	f.runtime.focusCEO = func(context.Context, herdr.Endpoint) error { return errors.New("herdr server is not running") }

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || len(f.nativeStarts) != 0 || !strings.Contains(stderr, "herdr server is not running") {
		t.Fatalf("exit=%d attached=%q nativeStarts=%q stderr=%q, want the failure and no attach", exit, f.attached, f.nativeStarts, stderr)
	}
}

// Inside a Herdr pane there is nothing to attach: goblins brings the CEO to
// the front and says so.
func TestGoblinsInsideHerdrOnlyBringsTheCEOToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCEO()
	t.Setenv("HERDR_PANE_ID", "p1")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.attached) != 0 || len(f.focused) != 1 || !strings.Contains(stdout, "The CEO is in front in Herdr.") {
		t.Fatalf("exit=%d attached=%q focused=%+v stdout=%q, want the CEO in front and no attach", exit, f.attached, f.focused, stdout)
	}
}

// goblins ends with herdr's own exit code.
func TestGoblinsEndsWithHerdrsExitCode(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCEO()
	f.runtime.attachHerdr = func(string) int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
