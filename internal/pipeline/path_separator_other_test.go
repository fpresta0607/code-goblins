//go:build !windows

package pipeline

import (
	"path/filepath"
	"testing"
)

// Outside Windows a backslash is a character of a name and no separator, so
// a finding that writes one keeps it, and names no file under a folder.
func TestNamedTextKeepsABackslashOutsideWindows(t *testing.T) {
	// Arrange
	run := Handback{Log: filepath.Join(t.TempDir(), "missing.log"), Findings: []HandbackFinding{{Description: `go vet: internal\c\c.go:3: unreachable code`}}}

	// Act
	text, err := run.namedText()

	// Assert
	if want := "go vet: internal\\c\\c.go:3: unreachable code\n"; err != nil || text != want {
		t.Fatalf("namedText = %q, %v; want %q", text, err, want)
	}
}
