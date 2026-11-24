package tickets

import (
	"os"
	"path/filepath"
	"testing"
)

// An error line names a task by its id in another case exactly where the
// state folder's file system opens the task's record by that spelling, so
// only there is the id taken out of the line's class.
func TestErrorClassNamesATaskByTheStateFoldersCase(t *testing.T) {
	// Arrange
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "cg-remote-toggle.meta"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(stateDir, "CG-Remote-Toggle.meta"))
	isOnePath := err == nil
	want := "push progress for CG-Remote-Toggle: git for-each-ref exited N"
	if isOnePath {
		want = "push progress for <task>: git for-each-ref exited N"
	}

	// Act
	class := ErrorClass("push progress for CG-Remote-Toggle: git for-each-ref exited 128", stateDir, []string{"cg-remote-toggle"})

	// Assert
	if class != want {
		t.Fatalf("ErrorClass = %q, want %q, as the file system opens the one spelling by the other: %t", class, want, isOnePath)
	}
}
