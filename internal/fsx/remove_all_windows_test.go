package fsx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RemoveAll is what removes a goblin's worktree, whose node_modules pnpm
// fills with junctions into a store every project on the machine shares. So
// it is held to removing a link as a link here, against the Go release this
// builds with: one that walked into a junction would delete the store.
func TestRemoveAllRemovesALinkAsALinkAndEveryFilePnpmMakes(t *testing.T) {
	// Arrange
	folder := filepath.Join(t.TempDir(), "worktree")
	pnpm := filepath.Join(folder, "node_modules", ".pnpm")
	store := filepath.Join(t.TempDir(), "store")
	stored := filepath.Join(store, "index.js")
	readOnly := filepath.Join(pnpm, "a@1.0.0", "index.js")
	long := filepath.Join(pnpm, "b@1.0.0", strings.Repeat("segment-of-a-deep-package-path"+string(filepath.Separator), 8), "index.js")
	if len(long) <= 260 {
		t.Fatalf("the long path is %d characters, want past 260", len(long))
	}
	for _, path := range []string{stored, readOnly, long} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("a file of the shared store"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(readOnly, 0o444); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(folder, "node_modules", "linked"): store,
		filepath.Join(folder, "node_modules", "a"):      filepath.Join(pnpm, "a@1.0.0"),
	} {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
		}
	}

	// Act
	err := RemoveAll(folder)

	// Assert
	if err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := os.Lstat(folder); !os.IsNotExist(err) {
		t.Errorf("the folder is still there: %v", err)
	}
	if content, err := os.ReadFile(stored); err != nil || string(content) != "a file of the shared store" {
		t.Errorf("the store's file behind the junction is %q, %v, want it untouched", content, err)
	}
}
