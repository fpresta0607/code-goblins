package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// The Codex CEO takes its native session hooks in its own launch, as a
// Codex goblin does, and a Pi CEO none there, since its extension carries
// them; nothing is written to a file every session of the harness loads.
func TestTheCodexCEOStartsWithItsNativeSessionHooks(t *testing.T) {
	// Arrange
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}

	// Act
	codex, codexErr := ceoHookArguments(h, "codex")
	pi, piErr := ceoHookArguments(h, "pi")

	// Assert
	if codexErr != nil || piErr != nil {
		t.Fatalf("ceoHookArguments = %v, %v", codexErr, piErr)
	}
	want, err := nativehook.CodexArguments(filepath.Join(root, home.BinDir, "goblins.exe"), h.Root, h.State)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(codex, want) {
		t.Errorf("the Codex CEO starts with %q, want %q", codex, want)
	}
	if len(pi) != 0 {
		t.Errorf("the Pi CEO starts with %q, want none", pi)
	}
}

// A home under a path with a space, as a profile named for a person can be,
// still starts its Codex CEO, without per-launch hooks, as before they came.
func TestACodexCEOOfAHomeUnderAPathWithASpaceStillStarts(t *testing.T) {
	// Arrange
	root := filepath.Join(t.TempDir(), "John Smith")
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}

	// Act
	hooks, err := ceoHookArguments(h, "codex")

	// Assert
	if err != nil || len(hooks) != 0 {
		t.Fatalf("ceoHookArguments = %q, %v; want the CEO started with no hooks", hooks, err)
	}
}
