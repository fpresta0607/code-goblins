package main

import (
	"path/filepath"
	"slices"
	"testing"
)

// A native terminal starts a program itself, and this system runs a script
// by its #! line, so each harness PATH finds, Claude Code's npm build
// included, starts as itself by its path.
func TestANativeCEOStartsEachHarnessByItsPath(t *testing.T) {
	// Arrange
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex", "pi"} {
		putScriptShim(t, bin, name)
	}
	t.Setenv("PATH", bin)

	for _, harness := range []string{"claude", "codex", "pi"} {
		// Act
		program, err := nativeCEOProgram(harness, "--version")

		// Assert
		if want := []string{filepath.Join(bin, harness), "--version"}; err != nil || !slices.Equal(program, want) {
			t.Errorf("nativeCEOProgram(%s) = %q, %v; want %q", harness, program, err, want)
		}
	}
}
