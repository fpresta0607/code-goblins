package worktree

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// A goblin's worktree under the home leaves its checkout exactly as it was:
// no .worktrees folder, no line in the clone's exclude file, and nothing for
// the checkout's git status to show.
func TestAcquireLeavesTheCheckoutAsItWas(t *testing.T) {
	// Arrange
	project := clonedProject(t)
	exclude := filepath.Join(project, ".git", "info", "exclude")
	excludeBefore, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore := entryNames(t, project)
	path := filepath.Join(t.TempDir(), "worktrees", filepath.Base(project), "task-1")

	// Act
	got, err := (RunnerGit{Commands: execx.OSRunner{}}).Acquire(context.Background(), project, path, "")

	// Assert
	if err != nil || got != path {
		t.Fatalf("Acquire = %q, %v; want the worktree at %s", got, err, path)
	}
	if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
		t.Errorf("the worktree holds no checkout: %v", err)
	}
	if excludeAfter, err := os.ReadFile(exclude); err != nil || string(excludeAfter) != string(excludeBefore) {
		t.Errorf("the clone's exclude file changed:\n%s", excludeAfter)
	}
	if entriesAfter := entryNames(t, project); !slices.Equal(entriesAfter, entriesBefore) {
		t.Errorf("the checkout holds %v after the acquire, want %v", entriesAfter, entriesBefore)
	}
	if status := gitRun(t, project, "status", "--porcelain", "--ignored"); status != "" {
		t.Errorf("the checkout's git status shows %q, want nothing", status)
	}
}
