package install

import (
	"os"
	"path/filepath"
	"testing"
)

// A file a previous install listed in another case than this build ships it
// is the shipped file, and stays, exactly where the home's file system opens
// the one spelling by the other; elsewhere it is a file this build no longer
// ships, and goes.
func TestRemoveUnshippedComparesListedFilesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	root := t.TempDir()
	listed := filepath.Join(root, "skills", "Foo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(listed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(listed, []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(root, "skills", "foo", "SKILL.md"))
	isOnePath := err == nil
	service := Service{Root: root}

	// Act
	removed, err := service.removeUnshipped([]string{"skills/Foo/SKILL.md"}, []string{"skills/foo/SKILL.md"})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(listed); (statErr == nil) != isOnePath || (removed == 0) != isOnePath {
		t.Fatalf("%s kept: %t after %d removed, want kept: %t", listed, statErr == nil, removed, isOnePath)
	}
}
