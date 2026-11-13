package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/installbase"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// The first-run page records the projects folder as this machine's setting,
// except on an example board: a test fixture or a gate driving one must never
// change the folder the machine's real CEO and goblins use.
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

func TestTheBoardKeepsStartAtLoginOffTheRegistryWithAStandInUserEnvironment(t *testing.T) {
	h := testHome(t)
	t.Setenv(installbase.UserEnvFileVariable, filepath.Join(h.Root, "user-environment.json"))
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.Root, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Root, "bin", "goblins-window.exe"), []byte("stand-in window"), 0o600); err != nil {
		t.Fatal(err)
	}
	login := boardStartAtLogin(h)
	if login.StartAtLoginKey != "" {
		t.Fatal("a scratch board was configured to write Start at login entries")
	}

	err := login.SetStartsAtLogin(true)

	if err == nil || !strings.Contains(err.Error(), "this machine keeps no Start at login entries") {
		t.Fatalf("Start at login = %v, want a refusal without registry entries", err)
	}
	if choice, err := installbase.ReadStartAtLogin(h.State); err != nil || choice != installbase.StartAtLoginOn {
		t.Fatalf("saved choice = %q, %v, want the choice retained in the scratch home", choice, err)
	}
}

// The board's supervisor reads the machine's heavy line for a stall: with the
// turn free, a run waiting for its floor and memory well above it, the
// Dispatch serve hands the supervisor names the free turn and the run at the
// head of the line, so its wake can fire (#2377).
func TestServeDispatchReadsAHeavyLineStandingStill(t *testing.T) {
	// Arrange
	lineWithAWaitingRun(t)
	dispatch := serveDispatch(testHome(t))
	if dispatch.LineStall == nil {
		t.Fatal("serve's Dispatch reads no line stall")
	}

	// Act
	stall, err := dispatch.LineStall(supervisor.Memory{Available: 16 << 30, CommitAvailable: 16 << 30, Total: 32 << 30})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"turns free and 1 in line", "a run that waits first"} {
		if !strings.Contains(stall, want) {
			t.Errorf("serve's LineStall = %q; want %q", stall, want)
		}
	}
}
