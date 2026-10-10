package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// installing makes an update of the fixture's home one that installs now: its
// lock is held by this test's process, which runs.
func (f *launcherFixture) installing() {
	f.t.Helper()
	if err := os.MkdirAll(update.Dir(f.home.State), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if _, err := lock.AcquireExclusiveNamed(update.Dir(f.home.State), ".lock"); err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(update.Dir(f.home.State), ".lock") })
}

// whileInstalling is the one sentence a lifecycle command is refused with
// while an update installs.
const whileInstalling = "Code Goblins is installing an update, so this did not run and can be tried again in a minute"

// A lifecycle command started while an update installs is refused with one
// plain sentence before it changes anything: the update swaps the programs
// such a command starts a terminal from and restarts the supervisor under it.
// With no update installing, none is refused for one.
func TestLifecycleCommandsAreRefusedWhileAnUpdateInstalls(t *testing.T) {
	for _, command := range [][]string{
		{"spawn", "fix-thing", "--project", "code-goblins", "--brief", `C:\briefs\fix-thing.md`},
		{"switch", "fix-thing", "--harness", "codex"},
		{"pause", "fix-thing"},
		{"resume", "fix-thing"},
		{"kill", "fix-thing"},
		{"cleanup", "fix-thing"},
		{"helper", "start", "fix-thing", "--brief", `C:\briefs\helper.md`},
		{"helper", "merge", "fix-thing"},
	} {
		name := command[0]
		if name == "helper" {
			name += " " + command[1]
		}
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) { return nil, errors.New("no supervisor in this test") })
			f.installing()

			// Act
			exit, stdout, stderr := f.command(command...)

			// Assert
			if exit != 1 || !strings.Contains(stderr, "cfo "+name+": "+whileInstalling) {
				t.Fatalf("%s exited %d while an update installs, want 1 and the refusal:\nstdout: %s\nstderr: %s", name, exit, stdout, stderr)
			}
			if strings.ContainsAny(stderr, ";\u2014") {
				t.Errorf("the refusal holds a semicolon or an em dash:\n%s", stderr)
			}
		})
	}
	t.Run("no update installing", func(t *testing.T) {
		// Arrange
		f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) { return nil, errors.New("no supervisor in this test") })

		// Act
		_, _, stderr := f.command("pause", "fix-thing")

		// Assert
		if strings.Contains(stderr, "installing an update") {
			t.Fatalf("a pause with no update installing was refused for one:\n%s", stderr)
		}
	})
}

// A goblins opened while an update installs starts no supervisor of its own,
// which on 2026-10-09 took the watcher lock from the update twice: it waits
// for the board the update brings back and opens that.
func TestGoblinsOpenedWhileAnUpdateInstallsWaitsForItsBoard(t *testing.T) {
	// Arrange
	f := newLauncherFixture(t, func(home.Home) (<-chan struct{}, error) {
		t.Error("goblins started a supervisor while an update installs")
		return nil, errors.New("refused")
	})
	f.installing()
	board := stoppableBoard(t, f.home.State, fakeBoardPID)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = writeBoardRecord(f.home.State, boardRecord{PID: fakeBoardPID, URL: board})
	}()

	// Act
	exit, stdout, stderr := f.command("--board")

	// Assert
	if exit != 0 || f.starts != 0 {
		t.Fatalf("goblins --board exited %d having started %d supervisors, want 0 and none:\nstdout: %s\nstderr: %s", exit, f.starts, stdout, stderr)
	}
	if len(f.opened) != 1 {
		t.Fatalf("goblins --board opened %v, want the board the update brought back", f.opened)
	}
}
