package proc_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// The machine's start and its boot ID are no one process's. Where either
// cannot be read, as in a sandbox that hides it, a process could not be
// read, which is never proof that it is gone: the lock takes a holder that
// is gone as dead, and would take the lock of every holder that runs.
func TestAMachineThatCannotBeReadIsNoProofAProcessIsGone(t *testing.T) {
	tests := []struct {
		name string
		hide func(t *testing.T)
	}{
		{name: "its boot ID is missing", hide: func(t *testing.T) {
			proc.ReadBootIDFrom(t, filepath.Join(t.TempDir(), "boot_id"))
		}},
		{name: "its boot ID is empty", hide: func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "boot_id")
			if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			proc.ReadBootIDFrom(t, path)
		}},
		{name: "its start is missing", hide: func(t *testing.T) {
			proc.ReadMachineStartFrom(t, filepath.Join(t.TempDir(), "stat"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			dir, name := t.TempDir(), ".watch.lock"
			holder := exec.Command("sleep", "600")
			if err := holder.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = holder.Process.Kill()
				_ = holder.Wait()
			})
			if _, err := lock.AcquireNamedOwner(dir, name, holder.Process.Pid, "watch"); err != nil {
				t.Fatal(err)
			}
			test.hide(t)

			// Act
			_, instanceErr := proc.Instance(holder.Process.Pid)
			_, lookupErr := proc.Lookup(holder.Process.Pid)
			_, acquireErr := lock.AcquireNamedOwner(dir, name, os.Getpid(), "watch")

			// Assert
			if instanceErr == nil && lookupErr == nil {
				t.Fatal("the holder was read whole, so the test proves nothing")
			}
			if errors.Is(instanceErr, fs.ErrNotExist) || errors.Is(lookupErr, fs.ErrNotExist) {
				t.Errorf("a holder that runs reads as gone: Instance = %v, Lookup = %v", instanceErr, lookupErr)
			}
			if !errors.Is(acquireErr, lock.ErrHeld) {
				t.Errorf("the lock of a holder that runs was asked for and gave %v, want ErrHeld", acquireErr)
			}
			if recorded, err := lock.ReadNamed(dir, name); err != nil || recorded.PID != holder.Process.Pid || !recorded.Alive() {
				t.Errorf("the lock records %+v, %v; want its holder, pid %d, left in it and alive", recorded, err, holder.Process.Pid)
			}
		})
	}
}
