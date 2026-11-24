package home

import "testing"

// Windows reads a name in any case, so the older layout's folder names are
// found however they are spelled.
func TestLocateWorktreeReadsTheOlderLayoutSpelledInAnyCaseOnWindows(t *testing.T) {
	// Arrange
	root := `C:\Users\op\AppData\Local\CodeGoblins\worktrees`
	dir := `c:\DEV\app\.WORKTREES\GB-Task-3\internal`
	want := WorktreePlace{Root: `c:\DEV\app\.WORKTREES\GB-Task-3`, Project: "app", Name: "Task-3"}

	// Act
	got, ok := LocateWorktree([]string{root}, dir)

	// Assert
	if !ok || got != want {
		t.Errorf("LocateWorktree(%q) = %+v, %v; want %+v", dir, got, ok, want)
	}
}
