package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// A path a failure prints after a worktree it ran in names a file of the
// branch; the worktree spelled in another case is that worktree exactly where
// the file system opens the one by the other.
func TestIsNamedAnchorsAPathAtAWorktreeByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	worktree := filepath.Join(parent, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "WORKTREE")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	roots := []string{filepath.ToSlash(worktree)}
	cases := []struct {
		name      string
		root      string
		wantNamed bool
	}{
		{name: "after the worktree", root: worktree, wantNamed: true},
		{name: "after the worktree in another case", root: upper, wantNamed: isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := "panic: boom\n\t" + filepath.ToSlash(tc.root) + "/internal/x/data.go:12 +0x1d\n"

			// Act
			isFileNamed := isNamed(text, "internal/x/data.go", roots, nil)

			// Assert
			if isFileNamed != tc.wantNamed {
				t.Fatalf("isNamed(%q, %q) = %t, want %t", text, roots, isFileNamed, tc.wantNamed)
			}
		})
	}
}

// A folder a program reports in another case is the folder computed exactly
// where the file system opens the one by the other; elsewhere that spelling
// is a folder of its own, here made beside it.
func TestSameFolderComparesSpellingsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	want := filepath.Join(parent, "gate")
	if err := os.Mkdir(want, 0o755); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(parent, "GATE")
	_, err := os.Stat(got)
	isOnePath := err == nil
	if !isOnePath {
		if err := os.Mkdir(got, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	isSame := sameFolder(got, want)

	// Assert
	if isSame != isOnePath {
		t.Fatalf("sameFolder(%s, %s) = %t, want %t", got, want, isSame, isOnePath)
	}
}

// A project manifest is the gate's source project's when it names the
// checkout's folder, in another case exactly where the file system opens the
// one by the other.
func TestProjectTestEnvironmentMatchesTheManifestsProjectByTheFileSystemsCase(t *testing.T) {
	// Arrange
	data, _, worktree, python := existingEnvironmentFixture(t)
	checkout := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(checkout), "PROJECT"))
	isOnePath := err == nil
	writeEnvironmentManifest(t, data, checkout, `{"project":"PROJECT","test_environment":{"python_version":"3.12.10"}}`)
	serial := 0
	commands := runnerFunc(func(_ context.Context, _ execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: environmentIdentity(t, python, filepath.Dir(filepath.Dir(python)), checkout, "3.12.10", "final", &serial)}, nil
	})

	// Act
	_, err = ProjectTestEnvironment(context.Background(), commands, data, checkout, worktree)

	// Assert
	isRefused := err != nil && strings.Contains(err.Error(), "project manifest identity does not match")
	if isRefused == isOnePath {
		t.Fatalf("ProjectTestEnvironment for %s with a manifest naming PROJECT = %v, want refused for its identity: %t", checkout, err, !isOnePath)
	}
}
