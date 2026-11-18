//go:build !windows

package pathguard

import (
	"path/filepath"
	"strings"
	"testing"
)

// Off Windows a path is read as it is written: /tmp is the system's /tmp,
// not the folder TMP names, and /c/ is a folder named c, not a drive.
func TestToolRefusalReadsAPathAsItIsWrittenOffWindows(t *testing.T) {
	// Arrange
	f := newFixture(t)
	cases := []struct {
		name    string
		command string
		path    string
	}{
		{"a write to /tmp", "echo hi > /tmp/note.txt", "/tmp/note.txt"},
		{"a write to /c/", "echo hi > /c/note.txt", "/c/note.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Act
			refusal := f.boundary.ToolRefusal("Bash", toolInput(t, map[string]string{"command": c.command}))

			// Assert
			if !strings.Contains(refusal, "it writes "+c.path) {
				t.Fatalf("%q gave %q, want it refused as a write to %s, outside what the goblin was given", c.command, refusal, c.path)
			}
		})
	}
	t.Run("a backslash before a name", func(t *testing.T) {
		// Act: \note.txt is a file of that name in the worktree, not a
		// path from a drive's root.
		refusal := f.boundary.ToolRefusal("Write", toolInput(t, map[string]string{"file_path": `\note.txt`}))

		// Assert
		if refusal != "" {
			t.Fatalf("a write to %s in the worktree was refused: %s", filepath.Join(f.worktree, `\note.txt`), refusal)
		}
	})
}
