//go:build windows

package uninstall

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The folder the uninstall moved its own program to goes once nothing holds
// it, by the cmd.exe it leaves behind, which reads the paths as it is given
// them, spaces and all.
func TestRemoveOnceEndedRemovesTheFolderOnceNothingHoldsIt(t *testing.T) {
	// Arrange
	folder := filepath.Join(t.TempDir(), AsidePrefix+"a folder with spaces")
	write(t, filepath.Join(folder, "goblins.exe"), "x")

	// Act
	err := RemoveOnceEnded(folder)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := os.Stat(folder); os.IsNotExist(err) {
			return
		}
	}
	t.Errorf("%s is still there 20 seconds on", folder)
}
