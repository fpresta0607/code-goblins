//go:build !windows

package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A removal keeps only the worktree's .git link for last: an entry named
// .GIT is that link exactly where the file system reads the two names as one
// and is removed with the rest elsewhere. A folder this process cannot empty
// stops the removal after the entries before it, which shows which were
// kept.
func TestRunnerGitRemoveKeepsOnlyTheGitLinkByTheFileSystemsCase(t *testing.T) {
	// Arrange
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, ".GIT"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(filepath.Join(worktree, ".git"))
	isOnePath := statErr == nil
	locked := filepath.Join(worktree, "z")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "held"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	runner := &scriptedRunner{results: []scriptedResult{{}, {}}}

	// Act
	err := (RunnerGit{Commands: runner}).Remove(context.Background(), t.TempDir(), worktree)

	// Assert
	if _, heldErr := os.Stat(filepath.Join(locked, "held")); err == nil || heldErr != nil {
		t.Fatalf("Remove = %v and the held file is still there: %t, want the removal stopped at %s", err, heldErr == nil, locked)
	}
	if _, linkErr := os.Stat(filepath.Join(worktree, ".GIT")); (linkErr == nil) != isOnePath {
		t.Fatalf(".GIT is kept: %t, want %t, as the file system reads it as .git: %t", linkErr == nil, isOnePath, isOnePath)
	}
}
