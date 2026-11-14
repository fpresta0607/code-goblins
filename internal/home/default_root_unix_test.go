//go:build linux || darwin

package home

import (
	"path/filepath"
	"testing"
)

// pointDefaultAt points the per-user folder DefaultRoot reads at folder for
// the test, and returns the home DefaultRoot then names.
func pointDefaultAt(t *testing.T, folder string) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", folder)
	return filepath.Join(folder, "code-goblins")
}

// clearDefault leaves DefaultRoot no per-user folder to read for the test.
func clearDefault(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "")
}

func TestDefaultRootReadsTheUsersDataFolderOrFallsBackUnderHome(t *testing.T) {
	user := t.TempDir()
	data := t.TempDir()
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "XDG_DATA_HOME names a folder", data: data, want: filepath.Join(data, "code-goblins")},
		{name: "XDG_DATA_HOME is unset", data: "", want: filepath.Join(user, ".local", "share", "code-goblins")},
		{name: "XDG_DATA_HOME is relative, which the rules ignore", data: "relative/data", want: filepath.Join(user, ".local", "share", "code-goblins")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("HOME", user)
			t.Setenv("XDG_DATA_HOME", test.data)

			// Act
			root, err := DefaultRoot()

			// Assert
			if err != nil || root != test.want {
				t.Fatalf("DefaultRoot = %q, %v; want %q", root, err, test.want)
			}
		})
	}
}

func TestDefaultRootWithNoAbsoluteFolderIsRefused(t *testing.T) {
	// Arrange
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "relative")

	// Act
	root, err := DefaultRoot()

	// Assert
	if err == nil {
		t.Fatalf("DefaultRoot = %q, want a refusal naming GOBLINS_HOME", root)
	}
}
