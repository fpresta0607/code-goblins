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
// them, spaces and all. The uninstall goes on for a while after it starts
// that cmd.exe, holding its program, so the folder goes seconds later too.
func TestRemoveOnceEndedRemovesTheFolderOnceNothingHoldsIt(t *testing.T) {
	cases := []struct {
		name    string
		holdFor time.Duration
	}{
		{name: "nothing holds it", holdFor: 0},
		{name: "the program goes on for seconds", holdFor: 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			folder := filepath.Join(t.TempDir(), AsidePrefix+"a folder with spaces")
			program := filepath.Join(folder, "goblins.exe")
			write(t, program, "x")
			held, err := os.Open(program)
			if err != nil {
				t.Fatal(err)
			}

			// Act
			err = RemoveOnceEnded(folder)
			time.Sleep(tc.holdFor)
			if closeErr := held.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
				if _, err := os.Stat(folder); os.IsNotExist(err) {
					return
				}
			}
			t.Errorf("%s is still there 20 seconds after it was let go", folder)
		})
	}
}
