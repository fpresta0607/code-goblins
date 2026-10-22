package access_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

func TestIsNarrowReadsOnlyTheExactNarrowedListAsNarrowed(t *testing.T) {
	self := accesstest.Self(t)
	cases := map[string]struct {
		sddl        string
		isProtected bool
		want        bool
	}{
		"narrowed":                             {"D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + self + ")", true, true},
		"another account let in too":           {"D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + self + ")(A;OICI;FA;;;AU)", true, false},
		"another account in the owner's place": {"D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;AU)", true, false},
		"not handed on to what it holds":       {"D:(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;" + self + ")", true, false},
		"modify rather than full control":      {"D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1301bf;;;" + self + ")", true, false},
		"taking access from its folder too":    {"D:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + self + ")", false, false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			// Arrange
			folder := filepath.Join(accesstest.DriveRootLike(t), "CodeGoblins")
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			accesstest.SetList(t, folder, test.sddl, test.isProtected)

			// Act
			isNarrow, err := access.IsNarrow(folder)

			// Assert
			if err != nil || isNarrow != test.want {
				t.Errorf("IsNarrow = %v, %v; want %v", isNarrow, err, test.want)
			}
		})
	}
}

// A folder this account may not change the list of is named, the rest is
// changed all the same, and the folder reads as not narrowed until a
// narrowing changes everything.
func TestNarrowNamesWhatItCouldNotChangeAndTriesAgainLater(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	folder := filepath.Join(parent, "CodeGoblins")
	stuck := filepath.Join(folder, "stuck")
	for _, name := range []string{"a", "stuck", "z"} {
		writeFile(t, filepath.Join(folder, name, "main.go"))
	}
	accesstest.Unchangeable(t, parent, stuck)

	// Act
	err := access.Narrow(folder)

	// Assert
	var unchanged *access.Unchanged
	if !errors.As(err, &unchanged) || unchanged.Count != 1 || len(unchanged.Examples) != 1 || !strings.HasPrefix(unchanged.Examples[0], stuck+": ") {
		t.Fatalf("Narrow = %v, want it to name the one folder whose list it could not change", err)
	}
	for _, name := range []string{"a", "z"} {
		if sids, _ := accesstest.Allowed(t, filepath.Join(folder, name, "main.go")); !slices.Equal(sids, accesstest.Narrowed(t)) {
			t.Errorf("%s allows %v, want only %v", name, sids, accesstest.Narrowed(t))
		}
	}
	if isNarrow, err := access.IsNarrow(folder); err != nil || isNarrow {
		t.Errorf("IsNarrow after an unfinished narrowing = %v, %v; want false, so the next look narrows it again", isNarrow, err)
	}
	if err := os.RemoveAll(stuck); err != nil {
		t.Fatal(err)
	}
	if err := access.Narrow(folder); err != nil {
		t.Fatalf("Narrow once nothing is left unchangeable = %v", err)
	}
	if isNarrow, err := access.IsNarrow(folder); err != nil || !isNarrow {
		t.Errorf("IsNarrow after a finished narrowing = %v, %v; want true", isNarrow, err)
	}
}
