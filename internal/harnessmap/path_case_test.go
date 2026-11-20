package harnessmap

import (
	"os"
	"path/filepath"
	"testing"
)

// Two spellings of a junction's target that differ only in case are one
// folder exactly where the file system opens the one by the other.
func TestSameDirComparesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	lower := filepath.Join(parent, "skills")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "SKILLS")
	_, err := os.Stat(upper)
	isOnePath := err == nil

	// Act
	isSame := sameDir(lower, upper)

	// Assert
	if isSame != isOnePath {
		t.Fatalf("sameDir(%s, %s) = %t, want %t", lower, upper, isSame, isOnePath)
	}
}

// A skill kept as a real folder in two skills folders under names that
// differ only in case is one skill kept twice exactly where the file system
// opens the one name by the other.
func TestCheckNamesDuplicateSkillsByTheFileSystemsCase(t *testing.T) {
	// Arrange
	shared, own := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(shared, "lavish"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(shared, "LAVISH"))
	isOnePath := err == nil
	if err := os.Mkdir(filepath.Join(own, "LAVISH"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := Map{SharedSkills: shared, Roots: []Root{{Harness: "codex", Path: own, Skills: own, Present: true}}}

	// Act
	problems := Check(m)

	// Assert
	if isDuplicate := len(problems) == 1 && problems[0].Kind == "duplicate"; isDuplicate != isOnePath || len(problems) > 1 {
		t.Fatalf("problems = %+v, want lavish kept twice: %t", problems, isOnePath)
	}
}
