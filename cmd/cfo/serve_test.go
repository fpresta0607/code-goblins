package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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
			}, commandRuntime{})

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

// The CFO the first-run page starts is watched for its startup dialogs, as
// goblins watches a native CFO it starts, without the page waiting on the
// watch; a CFO that does not start is not watched.
func TestTheFirstRunWatchesTheDialogsOfTheCFOItStarts(t *testing.T) {
	for _, c := range []struct {
		name      string
		startErr  error
		isWatched bool
	}{
		{"the CFO starts", nil, true},
		{"the CFO does not start", errors.New("claude is not on PATH"), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			h := home.Home{State: t.TempDir()}
			watched := make(chan string, 1)
			release := make(chan struct{})
			defer close(release)
			runtime := commandRuntime{
				startNativeCFO: func(_ home.Home, _, harness string) error { return c.startErr },
				settleCFO: func(_ context.Context, stateDir, harness string) []string {
					watched <- stateDir + " " + harness
					<-release
					return []string{"a note nobody reads"}
				},
			}
			run := firstRunOn(h, t.TempDir(), true, func(string) error { return nil }, runtime)

			// Act
			err := run.StartCFO(t.TempDir())

			// Assert
			if !errors.Is(err, c.startErr) {
				t.Fatalf("StartCFO error = %v, want %v", err, c.startErr)
			}
			select {
			case got := <-watched:
				if !c.isWatched || got != h.State+" claude" {
					t.Errorf("watched %q, want watched=%v for claude in %s", got, c.isWatched, h.State)
				}
			case <-time.After(time.Second):
				if c.isWatched {
					t.Error("the CFO's startup dialogs were never watched")
				}
			}
		})
	}
}
