package pipeline

import (
	"path/filepath"
	"testing"
)

// On Windows a backslash is a separator, so a finding that writes one names
// its file with forward slashes, as the branch's own paths are written.
func TestNamedTextWritesABackslashAsAForwardSlashOnWindows(t *testing.T) {
	// Arrange
	run := Handback{Log: filepath.Join(t.TempDir(), "missing.log"), Findings: []HandbackFinding{{Description: `go vet: internal\c\c.go:3: unreachable code`}}}

	// Act
	text, err := run.namedText()

	// Assert
	if want := "go vet: internal/c/c.go:3: unreachable code\n"; err != nil || text != want {
		t.Fatalf("namedText = %q, %v; want %q", text, err, want)
	}
}
