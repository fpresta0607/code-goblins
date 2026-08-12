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

func TestAcquireFailsWhileForeignHolderLives(t *testing.T) {
	// A record whose PID and Start match this process (so the local liveness
	// check alone would pass) but whose Hostname names a different host is
	// still live, foreign contention: Acquire must fail with ErrHeld and must
	// not modify the lock file.
	dir := t.TempDir()
	self := selfInfo()
	foreign := &Info{PID: self.PID, Start: self.Start, Hostname: "some-other-host", Acquired: time.Now()}
	lockPath := filepath.Join(dir, ".lock")
	if err := writeInfo(lockPath, foreign); err != nil {
		t.Fatal(err)
	}
	_, err := Acquire(dir)
	if !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want ErrHeld", err)
	}
	after, rerr := Read(dir)
	if rerr != nil {
		t.Fatalf("Read after failed Acquire: %v", rerr)
	}
	if after.Hostname != "some-other-host" || after.PID != self.PID {
		t.Errorf("lock file modified by failed Acquire: got %+v", after)
	}
}

func TestAcquireSameProcessDoubleAcquireSucceeds(t *testing.T) {
	// A process re-acquiring a lock it already holds gets the idempotent
	// contract: success with its own identity, never ErrHeld.
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("second Acquire from same process: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
}

func TestAcquireStealsFromDeadHolder(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	localHostname, _ := os.Hostname()
	stale := &Info{PID: deadPID, Start: time.Now().Add(-time.Hour), Hostname: localHostname, Acquired: time.Now().Add(-time.Hour)}
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

func TestRecycledPIDRejection(t *testing.T) {
	// A process with the current PID but a creation time from an hour ago
	// must be judged dead (the PID was recycled).
	localHostname, _ := os.Hostname()
	recycled := &Info{PID: os.Getpid(), Start: time.Now().Add(-time.Hour), Hostname: localHostname}
	if recycled.Alive() {
		t.Error("recycled PID with old creation time must be dead")
	}
}

func TestReleaseAuthorizationWithWrongStart(t *testing.T) {
	// Release must fail if the holder's Start time does not match.
	dir := t.TempDir()
	localHostname, _ := os.Hostname()
	wrongStart := &Info{PID: os.Getpid(), Start: time.Now().Add(-time.Hour), Hostname: localHostname, Acquired: time.Now()}
	lockPath := filepath.Join(dir, ".lock")
	if err := writeInfo(lockPath, wrongStart); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir); err == nil {
		t.Error("Release must fail with wrong Start time")
	}
	// Verify the file still exists.
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file removed after failed Release: %v", err)
	}
}

func TestReleaseAuthorizationWithDeadPID(t *testing.T) {
	// Release must fail if the holder is a dead PID.
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	localHostname, _ := os.Hostname()
	dead := &Info{PID: deadPID, Start: time.Now().Add(-time.Hour), Hostname: localHostname, Acquired: time.Now().Add(-time.Hour)}
	lockPath := filepath.Join(dir, ".lock")
	if err := writeInfo(lockPath, dead); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir); err == nil {
		t.Error("Release must fail with dead PID")
	}
	// Verify the file still exists.
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file removed after failed Release: %v", err)
	}
}

func TestReleaseAuthorizationWithForeignHostname(t *testing.T) {
	// Release must fail if the holder's hostname differs.
	dir := t.TempDir()
	foreign := &Info{PID: os.Getpid(), Start: time.Now(), Hostname: "foreign-host", Acquired: time.Now()}
	lockPath := filepath.Join(dir, ".lock")
	if err := writeInfo(lockPath, foreign); err != nil {
		t.Fatal(err)
	}
	if err := Release(dir); err == nil {
		t.Error("Release must fail with foreign hostname")
	}
	// Verify the file still exists.
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file removed after failed Release: %v", err)
	}
}

func TestAcquireIdempotentWhenHolderIsSelf(t *testing.T) {
	// If the lock file already records this process's own identity (e.g. a
	// transient read-back failure after a successful create left the file in
	// place), Acquire must recognize it as already held and succeed, not
	// return ErrHeld against itself.
	dir := t.TempDir()
	self := selfInfo()
	if err := writeInfo(filepath.Join(dir, ".lock"), self); err != nil {
		t.Fatal(err)
	}
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire over self-held lock: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if !info.Start.Equal(self.Start) {
		t.Errorf("Start = %v, want %v", info.Start, self.Start)
	}
}

func TestZeroByteOrphanRecovery(t *testing.T) {
	// A zero-byte lock file (holder crashed mid-write) must be recovered
	// by Acquire after the grace period elapses.
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	// Write a zero-byte file to simulate a crashed holder.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Acquire should succeed after handling the orphan.
	info, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire failed on zero-byte orphan: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	// Verify the lock file now contains valid data.
	read, err := Read(dir)
	if err != nil {
		t.Fatalf("Read after recovery: %v", err)
	}
	if read.PID != os.Getpid() {
		t.Errorf("recovered lock has PID %d, want %d", read.PID, os.Getpid())
	}
}
