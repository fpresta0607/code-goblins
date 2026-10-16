package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// The Codex CFO takes its native session hooks in its own launch, as a
// Codex goblin does, and a Pi CFO none there, since its extension carries
// them; nothing is written to a file every session of the harness loads.
func TestTheCodexCFOStartsWithItsNativeSessionHooks(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}

	// Act
	codex, codexErr := cfoHookArguments(h, "codex")
	pi, piErr := cfoHookArguments(h, "pi")

	// Assert
	if codexErr != nil || piErr != nil {
		t.Fatalf("cfoHookArguments = %v, %v", codexErr, piErr)
	}
	want, err := nativehook.CodexArguments(filepath.Join(root, home.BinDir, "cfo.exe"), h.Root, h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(codex, want) {
		t.Errorf("the Codex CFO starts with %q, want %q", codex, want)
	}
	if len(pi) != 0 {
		t.Errorf("the Pi CFO starts with %q, want none", pi)
	}
}
