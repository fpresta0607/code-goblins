//go:build linux || darwin

package access

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNarrowKeepsAFolderToItsOwnerAndLeavesWhatItHolds(t *testing.T) {
	// Arrange
	folder := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(folder, "record.json")
	if err := os.WriteFile(inside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(inside, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := IsNarrow(folder)
	if err != nil || before {
		t.Fatalf("a folder others can open reads narrowed = %v, %v", before, err)
	}

	// Act
	narrowErr := Narrow(folder)

	// Assert
	if narrowErr != nil {
		t.Fatal(narrowErr)
	}
	if info, err := os.Stat(folder); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("narrowed folder = %v, %v; want mode 0700", info.Mode(), err)
	}
	if after, err := IsNarrow(folder); err != nil || !after {
		t.Fatalf("a narrowed folder reads narrowed = %v, %v", after, err)
	}
	if info, err := os.Stat(inside); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("a file in the folder = %v, %v; want its own mode 0644 kept", info.Mode(), err)
	}
}

func TestIsNarrowReadsAnyPermissionOfTheGroupOrOthersAsOpen(t *testing.T) {
	for _, mode := range []os.FileMode{0o710, 0o701, 0o750, 0o705} {
		t.Run(mode.String(), func(t *testing.T) {
			// Arrange
			folder := t.TempDir()
			if err := os.Chmod(folder, mode); err != nil {
				t.Fatal(err)
			}

			// Act
			isNarrow, err := IsNarrow(folder)

			// Assert
			if err != nil || isNarrow {
				t.Fatalf("a folder with mode %v reads narrowed = %v, %v", mode, isNarrow, err)
			}
		})
	}
}
