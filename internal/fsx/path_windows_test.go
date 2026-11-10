package fsx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Go's filepath.EvalSymlinks fails with "The system cannot find the path
// specified." for any path below a directory junction, so a gate home whose
// worktrees folder is a junction to a Dev Drive had no canonical identity
// (issue #2277).
func TestCanonicalResolvesThroughADirectoryJunction(t *testing.T) {
	for _, name := range []string{"the junction", "a folder below the junction", "a file below the junction"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if err := os.MkdirAll(filepath.Join(target, "child"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "child", "file.txt"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
				t.Fatalf("mklink /J: %v: %s", err, out)
			}
			below := map[string]string{"the junction": "", "a folder below the junction": "child", "a file below the junction": filepath.Join("child", "file.txt")}[name]
			linked, physical := filepath.Join(link, below), filepath.Join(target, below)

			// Act
			got, err := Canonical(linked)

			// Assert
			want, wantErr := Canonical(physical)
			if err != nil || wantErr != nil || !strings.EqualFold(got, want) {
				t.Fatalf("Canonical(%q) = %q, %v; want %q, %v", linked, got, err, want, wantErr)
			}
			// The temporary root may itself sit behind a junction, as a gate
			// home moved to a Dev Drive has, so the physical path is spelled
			// from the root's own canonical identity.
			canonicalRoot, err := Canonical(root)
			if err != nil {
				t.Fatal(err)
			}
			wantPhysical := filepath.Join(canonicalRoot, "target", below)
			if !strings.EqualFold(got, wantPhysical) {
				t.Errorf("Canonical(%q) = %q, want the physical path %q", linked, got, wantPhysical)
			}
			if !SamePath(linked, physical) {
				t.Errorf("SamePath(%q, %q) = false, want true", linked, physical)
			}
		})
	}
}

// ShortPath is the reverse of LongPath, name for name, so a path can be
// matched to a folder under either spelling without being looked up itself.
func TestShortPathSpellsTheFolderLongPathSpells(t *testing.T) {
	// Arrange
	long := LongPath(filepath.Join(t.TempDir(), "a folder with a long name"))
	missing := filepath.Join(long, "never made")
	if err := os.Mkdir(long, 0o755); err != nil {
		t.Fatal(err)
	}

	// Act
	short, unchanged := ShortPath(long), ShortPath(missing)

	// Assert
	if unchanged != missing {
		t.Errorf("ShortPath(%q) = %q, want a missing path as given", missing, unchanged)
	}
	if short == long {
		t.Skipf("the volume keeps no 8.3 short name for %s", long)
	}
	separator := string(filepath.Separator)
	if got := LongPath(short); !strings.EqualFold(got, long) || strings.Count(short, separator) != strings.Count(long, separator) {
		t.Errorf("ShortPath(%q) = %q, which LongPath spells %q", long, short, got)
	}
}
