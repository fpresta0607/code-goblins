package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// caseSpellings makes a folder named in lowercase and returns its path and
// the same path with the folder's name in uppercase, and whether the file
// system opens the folder by that second spelling: the oracle every case
// test here holds the rule to, read from the file system itself.
func caseSpellings(t *testing.T) (string, string, bool) {
	t.Helper()
	parent := t.TempDir()
	lower := filepath.Join(parent, "target")
	if err := os.Mkdir(lower, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "TARGET")
	_, err := os.Stat(upper)
	return lower, upper, err == nil
}

// Two spellings that differ only in case are one path exactly where the file
// system opens one through the other: on Windows and a Mac's default volume,
// not on Linux.
func TestPathsThatDifferOnlyInCaseAreOnePathWhereTheFileSystemSaysSo(t *testing.T) {
	// Arrange
	lower, upper, isOnePath := caseSpellings(t)
	file := filepath.Join(lower, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	isEqual := EqualPath(lower, upper)
	isPrefix := HasPathPrefix(file, upper)
	isSameKey := PathKey(lower) == PathKey(upper)
	isSame := SamePath(lower, upper)

	// Assert
	if isEqual != isOnePath || isPrefix != isOnePath || isSameKey != isOnePath {
		t.Fatalf("EqualPath = %t, HasPathPrefix = %t, one PathKey = %t, want each %t, as the file system opens %s by %s: %t", isEqual, isPrefix, isSameKey, isOnePath, lower, upper, isOnePath)
	}
	if isSame != isOnePath {
		t.Fatalf("SamePath(%s, %s) = %t, want %t", lower, upper, isSame, isOnePath)
	}
}

// Spellings that are the same are one path everywhere, and different names
// are never one.
func TestEqualPathAndHasPathPrefixKeepDifferentNamesApart(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	folder := filepath.Join(parent, "target")
	cases := []struct {
		name      string
		left      string
		right     string
		wantEqual bool
	}{
		{name: "the same spelling", left: folder, right: folder, wantEqual: true},
		{name: "another name", left: folder, right: filepath.Join(parent, "other"), wantEqual: false},
		{name: "a longer name", left: folder, right: folder + "s", wantEqual: false},
		{name: "empty", left: "", right: "", wantEqual: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			isEqual := EqualPath(tc.left, tc.right)

			// Assert
			if isEqual != tc.wantEqual {
				t.Fatalf("EqualPath(%q, %q) = %t, want %t", tc.left, tc.right, isEqual, tc.wantEqual)
			}
		})
	}
	if !HasPathPrefix(filepath.Join(folder, "file"), folder) || HasPathPrefix(folder, filepath.Join(folder, "file")) || !HasPathPrefix(folder, "") {
		t.Fatal("HasPathPrefix does not read a path's prefix")
	}
}

// A key of a path that differs only in case from another is the other's key
// exactly where they are one path, and a key keeps the path's own spelling
// where the file system keeps case apart.
func TestPathKeyKeepsTheSpellingWhereCaseIsKept(t *testing.T) {
	// Arrange
	_, upper, isOnePath := caseSpellings(t)

	// Act
	key := PathKey(upper)

	// Assert
	if want := upper; !isOnePath && key != want {
		t.Fatalf("PathKey(%s) = %s, want it as written where case is kept", upper, key)
	}
	if want := strings.ToLower(upper); isOnePath && key != want {
		t.Fatalf("PathKey(%s) = %s, want %s where case is ignored", upper, key, want)
	}
}
