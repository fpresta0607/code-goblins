package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbsCleanMakesAbsoluteCleanPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	got, err := AbsClean("nested/..\\target\r\n")
	if err != nil {
		t.Fatalf("AbsClean: %v", err)
	}
	want := filepath.Join(dir, "target")
	if !strings.EqualFold(got, want) {
		t.Errorf("AbsClean = %q, want %q", got, want)
	}
}

func TestSamePathNormalizesCaseSeparatorsAndLongPathPrefix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	withSlash := strings.TrimRight(filepath.ToSlash(dir), "\r\n") + "\n"
	withLongPrefix := `\\?\` + filepath.Clean(dir)
	if !SamePath(withSlash, withLongPrefix) {
		t.Errorf("SamePath(%q, %q) = false, want true", withSlash, withLongPrefix)
	}

	caseVariant := strings.ToUpper(filepath.Clean(dir))
	if !SamePath(caseVariant, dir) {
		t.Errorf("SamePath(%q, %q) = false, want true", caseVariant, dir)
	}
}

func TestCanonicalRefusesUnavailableSymlinkResolution(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Canonical(missing); err == nil {
		t.Fatal("Canonical returned nil error for a missing path")
	}
	if SamePath(missing, missing) {
		t.Fatal("SamePath equated a path whose canonical identity could not be resolved")
	}
}

func TestSamePathDoesNotEquatePrimaryAndLinkedWorktreePaths(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "primary")
	linked := filepath.Join(root, "linked-worktree")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}

	if SamePath(primary, linked) {
		t.Errorf("SamePath(%q, %q) = true, want false", primary, linked)
	}
}
