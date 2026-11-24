package gatehome

import (
	"os"
	"path/filepath"
	"testing"
)

// Two spellings of a folder are one folder exactly where the file system
// opens it by both, as Windows does, and two folders where it keeps case
// apart.
func TestSameFolderComparesByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	lower := filepath.Join(parent, "target")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "TARGET")
	_, statErr := os.Stat(upper)
	cases := []struct {
		name     string
		other    string
		wantSame bool
	}{
		{name: "the same spelling", other: lower, wantSame: true},
		{name: "the same spelling uncleaned", other: lower + string(filepath.Separator) + ".", wantSame: true},
		{name: "another case", other: upper, wantSame: statErr == nil},
		{name: "another folder", other: filepath.Join(parent, "other"), wantSame: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isSame := sameFolder(lower, tc.other)

			// Assert
			if isSame != tc.wantSame {
				t.Fatalf("sameFolder(%s, %s) = %t, want %t", lower, tc.other, isSame, tc.wantSame)
			}
		})
	}
}
