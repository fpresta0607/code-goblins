package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A journal whose root, candidate copy, backup or set-aside program names
// this home in another case is this home's exactly where the file system
// opens the home by that spelling.
func TestValidateNamesTheHomeByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	root, upper := filepath.Join(parent, "home"), filepath.Join(parent, "HOME")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	_, statErr := os.Stat(upper)
	isOnePath := statErr == nil
	stateDir := filepath.Join(root, "state")
	other := func(path string) string { return upper + path[len(root):] }
	journal := Journal{
		Root: root,
		Copy: filepath.Join(Dir(stateDir), "candidate.exe"),
		Aliases: []Alias{{
			Name:     Aliases[0],
			Previous: strings.Repeat("0", 64),
			Backup:   filepath.Join(Dir(stateDir), "previous-"+Aliases[0]),
			Aside:    []string{filepath.Join(root, Aliases[0]+".1.update-old")},
		}},
	}
	cases := []struct {
		name string
		edit func(*Journal)
	}{
		{name: "the root", edit: func(j *Journal) { j.Root = other(j.Root) }},
		{name: "the candidate copy", edit: func(j *Journal) { j.Copy = other(j.Copy) }},
		{name: "the backup", edit: func(j *Journal) { j.Aliases[0].Backup = other(j.Aliases[0].Backup) }},
		{name: "a set-aside program", edit: func(j *Journal) { j.Aliases[0].Aside = []string{other(j.Aliases[0].Aside[0])} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edited := journal
			edited.Aliases = []Alias{journal.Aliases[0]}
			tc.edit(&edited)

			// Act
			err := Validate(edited, root, stateDir)

			// Assert
			if isValid := err == nil; isValid != isOnePath {
				t.Fatalf("Validate with %s spelled under %s = %v, want valid %t", tc.name, upper, err, isOnePath)
			}
		})
	}
}
