package access_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/access"
	"github.com/fpresta0607/code-goblins/internal/access/accesstest"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNarrowLeavesAFolderAndWhatItHoldsToItsOwnerSystemAndAdministrators(t *testing.T) {
	// Arrange
	folder := filepath.Join(accesstest.DriveRootLike(t), "CodeGoblins")
	held := filepath.Join(folder, "worktrees", "project", "task", "main.go")
	writeFile(t, held)
	want := accesstest.Narrowed(t)
	if sids, _ := accesstest.Allowed(t, held); !slices.Contains(sids, accesstest.AuthenticatedUsers) {
		t.Fatalf("the file starts with access for %v, want Authenticated Users among them for the test to mean anything", sids)
	}

	if isNarrow, err := access.IsNarrow(folder); err != nil || isNarrow {
		t.Fatalf("IsNarrow before = %v, %v; want false", isNarrow, err)
	}

	// Act
	err := access.Narrow(folder)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if sids, isProtected := accesstest.Allowed(t, folder); !slices.Equal(sids, want) || !isProtected {
		t.Errorf("the folder allows %v, protected %v; want only %v, taking nothing from its parent", sids, isProtected, want)
	}
	if sids, _ := accesstest.Allowed(t, held); !slices.Equal(sids, want) {
		t.Errorf("a file already in the folder allows %v, want only %v", sids, want)
	}
	made := filepath.Join(folder, "scratch", "task", "new.txt")
	writeFile(t, made)
	if sids, _ := accesstest.Allowed(t, made); !slices.Equal(sids, want) {
		t.Errorf("a file made after allows %v, want only %v", sids, want)
	}
	if isNarrow, err := access.IsNarrow(folder); err != nil || !isNarrow {
		t.Errorf("IsNarrow after = %v, %v; want true", isNarrow, err)
	}
}

func TestNarrowNeverReachesThroughAJunction(t *testing.T) {
	// Arrange
	root := accesstest.DriveRootLike(t)
	folder := filepath.Join(root, "CodeGoblins")
	outside := filepath.Join(root, "checkout")
	writeFile(t, filepath.Join(outside, "main.go"))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(folder, "link")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	// Act
	if err := access.Narrow(folder); err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, path := range []string{outside, filepath.Join(outside, "main.go")} {
		if sids, _ := accesstest.Allowed(t, path); !slices.Contains(sids, accesstest.AuthenticatedUsers) {
			t.Errorf("%s allows %v after Narrow; want what the junction points at left as it was", path, sids)
		}
	}
}

func TestIsNarrowRefusesAFolderThatLetsAnotherAccountIn(t *testing.T) {
	// Arrange
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := access.Limit(folder, accesstest.System, accesstest.Administrators, accesstest.Self(t), accesstest.AuthenticatedUsers); err != nil {
		t.Fatal(err)
	}

	// Act
	isNarrow, err := access.IsNarrow(folder)

	// Assert
	if err != nil || isNarrow {
		t.Errorf("IsNarrow = %v, %v; want a folder that also lets Authenticated Users in read as not narrow", isNarrow, err)
	}
}

// A folder this account may not change the list of is named, and the rest
// is changed all the same.
func TestNarrowNamesWhatItCouldNotChangeAndChangesTheRest(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("an elevated administrator may change any list here")
	}
	// Arrange
	parent := t.TempDir()
	folder := filepath.Join(parent, "CodeGoblins")
	for _, name := range []string{"a", "stuck", "z"} {
		writeFile(t, filepath.Join(folder, name, "main.go"))
	}
	accesstest.Unchangeable(t, parent, filepath.Join(folder, "stuck"))

	// Act
	err := access.Narrow(folder)

	// Assert
	var unchanged *access.Unchanged
	if !errors.As(err, &unchanged) || unchanged.Count != 1 || len(unchanged.Examples) != 1 || !strings.HasPrefix(unchanged.Examples[0], filepath.Join(folder, "stuck")+": ") {
		t.Fatalf("Narrow = %v, want it to name the one folder whose list it could not change", err)
	}
	for _, name := range []string{"a", "z"} {
		if sids, _ := accesstest.Allowed(t, filepath.Join(folder, name, "main.go")); !slices.Equal(sids, accesstest.Narrowed(t)) {
			t.Errorf("%s allows %v, want only %v", name, sids, accesstest.Narrowed(t))
		}
	}
}
