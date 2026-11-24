package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// A folder named .git in another case is a repository the container made,
// refused naming it, exactly where the file system opens it as .git.
func TestNestedGitRefusalNamesGitByTheFileSystemsCase(t *testing.T) {
	// Arrange
	worktree, _ := worktreeWithLink(t)
	planted := filepath.Join(worktree, "src", ".GIT")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(worktree, "src", ".git"))
	isOnePath := err == nil

	// Act
	err = NestedGitRefusal(worktree)

	// Assert
	if isRefused := err != nil; isRefused != isOnePath {
		t.Fatalf("NestedGitRefusal with %s = %v, want refused: %t, as the file system opens it as .git: %t", planted, err, isOnePath, isOnePath)
	}
}
