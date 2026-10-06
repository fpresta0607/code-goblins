package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// extraFixture is a home whose task g1 works in a checkout of a bare origin,
// with the worktree service cfo worktree uses.
func extraFixture(t *testing.T) (home.Home, worktree.Service) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	gitIn(t, root, "init", "-q", "--bare", "--initial-branch=main", origin)
	project := filepath.Join(root, "app")
	gitIn(t, root, "clone", "-q", origin, project)
	gitIn(t, project, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	gitIn(t, project, "push", "-q", "origin", "HEAD:main")
	gitIn(t, project, "remote", "set-head", "origin", "--auto")
	h := home.Home{Root: filepath.Join(root, "home"), State: filepath.Join(root, "home", "state"), Data: filepath.Join(root, "home", "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "g1", Project: project, Worktree: filepath.Join(h.Worktrees(), "app", "g1"), Harness: "claude", Mode: "direct-PR"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return h, worktree.Service{Commands: execx.OSRunner{}, Root: h.Worktrees()}
}

// cfo worktree add makes the task's extra worktree beside its own in the home
// and records it on the task; remove returns it and takes it off the record.
func TestWorktreeAddRecordsTheExtraAndRemoveTakesItOff(t *testing.T) {
	// Arrange
	h, service := extraFixture(t)
	ctx := context.Background()
	want := filepath.Join(h.Worktrees(), "app", "g1-proof")

	// Act
	_, addErr := addExtraWorktree(ctx, h, service, "g1", "proof", "")
	added, readErr := state.ReadTaskMeta(h.State, "g1")
	_, removeErr := removeExtraWorktree(ctx, h, service, worktree.RunnerGit{Commands: execx.OSRunner{}}, "g1", "proof")
	removed, rereadErr := state.ReadTaskMeta(h.State, "g1")

	// Assert
	if addErr != nil || readErr != nil || removeErr != nil || rereadErr != nil {
		t.Fatalf("add %v, read %v, remove %v, reread %v", addErr, readErr, removeErr, rereadErr)
	}
	if !slices.Equal(added.Extras, []string{want}) {
		t.Errorf("after add the record's extras are %v, want %s", added.Extras, want)
	}
	if len(removed.Extras) != 0 {
		t.Errorf("after remove the record's extras are %v, want none", removed.Extras)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Errorf("the extra worktree is still on disk after remove: %v", err)
	}
}

// remove refuses an extra worktree holding uncommitted work and changes
// nothing: the worktree, its work and the record all stay.
func TestWorktreeRemoveRefusesUncommittedWorkAndChangesNothing(t *testing.T) {
	// Arrange
	h, service := extraFixture(t)
	ctx := context.Background()
	if _, err := addExtraWorktree(ctx, h, service, "g1", "proof", ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Worktrees(), "app", "g1-proof")
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("not committed"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	_, err := removeExtraWorktree(ctx, h, service, worktree.RunnerGit{Commands: execx.OSRunner{}}, "g1", "proof")

	// Assert
	if err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("remove = %v, want a refusal naming the uncommitted work", err)
	}
	if data, err := os.ReadFile(filepath.Join(path, "work.txt")); err != nil || string(data) != "not committed" {
		t.Errorf("the uncommitted work changed: %q, %v", data, err)
	}
	if meta, err := state.ReadTaskMeta(h.State, "g1"); err != nil || !slices.Equal(meta.Extras, []string{path}) {
		t.Errorf("the record's extras are %v, %v; want the extra still recorded", meta.Extras, err)
	}
}
