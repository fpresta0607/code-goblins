package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// goblins --harness chooses the harness the CFO starts as, and the home
// remembers it: a later goblins starts that harness until another is chosen.
// A CFO that is not Claude Code is told it has no wake path.
func TestGoblinsRemembersTheCFOHarnessForEveryLaterStart(t *testing.T) {
	f := newSessionFixture(t)
	harnessesOnPath(t, "codex", "pi")

	chose, chooseOut, chooseErr := f.launch("--harness", "codex")
	later, laterOut, _ := f.launch()
	again, againOut, _ := f.launch("--harness", "pi")

	if chose != 0 || later != 0 || again != 0 {
		t.Fatalf("exits %d, %d, %d (stderr %q), want 0", chose, later, again, chooseErr)
	}
	if want := []string{"codex", "codex", "pi"}; !slices.Equal(f.harnesses, want) {
		t.Errorf("harnesses started = %q, want %q", f.harnesses, want)
	}
	if len(f.nativeStarts) != 3 {
		t.Errorf("native starts %q, want three", f.nativeStarts)
	}
	for _, out := range []string{chooseOut, laterOut} {
		if !strings.Contains(out, "The CFO starts as codex in "+f.project+", in native terminal cfo.") || !strings.Contains(out, "A codex CFO has no wake path") {
			t.Errorf("stdout = %q, want the codex start and its missing wake path", out)
		}
	}
	if !strings.Contains(againOut, "The CFO starts as pi in "+f.project+", in native terminal cfo.") || !strings.Contains(againOut, "A pi CFO has no wake path") {
		t.Errorf("stdout = %q, want the pi start and its missing wake path", againOut)
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
	exit, _, stderr = f.launch()
	if exit != 1 || !strings.Contains(stderr, "choose again with goblins --harness") || len(f.harnesses) != 0 {
		t.Errorf("exit=%d stderr=%q harnesses=%q, want the unreadable choice refused", exit, stderr, f.harnesses)
	}
}

// goblins takes only its flags: an argument left after them is refused with
// a message that names it, never with a bare exit code.
func TestGoblinsNamesAnArgumentItDoesNotTake(t *testing.T) {
	f := newSessionFixture(t)

	exit, _, stderr := f.launch("--harness", "claude", "codex")

	if exit != 2 || !strings.Contains(stderr, `unexpected argument "codex"`) || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d stderr=%q harnesses=%q, want the argument named and nothing started", exit, stderr, f.harnesses)
	}
}

// A harness whose program is not on PATH is refused before it is remembered,
// so a choice that cannot start never sticks.
func TestGoblinsRefusesAHarnessNotOnPath(t *testing.T) {
	// Arrange
	f := newSessionFixture(t)
	harnessesOnPath(t)

	// Act
	exit, _, stderr := f.launch("--harness", "codex")

	// Assert
	if exit != 1 || !strings.Contains(stderr, "codex is not on PATH") || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d stderr=%q harnesses=%q, want codex refused and nothing started", exit, stderr, f.harnesses)
	}
	if _, err := os.Stat(cfoHarnessPath(f.home.State)); !os.IsNotExist(err) {
		t.Errorf("a harness not on PATH was remembered: %v", err)
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
