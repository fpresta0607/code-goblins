package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.Start.IsZero() {
		t.Error("Start is zero, want the process creation time")
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); err != nil {
		t.Errorf("lock file missing: %v", err)
	}
}

func TestSecondAcquireFailsWhileHolderLives(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	_, err := Acquire(dir)
	if !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want ErrHeld", err)
	}
}

func TestAcquireStealsFromDeadHolder(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	stale := &Info{PID: deadPID, Start: time.Now().Add(-time.Hour), Hostname: "host", Acquired: time.Now().Add(-time.Hour)}
	if err := writeInfo(filepath.Join(dir, ".lock"), stale); err != nil {
		t.Fatal(err)
	}
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire over dead holder: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want current process %d", info.PID, os.Getpid())
	}
}

func TestReleaseThenReacquire(t *testing.T) {
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := Acquire(dir); err != nil {
		t.Fatalf("re-Acquire: %v", err)
	}
}

func TestAliveForSelfAndDead(t *testing.T) {
	self := selfInfo()
	if !self.Alive() {
		t.Error("current process must be alive")
	}
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := &Info{PID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour)}
	if dead.Alive() {
		t.Error("exited process must not be alive (pid gone or start mismatch)")
	}
}
