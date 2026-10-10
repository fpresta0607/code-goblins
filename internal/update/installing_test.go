package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// An update installs while a process that runs holds its lock. No lock, a
// lock left by a process that ended, and a record that cannot be read are no
// update: nothing waits on one that is not there.
func TestInstallingIsAProcessThatRunsHoldingTheUpdatesLock(t *testing.T) {
	ended := exec.Command("cmd", "/c", "exit 0")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		// held arranges the lock under the home's state.
		held func(t *testing.T, stateDir string)
		want bool
	}{
		{"no update ever ran", func(*testing.T, string) {}, false},
		{"a process that runs holds the lock", func(t *testing.T, stateDir string) {
			if _, err := lock.AcquireExclusiveNamed(Dir(stateDir), LockName); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(Dir(stateDir), LockName) })
		}, true},
		{"the process that held the lock ended", func(t *testing.T, stateDir string) {
			data, err := json.Marshal(lock.Info{PID: ended.Process.Pid, OwnerPID: ended.Process.Pid, Session: "exclusive-spawn", Start: time.Now().Add(-time.Minute).UTC(), Hostname: hostname, Acquired: time.Now().UTC()})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(Dir(stateDir), LockName), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"a record that cannot be read", func(t *testing.T, stateDir string) {
			if err := os.WriteFile(filepath.Join(Dir(stateDir), LockName), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := filepath.Join(t.TempDir(), "state")
			if err := os.MkdirAll(Dir(stateDir), 0o700); err != nil {
				t.Fatal(err)
			}
			test.held(t, stateDir)

			// Act
			got := Installing(stateDir)

			// Assert
			if got != test.want {
				t.Fatalf("Installing = %v, want %v", got, test.want)
			}
		})
	}
}
