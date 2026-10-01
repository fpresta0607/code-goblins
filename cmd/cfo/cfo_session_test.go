package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	f.runtime.gitTop = func(context.Context) (string, error) {
		f.t.Error("goblins picked a project for a CFO that is live")
		return "", errors.New("not asked")
	}
}

// projectsRoot makes a projects root holding the named folders, those in
// checkouts with a .git of their own, and points the fixture at it with the
// terminal in no checkout.
func (f *launcherFixture) projectsRoot(folders []string, checkouts ...string) string {
	f.t.Helper()
	root := f.t.TempDir()
	for _, folder := range folders {
		if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, checkout := range checkouts {
		if err := os.MkdirAll(filepath.Join(root, checkout, ".git"), 0o755); err != nil {
			f.t.Fatal(err)
		}
	}
	f.runtime.projectsRoot = func() (string, error) { return root, nil }
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a git checkout") }
	return root
}

// A CFO whose registration names a live process is never started twice:
// goblins brings its registered tab to the front and attaches to the session
// it registered in.
func TestGoblinsBringsALiveCFOToTheFrontWithoutStartingAnother(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, _, stderr := f.launch()

	if exit != 0 || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q stderr=%q, want no CFO started", exit, f.nativeStarts, stderr)
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

	if exit != 0 || len(f.nativeStarts) != 0 || len(f.focused) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q focused=%+v attached=%q stderr=%q, want nothing started and nothing in Herdr", exit, f.nativeStarts, f.focused, f.attached, stderr)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != "cfo" {
		t.Errorf("native terminals shown = %q, want the CFO's, cfo", f.nativeAttached)
	}
}

// goblins with no live CFO starts one in native terminal cfo, in the project
// picked, and shows it in this terminal, starting nothing in Herdr.
func TestGoblinsStartsANewCFOInANativeTerminal(t *testing.T) {
	f := newSessionFixture(t)

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != f.project || len(f.attached) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q attached=%q stderr=%q, want the CFO started natively in %s and nothing in Herdr", exit, f.nativeStarts, f.attached, stderr, f.project)
	}
	if len(f.nativeAttached) != 1 || f.nativeAttached[0] != supervisor.NativeCFOTerminal {
		t.Errorf("native terminals shown = %q, want %s", f.nativeAttached, supervisor.NativeCFOTerminal)
	}
	if !strings.Contains(stdout, "The CFO starts as claude in "+f.project+", in native terminal cfo.") {
		t.Errorf("stdout = %q, want it to say where the CFO starts", stdout)
	}
}

// A CFO that cannot start is reported, and nothing is shown.
func TestGoblinsReportsACFOThatCannotStart(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.startNativeCFO = func(home.Home, string, string) error { return errors.New("claude is not on PATH") }

	exit, _, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "could not be started in a native terminal: claude is not on PATH") || len(f.nativeAttached) != 0 {
		t.Fatalf("exit=%d stderr=%q nativeAttached=%q, want the failure reported and nothing shown", exit, stderr, f.nativeAttached)
	}
}

// With no CFO registered, a CFO already running in native terminal cfo, which
// may not have registered yet, is shown rather than started a second time.
func TestGoblinsShowsAnUnregisteredCFOInNativeTerminalCFO(t *testing.T) {
	f := newSessionFixture(t)
	f.cfoTerminalRuns = true

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.nativeStarts) != 0 || len(f.attached) != 0 {
		t.Fatalf("exit=%d nativeStarts=%q attached=%q stderr=%q, want nothing started", exit, f.nativeStarts, f.attached, stderr)
	}
	if !slices.Equal(f.nativeAttached, []string{supervisor.NativeCFOTerminal}) || !strings.Contains(stdout, "The CFO is already running in native terminal cfo.") {
		t.Errorf("native terminals shown = %q, stdout = %q; want cfo shown and said so", f.nativeAttached, stdout)
	}
}

// goblins --native went with the Herdr CFO start it chose against: a new CFO
// always starts natively, so the flag is refused and nothing starts.
func TestGoblinsRefusesTheRetiredNativeFlag(t *testing.T) {
	f := newSessionFixture(t)

	exit, _, stderr := f.launch("--native")

	if exit != 2 || !strings.Contains(stderr, "-native") || len(f.nativeStarts)+len(f.nativeAttached)+len(f.attached) != 0 {
		t.Fatalf("exit=%d stderr=%q nativeStarts=%q nativeAttached=%q attached=%q, want the flag refused and nothing started", exit, stderr, f.nativeStarts, f.nativeAttached, f.attached)
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

// Outside a checkout, goblins lists the checkouts under the projects root, a
// folder with no .git left out, and starts the CFO in the one picked.
func TestGoblinsAsksWhichProjectOutsideACheckout(t *testing.T) {
	f := newSessionFixture(t)
	root := f.projectsRoot([]string{"notes"}, "alpha", "beta")
	f.runtime.stdin = strings.NewReader("2\n")

	exit, stdout, stderr := f.launch()

	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != filepath.Join(root, "beta") {
		t.Fatalf("exit=%d nativeStarts=%q stderr=%q, want beta", exit, f.nativeStarts, stderr)
	}
	if !strings.Contains(stdout, "  1  alpha\n  2  beta\n") || strings.Contains(stdout, "notes") {
		t.Errorf("stdout = %q, want the two checkouts listed and the plain folder left out", stdout)
	}
}

// A projects root with one checkout needs no question.
func TestGoblinsTakesTheOnlyProjectWithoutAsking(t *testing.T) {
	f := newSessionFixture(t)
	root := f.projectsRoot(nil, "alpha")

	exit, stdout, _ := f.launch()

	if exit != 0 || len(f.nativeStarts) != 1 || f.nativeStarts[0] != filepath.Join(root, "alpha") || strings.Contains(stdout, "Project number") {
		t.Fatalf("exit=%d nativeStarts=%q stdout=%q, want alpha without a question", exit, f.nativeStarts, stdout)
	}
}

// A number that names no project starts nothing and attaches nothing.
func TestGoblinsRefusesAProjectNumberThatNamesNoProject(t *testing.T) {
	for _, answer := range []string{"3\n", "zero\n", ""} {
		f := newSessionFixture(t)
		f.projectsRoot(nil, "alpha", "beta")
		f.runtime.stdin = strings.NewReader(answer)

		exit, _, stderr := f.launch()

		if exit != 1 || len(f.nativeStarts) != 0 || len(f.nativeAttached) != 0 {
			t.Errorf("answer %q: exit=%d nativeStarts=%q nativeAttached=%q stderr=%q, want a refusal", answer, exit, f.nativeStarts, f.nativeAttached, stderr)
		}
	}
}

// With neither a checkout nor a projects root, goblins says how to give it one.
func TestGoblinsNamesTheFixWhenThereIsNoProject(t *testing.T) {
	f := newSessionFixture(t)
	f.runtime.gitTop = func(context.Context) (string, error) { return "", errors.New("not a git checkout") }

	exit, _, stderr := f.launch()

	if exit != 1 || !strings.Contains(stderr, "cfo install --projects-root <dir>") || len(f.nativeStarts) != 0 {
		t.Fatalf("exit=%d stderr=%q nativeStarts=%q, want the fix named", exit, stderr, f.nativeStarts)
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

// goblins ends with herdr's own exit code when it attaches to a live CFO there.
func TestGoblinsEndsWithHerdrsExitCode(t *testing.T) {
	f := newSessionFixture(t)
	f.withLiveCFO()
	f.runtime.attachHerdr = func(string) int { return 3 }

	if exit, _, stderr := f.launch(); exit != 3 {
		t.Fatalf("exit=%d stderr=%q, want herdr's 3", exit, stderr)
	}
}
