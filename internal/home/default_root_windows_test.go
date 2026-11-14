package home

import (
	"path/filepath"
	"testing"
)

// pointDefaultAt points the per-user folder DefaultRoot reads at folder for
// the test, and returns the home DefaultRoot then names.
func pointDefaultAt(t *testing.T, folder string) string {
	t.Helper()
	t.Setenv("LOCALAPPDATA", folder)
	return filepath.Join(folder, "CodeGoblins")
}

// clearDefault leaves DefaultRoot no per-user folder to read for the test.
func clearDefault(t *testing.T) {
	t.Helper()
	t.Setenv("LOCALAPPDATA", "")
}
