package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A stray is returned on the same rule the task's own worktrees get: clean and
// on the default branch, it goes; with commits of its own, its work is kept as
// an archive tag first; holding uncommitted work, it stays and is named.
func TestReturnStraysKeepsWorkBeforeItReturns(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	ctx := context.Background()
	git := RunnerGit{Commands: execx.OSRunner{}}
	scratch := filepath.Join(t.TempDir(), "linked-scratch")
	makeJunction(t, scratch, t.TempDir())
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
	registered, err := WorktreeList(ctx, execx.OSRunner{}, project)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve the spellings while the folders still exist. A returned folder
	// can no longer be compared by its directory identity.
	for _, path := range registered {
		if sameFolderPath(path, merged) {
			merged = path
		}
		if sameFolderPath(path, own) {
			own = path
		}
		if sameFolderPath(path, dirty) {
			dirty = path
		}
	}

	// Act
	returned, err := ReturnStrays(ctx, execx.OSRunner{}, git, project, []string{scratch})

	// Assert
	if err != nil {
		t.Fatalf("ReturnStrays: %v", err)
	}
	if len(returned) != 3 {
		t.Fatalf("returned = %+v, want all three listed", returned)
	}
	if stray := noteOf(t, returned, merged); stray.Kept != "" || stray.Tag != "" {
		t.Errorf("merged stray = %+v, want it returned with nothing kept", stray)
	}
	if _, err := os.Stat(merged); !os.IsNotExist(err) {
		t.Errorf("the merged stray survived: %v", err)
	}
	if stray := noteOf(t, returned, own); stray.Kept != "" || stray.Tag != "archive/own-work" {
		t.Errorf("unlanded stray = %+v, want it returned with its work kept as archive/own-work", stray)
	}
	if got := gitRun(t, project, "rev-parse", "refs/tags/archive/own-work^{commit}"); got != head {
		t.Errorf("archive/own-work = %s, want the stray's own commit %s", got, head)
	}
	if stray := noteOf(t, returned, dirty); stray.Kept == "" || !strings.Contains(stray.Kept, "uncommitted") {
		t.Errorf("dirty stray = %+v, want it kept for its uncommitted work", stray)
	}
	if _, err := os.Stat(filepath.Join(dirty, "draft.txt")); err != nil {
		t.Errorf("the uncommitted work was removed: %v", err)
	}
}

// noteOf is what became of the stray at folder.
func noteOf(t *testing.T, returned []Stray, folder string) Stray {
	t.Helper()
	for _, stray := range returned {
		if sameFolderPath(stray.Path, folder) {
			return stray
		}
	}
	t.Fatalf("returned = %+v, want %s among them", returned, folder)
	return Stray{}
}

// One folder has more than one spelling: git answers with an 8.3 short name
// where the home recorded the path in full, and a junction names its target's
// directory from elsewhere. The scan compares directory identity, so either
// spelling finds the same worktree.
func TestStraysFindAFolderThroughAnotherSpelling(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	ctx := context.Background()
	real := t.TempDir()
	stray := filepath.Join(real, "scratchpad", "probe")
	gitRun(t, project, "worktree", "add", "--detach", stray, "origin/main")
	link := filepath.Join(t.TempDir(), "linked")
	result, err := (execx.OSRunner{}).Run(ctx, execx.Request{Name: "cmd", Args: []string{"/c", "mklink", "/J", link, real}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("create junction: %v: %s", err, result.Stderr)
	}

	// Act
	throughTheLink, err := Strays(ctx, execx.OSRunner{}, project, []string{link})

	// Assert
	if err != nil {
		t.Fatalf("Strays: %v", err)
	}
	if len(throughTheLink) != 1 || !sameFolderPath(throughTheLink[0], stray) {
		t.Fatalf("strays = %v, want the worktree at %s found through the junction %s", throughTheLink, stray, link)
	}
}
