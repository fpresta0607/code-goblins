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
	want := []string{inScratchpad, inCheckout}
	if len(strays) != len(want) {
		t.Fatalf("strays = %v, want the two inside the task's folders", strays)
	}
	for _, path := range want {
		if !lists(t, strays, path) {
			t.Errorf("strays = %v, want it among them as %s", strays, path)
		}
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

// sameFolderPath reports whether two paths name one folder. git spells a path
// through an 8.3 folder name, as C:\Users\RUNNER~1\... for
// C:\Users\runneradmin\..., so a comparison by spelling fails on a machine
// whose temporary folder has one.
func sameFolderPath(left, right string) bool {
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}

// lists reports whether strays names folder.
func lists(t *testing.T, strays []string, folder string) bool {
	t.Helper()
	for _, stray := range strays {
		if sameFolderPath(stray, folder) {
			return true
		}
	}
	return false
}

func TestStraysExcludeRecordedWorktreesInsideAnotherTaskFolder(t *testing.T) {
	project := clonedProject(t)
	scratch := t.TempDir()
	recorded := filepath.Join(scratch, "recorded")
	gitRun(t, project, "worktree", "add", "--detach", recorded, "origin/main")

	strays, err := Strays(context.Background(), execx.OSRunner{}, project, []string{scratch, recorded})

	if err != nil || len(strays) != 0 {
		t.Fatalf("Strays = %v, %v; a recorded folder is never a stray", strays, err)
	}
}

func TestReturnStraysKeepsNestedUncommittedWorkEvenWhenTheParentIgnoresIt(t *testing.T) {
	project := clonedProject(t)
	scratch := t.TempDir()
	parent := filepath.Join(scratch, "parent")
	nested := filepath.Join(parent, "nested")
	for _, path := range []string{parent, nested} {
		gitRun(t, project, "worktree", "add", "--detach", path, "origin/main")
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "info", "exclude"), []byte("nested/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(nested, "draft.txt")
	if err := os.WriteFile(draft, []byte("unfinished"), 0o644); err != nil {
		t.Fatal(err)
	}

	returned, err := ReturnStrays(context.Background(), execx.OSRunner{}, RunnerGit{Commands: execx.OSRunner{}}, project, []string{scratch})

	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(draft); err != nil || string(content) != "unfinished" {
		t.Fatalf("draft = %q, %v; returning a parent discarded its nested worktree: %+v", content, err, returned)
	}
	for _, stray := range returned {
		if stray.Kept == "" {
			t.Errorf("returned = %+v, want both worktrees kept", returned)
		}
	}
}
