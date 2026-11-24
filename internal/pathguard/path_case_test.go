package pathguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A write to .git in another case in a repository-free folder makes a
// repository exactly where the file system opens that spelling as .git, so
// it is refused there and goes on elsewhere.
func TestToolRefusalNamesGitByTheFileSystemsCase(t *testing.T) {
	// Arrange
	f := newFixture(t)
	if err := os.Mkdir(filepath.Join(f.worktree, "probe"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(f.worktree, "PROBE"))
	isOnePath := err == nil
	boundary := f.boundary
	boundary.RepositoryFree = []string{f.worktree}
	path := filepath.Join(f.worktree, "src", ".GIT", "HEAD")

	// Act
	refusal := boundary.ToolRefusal("Edit", toolInput(t, map[string]string{"file_path": path}))

	// Assert
	if isRefused := strings.Contains(refusal, "a .git in "+f.worktree); isRefused != isOnePath {
		t.Fatalf("an edit of %s gave %q, want it refused as a .git in %s: %t", path, refusal, f.worktree, isOnePath)
	}
	if !isOnePath && refusal != "" {
		t.Fatalf("an edit of %s was refused: %s", path, refusal)
	}
}
