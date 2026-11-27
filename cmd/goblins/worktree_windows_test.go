package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// A home named through a link spells an extra worktree's path otherwise than
// the record, which holds the canonical path, and remove still finds it.
func TestWorktreeRemoveFindsTheExtraThroughALinkedHome(t *testing.T) {
	// Arrange: the extra is added through the home's canonical path and
	// removed through a junction to it.
	h, service := extraFixture(t)
	ctx := context.Background()
	if _, err := addExtraWorktree(ctx, h, service, "g1", "proof", "", ""); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "linked-home")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", linked, h.Root).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", linked, h.Root, err, out)
	}
	viaLink := home.Home{Root: linked, State: filepath.Join(linked, "state"), Data: filepath.Join(linked, "data")}

	// Act
	_, err := removeExtraWorktree(ctx, viaLink, worktree.Service{Commands: execx.OSRunner{}, Root: viaLink.Worktrees()}, worktree.RunnerGit{Commands: execx.OSRunner{}}, "g1", "proof", "")

	// Assert
	if err != nil {
		t.Fatalf("remove through the linked home = %v, want the recorded extra found and removed", err)
	}
	if meta, err := state.ReadTaskMeta(h.State, "g1"); err != nil || len(meta.Extras) != 0 {
		t.Errorf("the record's extras are %v, %v; want none", meta.Extras, err)
	}
	if _, err := os.Stat(filepath.Join(h.Worktrees(), "app", "g1-proof")); !os.IsNotExist(err) {
		t.Errorf("the extra worktree is still on disk after remove: %v", err)
	}
}
