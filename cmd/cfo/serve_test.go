package main

import (
	"os"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// The first-run page records the projects folder as this machine's setting,
// except on an example board: a test fixture or a gate driving one must never
// change the folder the machine's real CFO and goblins use.
func TestFirstRunRecordsTheProjectsFolderOnThisMachineUnlessTheBoardIsAnExample(t *testing.T) {
	for _, c := range []struct {
		name        string
		isExample   bool
		wantRecords int
	}{
		{"a real board", false, 1},
		{"an example board", true, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			t.Setenv(install.ProjectsRootVariable, `C:\before`)
			var machine []string
			run := firstRunOn(home.Home{State: t.TempDir()}, t.TempDir(), c.isExample, func(root string) error {
				machine = append(machine, root)
				return nil
			})

			// Act
			err := run.SetProjectsRoot(`C:\projects`)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(machine) != c.wantRecords {
				t.Fatalf("machine settings written = %q, want %d", machine, c.wantRecords)
			}
			if got := os.Getenv(install.ProjectsRootVariable); got != `C:\projects` {
				t.Fatalf("the board's own projects folder = %q, want the one picked", got)
			}
		})
	}
}
