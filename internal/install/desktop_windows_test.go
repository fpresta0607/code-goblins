//go:build windows

package install

import (
	"path/filepath"
	"strings"
	"testing"
)

// The desktop shortcut's folder is the one DesktopVariable names, as a test's
// own, and otherwise the desktop Windows shows.
func TestDesktopShortcutPathIsInTheFolderItsVariableNames(t *testing.T) {
	// Arrange
	folder := t.TempDir()
	t.Setenv(DesktopVariable, folder)

	// Act
	got := DesktopShortcutPath()

	// Assert
	if want := filepath.Join(folder, "Code Goblins.lnk"); got != want {
		t.Errorf("DesktopShortcutPath() = %q, want %q", got, want)
	}
	t.Setenv(DesktopVariable, "")
	if got := DesktopShortcutPath(); got == "" || filepath.Base(got) != "Code Goblins.lnk" || strings.HasPrefix(got, folder) {
		t.Errorf("DesktopShortcutPath() without %s = %q, want Code Goblins.lnk on the user's own desktop", DesktopVariable, got)
	}
}
