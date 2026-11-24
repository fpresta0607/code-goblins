//go:build !windows

package localsocket

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A socket's path must fit sun_path, 104 bytes on macOS, whatever the length
// of the home it serves, and each home has a folder of its own.
func TestTheFolderFitsSunPathAndIsEachHomesOwn(t *testing.T) {
	// Arrange
	long := filepath.Join(t.TempDir(), strings.Repeat("a-long-folder-name/", 12), "state")
	other := filepath.Join(t.TempDir(), "state")

	// Act
	folder := Folder(long)

	// Assert
	if path := filepath.Join(folder, "run-requests.sock"); len(path) >= 104 {
		t.Fatalf("the socket path %s is %d bytes, want under 104", path, len(path))
	}
	if Folder(long) != folder {
		t.Fatalf("Folder of one home = %s and %s, want the same", folder, Folder(long))
	}
	if Folder(other) == folder {
		t.Fatalf("two homes share the folder %s, want one each", folder)
	}
}

// Only a folder this user owns, that nobody else may enter and that is not a
// link to one, holds sockets.
func TestCheckFolderTakesOnlyThisUsersOwnClosedFolder(t *testing.T) {
	for _, test := range []struct {
		name     string
		make     func(t *testing.T, folder string)
		isTaken  bool
		isAbsent bool
	}{
		{name: "this user's folder with mode 0700", make: func(t *testing.T, folder string) { mkdir(t, folder, 0o700) }, isTaken: true},
		{name: "a folder others may enter", make: func(t *testing.T, folder string) { mkdir(t, folder, 0o755) }},
		{name: "a link to this user's closed folder", make: func(t *testing.T, folder string) {
			target := folder + "-target"
			mkdir(t, target, 0o700)
			if err := os.Symlink(target, folder); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a file", make: func(t *testing.T, folder string) {
			if err := os.WriteFile(folder, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "nothing", make: func(*testing.T, string) {}, isAbsent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			folder := filepath.Join(t.TempDir(), "sockets")
			test.make(t, folder)

			// Act
			err := CheckFolder(folder)

			// Assert
			if isTaken := err == nil; isTaken != test.isTaken {
				t.Fatalf("CheckFolder = %v, want it taken: %t", err, test.isTaken)
			}
			if test.isAbsent && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("CheckFolder of nothing = %v, want a not-exist error", err)
			}
		})
	}
}

// mkdir makes folder with mode, which the process's umask does not narrow.
func mkdir(t *testing.T, folder string, mode os.FileMode) {
	t.Helper()
	if err := os.Mkdir(folder, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, mode); err != nil {
		t.Fatal(err)
	}
}
