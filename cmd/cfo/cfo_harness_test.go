package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// goblins --harness chooses the harness the CFO starts as, and the home
// remembers it: a later goblins, in Herdr or native, starts that harness
// until another is chosen. A CFO that is not Claude Code is told how it is
// woken: in Herdr not at all, and in a native terminal by a typed line once
// it has registered.
func TestGoblinsRemembersTheCFOHarnessForEveryLaterStart(t *testing.T) {
	f := newSessionFixture(t)
	harnessesOnPath(t, "codex", "pi")

	chose, chooseOut, chooseErr := f.launch("--harness", "codex")
	later, laterOut, _ := f.launch()
	native, nativeOut, _ := f.launch("--native", "--harness", "pi")

	if chose != 0 || later != 0 || native != 0 {
		t.Fatalf("exits %d, %d, %d (stderr %q), want 0", chose, later, native, chooseErr)
	}
	if want := []string{"codex", "codex", "pi"}; !slices.Equal(f.harnesses, want) {
		t.Errorf("harnesses started = %q, want %q", f.harnesses, want)
	}
	if len(f.cfoStarts) != 2 || len(f.nativeStarts) != 1 {
		t.Errorf("starts in Herdr %q and native %q, want two and one", f.cfoStarts, f.nativeStarts)
	}
	for _, out := range []string{chooseOut, laterOut} {
		if !strings.Contains(out, "The CFO starts as codex in "+f.home.Root+".") || !strings.Contains(out, "A codex CFO has no wake path in Herdr") {
			t.Errorf("stdout = %q, want the codex start and its missing wake path in Herdr", out)
		}
	}
	if !strings.Contains(nativeOut, "The CFO starts as pi in "+f.home.Root+", in native terminal cfo.") || !strings.Contains(nativeOut, "A pi CFO is woken by one line typed into this terminal") || !strings.Contains(nativeOut, "once it has run cfo register in this terminal") {
		t.Errorf("stdout = %q, want the native pi start and how it is woken", nativeOut)
	}
	if strings.Contains(nativeOut, "no wake path") {
		t.Errorf("stdout = %q, want no missing wake path for a native pi CFO", nativeOut)
	}
	if data, err := os.ReadFile(cfoHarnessPath(f.home.State)); err != nil || strings.TrimSpace(string(data)) != "pi" {
		t.Errorf("remembered harness = %q, %v; want pi", data, err)
	}
}

// With nothing chosen the CFO starts as Claude Code, which has a wake path.
func TestGoblinsStartsTheCFOAsClaudeUnlessToldOtherwise(t *testing.T) {
	f := newSessionFixture(t)

	exit, stdout, stderr := f.launch()

	if exit != 0 || !slices.Equal(f.harnesses, []string{"claude"}) {
		t.Fatalf("exit=%d harnesses=%q stderr=%q, want one claude start", exit, f.harnesses, stderr)
	}
	if strings.Contains(stdout, "wake path") {
		t.Errorf("stdout = %q, want no wake path warning for Claude Code", stdout)
	}
}

// A CFO already running keeps the harness it runs: goblins --harness starts
// nothing beside it, remembers the choice for the next start, and says so.
func TestALiveCFOKeepsItsHarness(t *testing.T) {
	for _, c := range []struct {
		name string
		live func(f *launcherFixture)
	}{
		{"registered native", func(f *launcherFixture) { f.nativeCFO = supervisor.NativeCFOTerminal }},
		{"registered in Herdr", func(f *launcherFixture) { f.withLiveCFO() }},
		{"unregistered in native terminal cfo", func(f *launcherFixture) { f.cfoTerminalRuns = true }},
		{"an agent already in the cfo tab", func(f *launcherFixture) {
			f.runtime.startCFO = func(context.Context, string, string) (bool, error) { return false, nil }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			f := newSessionFixture(t)
			harnessesOnPath(t, "codex")
			c.live(f)

			// Act
			exit, stdout, stderr := f.launch("--harness", "codex")

			// Assert
			if exit != 0 || len(f.harnesses) != 0 || len(f.nativeStarts) != 0 {
				t.Fatalf("exit=%d harnesses=%q nativeStarts=%q stderr=%q, want nothing started", exit, f.harnesses, f.nativeStarts, stderr)
			}
			if !strings.Contains(stdout, "The CFO already runs, and keeps its harness; codex is the harness goblins starts the next CFO as.") {
				t.Errorf("stdout = %q, want it to say the live CFO keeps its harness", stdout)
			}
			if harness, err := cfoHarness(f.home.State); err != nil || harness != "codex" {
				t.Errorf("remembered harness = %q, %v; want codex", harness, err)
			}
		})
	}
}

// The board's first-run page offers only Claude Code, so the CFO it starts is
// claude whatever harness goblins remembers. With claude found only as a
// script and codex not on PATH, neither start can reach a host.
func TestTheFirstRunStartsClaudeWhateverHarnessIsRemembered(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	h := home.Home{State: t.TempDir()}
	if err := os.WriteFile(cfoHarnessPath(h.State), []byte("codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := firstRunOn(h, t.TempDir(), true, func(string) error { return nil })

	// Act
	err := run.StartCFO(t.TempDir())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "the native build of Claude Code is claude.exe") {
		t.Fatalf("first-run StartCFO error = %v, want claude looked up rather than codex", err)
	}
}

// A harness goblins cannot start the CFO as is refused before anything is
// remembered or started, and a remembered one that names none is refused
// rather than guessed at.
func TestGoblinsRefusesAHarnessItCannotStartTheCFOAs(t *testing.T) {
	f := newSessionFixture(t)

	exit, _, stderr := f.launch("--harness", "kimi")

	if exit != 2 || !strings.Contains(stderr, `--harness "kimi" is not claude, codex or pi`) || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d stderr=%q harnesses=%q, want kimi refused", exit, stderr, f.harnesses)
	}
	if _, err := os.Stat(cfoHarnessPath(f.home.State)); !os.IsNotExist(err) {
		t.Errorf("a refused harness was remembered: %v", err)
	}
	if err := os.WriteFile(cfoHarnessPath(f.home.State), []byte("kimi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.agent = "pi"
	exit, _, stderr = f.launch()
	if exit != 0 || !slices.Equal(f.harnesses, []string{"pi"}) {
		t.Errorf("exit=%d stderr=%q harnesses=%q, want the choice asked again and pi started", exit, stderr, f.harnesses)
	}
	if harness, err := cfoHarness(f.home.State); err != nil || harness != "pi" {
		t.Errorf("remembered harness = %q, %v; want the choice replacing kimi", harness, err)
	}
}

// goblins takes only its flags: an argument left after them is refused with
// a message that names it, never with a bare exit code.
func TestGoblinsNamesAnArgumentItDoesNotTake(t *testing.T) {
	f := newSessionFixture(t)

	exit, _, stderr := f.launch("--native", "codex")

	if exit != 2 || !strings.Contains(stderr, `unexpected argument "codex"`) || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d stderr=%q harnesses=%q, want the argument named and nothing started", exit, stderr, f.harnesses)
	}
}

// A harness that is not installed goes to its install step, and a choice that
// never became ready is not remembered and starts nothing.
func TestGoblinsRemembersNoHarnessThatNeverBecameReady(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	f.missing = []string{"codex"}

	// Act
	exit, _, stderr := f.launch("--harness", "codex")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "setup was cancelled") || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d stderr=%q harnesses=%q, want codex's install step cancelled and nothing started", exit, stderr, f.harnesses)
	}
	if _, err := os.Stat(cfoHarnessPath(f.home.State)); !os.IsNotExist(err) {
		t.Errorf("a harness not on PATH was remembered: %v", err)
	}
}

// In Herdr Claude Code starts through herdr agent start, while codex and pi,
// npm script shims Herdr's Windows agent start cannot run, are typed into the
// cfo pane's shell and submitted, as a Herdr goblin's typed launch is.
func TestStartingTheCFOInHerdrStartsEachHarnessAsHerdrCan(t *testing.T) {
	for harness, want := range map[string][]string{
		"claude": {"AgentStart fleet:w1:p1 cfo claude"},
		"codex":  {"SendLiteral fleet:w1:p1 codex", "SendKey fleet:w1:p1 Enter"},
		"pi":     {"SendLiteral fleet:w1:p1 pi", "SendKey fleet:w1:p1 Enter"},
	} {
		t.Run(harness, func(t *testing.T) {
			// Arrange
			fake := &terminaltest.Fake{
				Session:   "fleet",
				Container: herdr.Container{Session: "fleet", WorkspaceID: "w1"},
				CFO:       herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"},
			}
			noLaunchWait(t)

			// Act
			started, err := startCFOWith(context.Background(), fake, `C:\dev\app`, harness, io.Discard)

			// Assert
			if err != nil || !started {
				t.Fatalf("startCFOWith = %v, %v; want a CFO started", started, err)
			}
			var launch []string
			for _, call := range fake.Calls() {
				if strings.HasPrefix(call, "AgentStart ") || strings.HasPrefix(call, "SendLiteral ") || strings.HasPrefix(call, "SendKey ") {
					launch = append(launch, call)
				}
			}
			if !slices.Equal(launch, want) {
				t.Errorf("launch calls = %q, want %q", launch, want)
			}
		})
	}
}

// A typed CFO that Herdr does not detect, its pane dead with the harness
// running, is reported to Herdr as agent cfo, so a later goblins finds an
// agent in the cfo tab rather than starting a second CFO beside it. One Herdr
// detects is left alone, and Claude Code, which herdr agent start registers,
// is never reported.
func TestStartingTheCFOInHerdrReportsATypedCFOHerdrDoesNotDetect(t *testing.T) {
	for _, c := range []struct {
		name       string
		harness    string
		status     herdr.AgentStatus
		wantReport []string
	}{
		{"undetected codex", "codex", herdr.AgentDead, []string{"ReportAgent fleet:w1:p1 codex unknown"}},
		{"undetected pi", "pi", herdr.AgentDead, []string{"ReportAgent fleet:w1:p1 pi unknown"}},
		{"detected pi", "pi", herdr.AgentAlive, nil},
		{"claude", "claude", herdr.AgentDead, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			fake := &terminaltest.Fake{
				Session:   "fleet",
				Container: herdr.Container{Session: "fleet", WorkspaceID: "w1"},
				CFO:       herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"},
				Status:    c.status,
				Running:   true,
			}
			noLaunchWait(t)

			// Act
			if _, err := startCFOWith(context.Background(), fake, `C:\dev\app`, c.harness, io.Discard); err != nil {
				t.Fatal(err)
			}

			// Assert
			var reports []string
			for _, call := range fake.Calls() {
				if strings.HasPrefix(call, "ReportAgent ") {
					reports = append(reports, call)
				}
			}
			if !slices.Equal(reports, c.wantReport) {
				t.Errorf("reports = %q, want %q (calls %q)", reports, c.wantReport, fake.Calls())
			}
		})
	}
}

// harnessesOnPath makes PATH hold only stub script shims for the named
// harnesses.
func harnessesOnPath(t *testing.T, names ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(bin, name+".cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

// A native terminal starts a program itself: Claude Code's native build as
// it is, and the npm script shims codex and pi install through cmd /c, which
// exits with them. A claude found only as a script is refused.
func TestANativeCFOStartsEachHarnessAsItsProgramNeeds(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude.exe", "codex.cmd", "pi.cmd"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("@echo off\r\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("ComSpec", `C:\Windows\System32\cmd.exe`)

	for harness, want := range map[string][]string{
		"claude": {filepath.Join(bin, "claude.exe")},
		"codex":  {`C:\Windows\System32\cmd.exe`, "/c", "codex"},
		"pi":     {`C:\Windows\System32\cmd.exe`, "/c", "pi"},
	} {
		program, err := nativeCFOProgram(harness)
		if err != nil || !slices.EqualFunc(program, want, strings.EqualFold) {
			t.Errorf("nativeCFOProgram(%s) = %q, %v; want %q", harness, program, err, want)
		}
	}
}

// laterHarness is a Herdr whose pane shows the typed harness running only
// from the given HarnessRunning look on.
type laterHarness struct {
	*terminaltest.Fake
	looks, from int
}

func (l *laterHarness) HarnessRunning(ctx context.Context, target herdr.Target) (bool, error) {
	l.looks++
	_, err := l.Fake.HarnessRunning(ctx, target)
	return l.looks >= l.from, err
}

// A typed CFO that Herdr does not detect and that takes a while to start is
// still reported once it shows running, on a later look; one that never shows
// ends the looks with a line saying so, no error, and the tab in front.
func TestATypedCFOIsLookedForUntilItShows(t *testing.T) {
	for _, c := range []struct {
		name        string
		from        int
		wantReports []string
		wantLine    bool
	}{
		{"shows on a later look", 3, []string{"ReportAgent fleet:w1:p1 pi unknown"}, false},
		{"never shows", cfoLaunchTries + 1, nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			noLaunchWait(t)
			fake := &laterHarness{Fake: &terminaltest.Fake{
				Session:   "fleet",
				Container: herdr.Container{Session: "fleet", WorkspaceID: "w1"},
				CFO:       herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"},
				Status:    herdr.AgentDead,
			}, from: c.from}
			var stdout strings.Builder

			// Act
			started, err := startCFOWith(context.Background(), fake, `C:\dev\app`, "pi", &stdout)

			// Assert
			var reports []string
			for _, call := range fake.Calls() {
				if strings.HasPrefix(call, "ReportAgent ") {
					reports = append(reports, call)
				}
			}
			if err != nil || !started || !slices.Equal(reports, c.wantReports) {
				t.Errorf("startCFOWith = %v, %v, reports %q; want %q", started, err, reports, c.wantReports)
			}
			if said := strings.Contains(stdout.String(), "Herdr never saw pi start in the cfo tab"); said != c.wantLine {
				t.Errorf("stdout = %q, want the never-saw line %v", stdout.String(), c.wantLine)
			}
			if calls := fake.Calls(); calls[len(calls)-1] != "Focus w1 w1:t1" {
				t.Errorf("calls = %q, want the cfo tab brought to the front last", calls)
			}
		})
	}
}

// noLaunchWait makes a typed CFO start wait no real time.
func noLaunchWait(t *testing.T) {
	t.Helper()
	sleep := cfoLaunchSleep
	cfoLaunchSleep = func(time.Duration) {}
	t.Cleanup(func() { cfoLaunchSleep = sleep })
}
