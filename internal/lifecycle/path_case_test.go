package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// Git answers for the worktree when it names the worktree's folder, in
// another case exactly where the file system opens the one by the other.
func TestPreserveWorkTakesGitsAnswerByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	worktree := filepath.Join(parent, "gb-work")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "GB-WORK")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	runner := &preservationRunner{root: upper, branch: "feat/work", head: "abc123", remote: "abc123"}

	// Act
	_, err = PreserveWork(context.Background(), runner, state.TaskMeta{ID: "work", Worktree: worktree, Mode: "direct-PR"}, "")

	// Assert
	if (err == nil) != isOnePath {
		t.Fatalf("PreserveWork with git naming %s for %s = %v, want it taken: %t, as the file system opens the one by the other: %t", upper, worktree, err, isOnePath, isOnePath)
	}
}

// A process works in a task's directory when its path is that directory or
// below it, in another case exactly where the file system opens the one by
// the other, and never in a folder whose name only begins with it.
func TestWithinDirectoryComparesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	directory := filepath.Join(parent, "gb-work")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "GB-WORK")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	cases := []struct {
		name       string
		path       string
		wantWithin bool
	}{
		{name: "the directory itself", path: directory, wantWithin: true},
		{name: "a folder below it", path: filepath.Join(directory, "src"), wantWithin: true},
		{name: "the directory in another case", path: upper, wantWithin: isOnePath},
		{name: "a folder below it in another case", path: filepath.Join(upper, "src"), wantWithin: isOnePath},
		{name: "a folder whose name it begins", path: directory + "-2", wantWithin: false},
		{name: "a relative path", path: "gb-work", wantWithin: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isWithin := withinDirectory(tc.path, directory)

			// Assert
			if isWithin != tc.wantWithin {
				t.Fatalf("withinDirectory(%s, %s) = %t, want %t", tc.path, directory, isWithin, tc.wantWithin)
			}
		})
	}
}
