package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// The board's first-run page starts the agent it is given, whatever harness
// goblins remembers, and reads the remembered one. With claude found only as
// a script and codex not on PATH, neither start can reach a host.
func TestTheFirstRunStartsTheAgentItIsGivenWhateverHarnessIsRemembered(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude.cmd"), []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	h := home.Home{State: t.TempDir()}
	if err := os.WriteFile(ceoHarnessPath(h.State), []byte("codex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := firstRunOn(h, t.TempDir(), true, func(string) error { return nil })

	// Act
	saved := run.SavedAgent()
	err := run.StartCEO("claude")

	// Assert
	if saved != "codex" {
		t.Errorf("the first-run page reads the remembered agent as %q, want codex", saved)
	}
	if err == nil || !strings.Contains(err.Error(), "the native build of Claude Code is claude.exe") {
		t.Fatalf("first-run StartCEO error = %v, want claude looked up rather than codex", err)
	}
}

// A native terminal starts a program itself: Claude Code's native build as
// it is, and the npm script shims codex and pi install through cmd /c, which
// exits with them, each by the path PATH finds it at. A claude found only as
// a script is refused.
func TestANativeCEOStartsEachHarnessAsItsProgramNeeds(t *testing.T) {
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
		"codex":  {`C:\Windows\System32\cmd.exe`, "/c", filepath.Join(bin, "codex.cmd")},
		"pi":     {`C:\Windows\System32\cmd.exe`, "/c", filepath.Join(bin, "pi.cmd")},
	} {
		program, err := nativeCEOProgram(harness)
		if err != nil || !slices.EqualFunc(program, want, strings.EqualFold) {
			t.Errorf("nativeCEOProgram(%s) = %q, %v; want %q", harness, program, err, want)
		}
	}
}
