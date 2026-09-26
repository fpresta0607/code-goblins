package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// goblins --harness chooses the harness the CFO starts as, and the home
// remembers it: a later goblins, in Herdr or native, starts that harness
// until another is chosen. A CFO that is not Claude Code is told it has no
// wake path.
func TestGoblinsRemembersTheCFOHarnessForEveryLaterStart(t *testing.T) {
	f := newSessionFixture(t)

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
		if !strings.Contains(out, "The CFO starts as codex in "+f.project+".") || !strings.Contains(out, "A codex CFO has no wake path") {
			t.Errorf("stdout = %q, want the codex start and its missing wake path", out)
		}
	}
	if !strings.Contains(nativeOut, "The CFO starts as pi in "+f.project+", in native terminal cfo.") || !strings.Contains(nativeOut, "A pi CFO has no wake path") {
		t.Errorf("stdout = %q, want the native pi start and its missing wake path", nativeOut)
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
	f := newSessionFixture(t)
	f.withLiveCFO()

	exit, stdout, stderr := f.launch("--harness", "codex")

	if exit != 0 || len(f.harnesses) != 0 {
		t.Fatalf("exit=%d harnesses=%q stderr=%q, want nothing started", exit, f.harnesses, stderr)
	}
	if !strings.Contains(stdout, "The CFO already runs, and keeps its harness; codex is the harness goblins starts the next CFO as.") {
		t.Errorf("stdout = %q, want it to say the live CFO keeps its harness", stdout)
	}
	if harness, err := cfoHarness(f.home.State); err != nil || harness != "codex" {
		t.Errorf("remembered harness = %q, %v; want codex", harness, err)
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

// In Herdr the harness is the kind the CFO's agent starts as.
func TestStartingTheCFOStartsTheChosenHarnessInHerdr(t *testing.T) {
	fake := &terminaltest.Fake{
		Session:   "fleet",
		Container: herdr.Container{Session: "fleet", WorkspaceID: "w1"},
		CFO:       herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p1"}, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1"},
	}

	if _, err := startCFOWith(context.Background(), fake, `C:\dev\app`, "codex"); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(fake.Calls(), "AgentStart fleet:w1:p1 cfo codex") {
		t.Errorf("calls = %q, want the CFO's agent started as codex", fake.Calls())
	}
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
