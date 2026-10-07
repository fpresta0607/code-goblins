package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
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

func TestTheBoardKeepsStartAtLoginOffTheRegistryWithAStandInUserEnvironment(t *testing.T) {
	h := testHome(t)
	t.Setenv(install.UserEnvFileVariable, filepath.Join(h.Root, "user-environment.json"))
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
	if choice, err := install.ReadStartAtLogin(h.State); err != nil || choice != install.StartAtLoginOn {
		t.Fatalf("saved choice = %q, %v, want the choice retained in the scratch home", choice, err)
	}
}

func TestTheFirstRunStartWaitsForAnotherCFOStartAndKeepsItsTerminal(t *testing.T) {
	h := testHome(t)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyFile(t, self, filepath.Join(bin, "claude.exe"))
	t.Setenv("PATH", bin)
	run := firstRunOn(h, t.TempDir(), true, func(string) error { return nil })
	if _, err := lock.AcquireExclusiveNamed(h.State, cfoLaunchLock); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock) })
	finished := make(chan error, 1)

	go func() { finished <- run.StartCFO("claude") }()
	select {
	case err := <-finished:
		if err == nil {
			closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal)
		}
		t.Fatalf("first-run Start returned while another start held the lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := startNativeCFO(h, h.Root, "claude", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeNativeTerminal(t, h.State, supervisor.NativeCFOTerminal) })
	before, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.ReleaseExclusiveNamed(h.State, cfoLaunchLock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first-run Start did not finish after the launch lock was released")
	}

	after, err := host.ReadRecord(h.State, supervisor.NativeCFOTerminal)
	if err != nil || before.HostPID != after.HostPID || before.Token != after.Token {
		t.Fatalf("first-run Start replaced the running terminal: before %+v, after %+v, error %v", before, after, err)
	}
}
