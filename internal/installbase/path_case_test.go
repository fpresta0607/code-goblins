package installbase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two spellings of a directory that differ only in case are one directory
// exactly where the file system opens the one by the other, a backslash
// separates folders only where the system takes it as a separator, and a
// trailing separator or quotes mean nothing.
func TestSameDirectoryComparesByTheFileSystemsRule(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	lower := filepath.Join(parent, "home")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "HOME")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	slashed := filepath.ToSlash(lower)
	cases := []struct {
		name         string
		left, right  string
		wantSameDirs bool
	}{
		{name: "another case", left: lower, right: upper, wantSameDirs: isOnePath},
		{name: "backslashes for slashes", left: slashed, right: strings.ReplaceAll(slashed, "/", `\`), wantSameDirs: os.IsPathSeparator('\\')},
		{name: "a trailing separator", left: lower + string(filepath.Separator), right: lower, wantSameDirs: true},
		{name: "quotes", left: `"` + lower + `"`, right: lower, wantSameDirs: true},
		{name: "another folder", left: lower, right: parent, wantSameDirs: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isSame := SameDirectory(tc.left, tc.right)

			// Assert
			if isSame != tc.wantSameDirs {
				t.Fatalf("SameDirectory(%q, %q) = %t, want %t", tc.left, tc.right, isSame, tc.wantSameDirs)
			}
		})
	}
}
