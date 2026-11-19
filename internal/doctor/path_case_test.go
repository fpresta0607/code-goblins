package doctor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A process runs from its program's folder, spelled in another case exactly
// where the file system opens the folder by that spelling.
func TestRunningFromNamesAProgramsFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Dir(self)
	upper := filepath.Join(filepath.Dir(folder), strings.ToUpper(filepath.Base(folder)))
	if upper == folder {
		t.Skipf("%s has no lowercase letter to spell in another case", folder)
	}
	_, err = os.Stat(upper)
	isOnePath := err == nil
	cases := []struct {
		name        string
		folder      string
		wantRunning bool
	}{
		{name: "its own spelling", folder: folder, wantRunning: true},
		{name: "another case", folder: upper, wantRunning: isOnePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			running, err := RunningFrom(tc.folder)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isRunning := slices.Contains(running, os.Getpid()); isRunning != tc.wantRunning {
				t.Fatalf("this test, run from %s, runs from %s: %t, want %t", self, tc.folder, isRunning, tc.wantRunning)
			}
		})
	}
}
