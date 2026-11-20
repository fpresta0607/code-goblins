package projectcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A checkout typed in another case is named as its folder is spelled on
// disk exactly where the file system opens the one spelling by the other,
// and as it was typed elsewhere.
func TestProjectNameSpellsTheCheckoutByTheFileSystemsCase(t *testing.T) {
	t.Parallel()
	// Arrange
	parent := t.TempDir()
	if err := os.Mkdir(filepath.Join(parent, "northwind"), 0o755); err != nil {
		t.Fatal(err)
	}
	typed := filepath.Join(parent, "NORTHWIND")
	_, err := os.Stat(typed)
	isOnePath := err == nil
	want := "NORTHWIND"
	if isOnePath {
		want = "northwind"
	}

	// Act
	name := projectName(typed, t.TempDir())

	// Assert
	if name != want {
		t.Fatalf("projectName(%s) = %q, want %q, as the file system opens northwind by NORTHWIND: %t", typed, name, want, isOnePath)
	}
}

// A checkout whose home folder is spelled in another case is filed exactly
// where the file system opens the one name by the other, and that folder
// has its checkout there.
func TestUnfiledKnowsTheHomesFolderForACheckoutByTheFileSystemsCase(t *testing.T) {
	t.Parallel()
	// Arrange
	base := t.TempDir()
	root, data := filepath.Join(base, "dev"), filepath.Join(base, "home", "data")
	writeFile(t, filepath.Join(root, "northwind", ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(data, "projects", "NORTHWIND", "auth.json"), `{"project":"NORTHWIND","services":[]}`)
	_, err := os.Stat(filepath.Join(root, "NORTHWIND"))
	isOnePath := err == nil

	// Act
	lines, err := Unfiled(UnfiledOptions{DataDir: data, ProjectsRoot: root})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	report := Report{Lines: lines}
	isUnfiled, isGone := false, false
	for _, line := range lines {
		isUnfiled = isUnfiled || line.Check == "checkout-unfiled"
		isGone = isGone || line.Check == "checkouts-examined" && strings.Contains(line.Evidence, "with no checkout directly under the root")
	}
	if isUnfiled == isOnePath || isGone == isOnePath {
		t.Fatalf("northwind named unfiled: %t and NORTHWIND named gone: %t, want both %t:\n%s", isUnfiled, isGone, !isOnePath, report.Text())
	}
}
