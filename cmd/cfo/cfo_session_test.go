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

// newSessionFixture is a launcher fixture with no live CFO whose supervisor
// is already serving a snapshot whose registration is empty, as a
// supervisor's is before its first check, so each test starts at the CFO
// session.
func newSessionFixture(t *testing.T) *launcherFixture {
	t.Helper()
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Fatal("goblins started a second supervisor")
		return nil, nil
	})
	f.cfoLive = false
	f.record(fakeBoard(t, busySnapshot))
	return f
}

// withLiveCFO registers a live CFO in a session other than the fleet's.
func (f *launcherFixture) withLiveCFO() {
	f.cfo = herdr.Endpoint{Target: herdr.Target{Session: "cfo-session", Pane: "w9:p2"}, WorkspaceID: "w9", TabID: "w9:t2", PaneID: "w9:p2"}
	f.cfoLive = true
}

// A CFO whose registration names a live process is never started twice:
// goblins runs no agent steps, brings its registered tab to the front and
// attaches to the session it registered in.
func TestGoblinsBringsALiveCFOToTheFrontWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q stderr=%q, want no agent steps and no CFO started", exit, f.setups, f.nativeStarts, stderr)
	}
	if len(f.focused) != 1 || f.focused[0] != f.cfo {
		t.Errorf("focused %+v, want the registered CFO %+v", f.focused, f.cfo)
	}
	if len(f.attached) != 1 || f.attached[0] != "cfo-session" {
		t.Errorf("attached %q, want the registered session", f.attached)
	}
}

// A CFO registered in a native terminal is shown in this terminal: goblins
// starts no CFO beside it and touches nothing in Herdr.
func TestGoblinsShowsANativeCFOInThisTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.nativeCFO = "cfo"

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q focused=%+v attached=%q stderr=%q, want nothing started and nothing in Herdr", exit, f.setups, f.nativeStarts, f.focused, f.attached, stderr)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != "cfo" {
		t.Errorf("native terminals shown = %q, want the CFO's, cfo", f.nativeAttached)
	}
}

// With no CFO running, goblins starts one in the Code Goblins home, whatever
// folder it is run from, asks for no project, starts it in native terminal
// cfo and shows it here.
func TestGoblinsStartsTheCFOInItsHome(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)

	// Act
	exit, stdout, stderr := f.launch()

	// Assert
	if exit != 0 || !slices.Equal(f.nativeStarts, []string{f.home.Root}) || !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) || len(f.attached) != 0 {
		t.Errorf("exit=%d nativeStarts=%q nativeAttached=%q attached=%q stderr=%q, want the CFO started natively in the home %s and shown, nothing in Herdr", exit, f.nativeStarts, f.nativeAttached, f.attached, stderr, f.home.Root)
	}
	if !strings.Contains(stdout, "CFO        started as Claude Code in "+f.home.Root+", in native terminal cfo\n") {
		t.Errorf("stdout = %q, want it to say where the CFO starts", stdout)
	}
}

// A CFO that cannot start is reported, and nothing is shown.
func TestGoblinsReportsACFOThatCannotStart(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.runtime.startNativeCFO = func(home.Home, string, string, []string) error { return errors.New("claude is not on PATH") }

	// Act
	exit, _, stderr := f.launch()

	// Assert
	if exit != 1 || !strings.Contains(stderr, "could not be started in a native terminal: claude is not on PATH") || len(f.nativeAttached) != 0 || len(f.screens) != 0 {
		t.Errorf("exit=%d stderr=%q nativeAttached=%q screens=%+v, want the failure reported and nothing shown", exit, stderr, f.nativeAttached, f.screens)
	}
}

// With no CFO registered, a CFO already running in native terminal cfo, which
// may not have registered yet, is shown rather than started a second time.
func TestGoblinsShowsAnUnregisteredCFOInNativeTerminalCFO(t *testing.T) {
	f := newSessionFixture(t)
	f.cfoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.setups) != 0 || len(f.nativeStarts) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d setups=%+v nativeStarts=%q attached=%q stderr=%q, want nothing started", exit, f.setups, f.nativeStarts, f.attached, stderr)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) {
		t.Errorf("native terminals shown = %q, want cfo", f.nativeAttached)
	}
}

// A CFO registered in Herdr is brought to the front even while native
// terminal cfo runs: the registration decides which CFO goblins shows.
func TestGoblinsPrefersARegisteredHerdrCFOToAnUnregisteredNativeTerminal(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.cfoTerminalRuns = true

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.focused) != 1 || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d focused=%+v nativeAttached=%q stderr=%q, want the Herdr CFO in front and no native terminal shown", exit, f.focused, f.nativeAttached, stderr)
	}
}

// A live CFO that cannot be brought to the front is reported, and nothing is
// attached.
func TestGoblinsReportsALiveCFOItCannotBringToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.focusCFO = func(context.Context, herdr.Endpoint) error { return errors.New("herdr server is not running") }

	exit, _, stderr := f.launch()

	if exit != 1 || len(f.attached) != 0 || len(f.nativeStarts) != 0 || !strings.Contains(stderr, "herdr server is not running") {
		t.Fatalf("exit=%d attached=%q nativeStarts=%q stderr=%q, want the failure and no attach", exit, f.attached, f.nativeStarts, stderr)
	}
}

// Inside a Herdr pane there is nothing to attach: goblins brings the CFO to
// the front and says so.
func TestGoblinsInsideHerdrOnlyBringsTheCFOToTheFront(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	t.Setenv("HERDR_PANE_ID", "p1")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.attached) != 0 || len(f.focused) != 1 || !strings.Contains(stdout, "The CFO is in front in Herdr.") {
		t.Fatalf("exit=%d attached=%q focused=%+v stdout=%q, want the CFO in front and no attach", exit, f.attached, f.focused, stdout)
	}
}

// goblins ends with herdr's own exit code.
func TestGoblinsEndsWithHerdrsExitCode(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.attachHerdr = func(string) int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
