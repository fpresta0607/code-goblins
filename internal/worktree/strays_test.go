package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A task's own folders are its worktree, its extra worktrees and its scratch
// folder. A harness's tooling leaves worktrees of its own inside them, and
// those are what this scan lists: everything else the project registers,
// its own checkout and the folders themselves included, is not this
// command's to return.
func TestStraysListOnlyWorktreesInsideTheTasksOwnFolders(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	ctx := context.Background()
	worktreePath := filepath.Join(t.TempDir(), "goblin")
	gitRun(t, project, "worktree", "add", "--detach", worktreePath, "origin/main")
	scratch := t.TempDir()
	inScratchpad := filepath.Join(scratch, "claude", "scratchpad", "probe")
	inCheckout := filepath.Join(worktreePath, ".claude", "worktrees", "probe")
	beside := filepath.Join(t.TempDir(), "another-task")
	for _, path := range []string{inScratchpad, inCheckout, beside} {
		gitRun(t, project, "worktree", "add", "--detach", path, "origin/main")
	}

	// Act
	strays, err := Strays(ctx, execx.OSRunner{}, project, []string{worktreePath, scratch})

	// Assert
	if err != nil {
		t.Fatalf("Strays: %v", err)
	}
	want := map[string]bool{filepath.Clean(inScratchpad): true, filepath.Clean(inCheckout): true}
	if len(strays) != len(want) {
		t.Fatalf("strays = %v, want the two inside the task's folders", strays)
	}
	for _, stray := range strays {
		if !want[filepath.Clean(stray)] {
			t.Errorf("strays = %v, want only %v", strays, want)
		}
	}
}

// A stray is returned on the same rule the task's own worktrees get: clean and
// on the default branch, it goes; with commits of its own, its work is kept as
// an archive tag first; holding uncommitted work, it stays and is named.
func TestReturnStraysKeepsWorkBeforeItReturns(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	ctx := context.Background()
	git := RunnerGit{Commands: execx.OSRunner{}}
	scratch := t.TempDir()
	merged := filepath.Join(scratch, "scratchpad", "merged")
	own := filepath.Join(scratch, "scratchpad", "own-work")
	dirty := filepath.Join(scratch, "scratchpad", "draft")
	for _, path := range []string{merged, own, dirty} {
		gitRun(t, project, "worktree", "add", "--detach", path, "origin/main")
	}
	gitRun(t, own, "commit", "-q", "--allow-empty", "-m", "work of its own")
	head := gitRun(t, own, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dirty, "draft.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	returned, err := ReturnStrays(ctx, execx.OSRunner{}, git, project, []string{scratch})

	// Assert
	if err != nil {
		t.Fatalf("ReturnStrays: %v", err)
	}
	byPath := map[string]Stray{}
	for _, stray := range returned {
		byPath[filepath.Clean(stray.Path)] = stray
	}
	if len(byPath) != 3 {
		t.Fatalf("returned = %+v, want all three listed", returned)
	}
	if stray := byPath[filepath.Clean(merged)]; stray.Kept != "" || stray.Tag != "" {
		t.Errorf("merged stray = %+v, want it returned with nothing kept", stray)
	}
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Errorf("the merged stray survived: %v", err)
	}
	if stray := byPath[filepath.Clean(own)]; stray.Kept != "" || stray.Tag != "archive/own-work" {
		t.Errorf("unlanded stray = %+v, want it returned with its work kept as archive/own-work", stray)
	}
	if got := gitRun(t, project, "rev-parse", "refs/tags/archive/own-work^{commit}"); got != head {
		t.Errorf("archive/own-work = %s, want the stray's own commit %s", got, head)
	}
	if stray := byPath[filepath.Clean(dirty)]; stray.Kept == "" || !strings.Contains(stray.Kept, "uncommitted") {
		t.Errorf("dirty stray = %+v, want it kept for its uncommitted work", stray)
	}
	if _, err := os.Stat(filepath.Join(dirty, "draft.txt")); err != nil {
		t.Errorf("the uncommitted work was removed: %v", err)
	}
}

// The command's output names each stray it returned and each it left, with the
// tag its work went under or the reason it stayed.
func TestStrayNoteNamesWhatBecameOfEach(t *testing.T) {
	// Arrange
	returned := []Stray{
		{Path: "C:/work/returned"},
		{Path: "C:/work/kept-work", Tag: "archive/kept-work"},
		{Path: "C:/work/stays", Kept: "it holds uncommitted work"},
	}

	// Act
	note := StrayNote(returned, "cleanup")

	// Assert
	for _, want := range []string{
		"\nreturned harness worktree C:/work/returned",
		"\nreturned harness worktree C:/work/kept-work, its unlanded work kept as the local tag archive/kept-work",
		"\nkept the harness worktree C:/work/stays, which the cleanup does not touch: it holds uncommitted work",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note = %q, want it to contain %q", note, want)
		}
	}
}
