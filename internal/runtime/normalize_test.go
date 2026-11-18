package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// A directory's key drops a trailing separator, and a folder spelled in
// another case shares it exactly where the file system reads the two
// spellings as one folder.
func TestNormalizeKeysAFolderByTheFileSystemsCaseAndSeparator(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	lower := filepath.Join(parent, "worktree")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "WORKTREE")
	_, statErr := os.Stat(upper)
	cases := []struct {
		name        string
		path        string
		wantSameKey bool
	}{
		{name: "the folder as written", path: lower, wantSameKey: true},
		{name: "the folder with a trailing separator", path: lower + string(filepath.Separator), wantSameKey: true},
		{name: "the folder in another case", path: upper, wantSameKey: statErr == nil},
		{name: "a folder whose name it begins", path: lower + "s", wantSameKey: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isSameKey := normalize(tc.path) == normalize(lower)

			// Assert
			if isSameKey != tc.wantSameKey {
				t.Fatalf("normalize(%s) = %s, normalize(%s) = %s; one key: %t, want %t", tc.path, normalize(tc.path), lower, normalize(lower), isSameKey, tc.wantSameKey)
			}
		})
	}
}
