package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	self, _ := ownerInfo(os.Getpid(), "")
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
	self, _ := ownerInfo(os.Getpid(), "")
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
	self, _ := ownerInfo(os.Getpid(), "")
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

func localHostnameForTest(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestAcquireOwnerRecordsForeignPID(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	info, err := AcquireOwner(dir, cmd.Process.Pid, "sess-1")
	if err != nil {
		t.Fatalf("AcquireOwner: %v", err)
	}
	if info.PID != cmd.Process.Pid || info.OwnerPID != cmd.Process.Pid || info.Session != "sess-1" {
		t.Errorf("recorded identity wrong: %+v", info)
	}
	if !HeldBy(dir, cmd.Process.Pid) {
		t.Error("HeldBy(owner) = false for live owner")
	}
	if HeldBy(dir, os.Getpid()) {
		t.Error("HeldBy(non-owner) = true")
	}
}

func TestAcquireOwnerIdempotentAcrossSessions(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if _, err := AcquireOwner(dir, cmd.Process.Pid, "sess-1"); err != nil {
		t.Fatal(err)
	}
	info, err := AcquireOwner(dir, cmd.Process.Pid, "sess-2")
	if err != nil {
		t.Fatalf("same-owner reacquire with new session: %v", err)
	}
	if info.PID != cmd.Process.Pid {
		t.Errorf("identity changed: %+v", info)
	}
}

func TestAcquireOwnerContendedByLiveForeignOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	if _, err := AcquireOwner(dir, cmd.Process.Pid, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(dir, os.Getpid(), "other"); !errors.Is(err, ErrHeld) {
		t.Errorf("err = %v, want ErrHeld while foreign owner lives", err)
	}
	after, rerr := Read(dir)
	if rerr != nil {
		t.Fatalf("Read after failed AcquireOwner: %v", rerr)
	}
	if after.PID != cmd.Process.Pid || after.Hostname != localHostnameForTest(t) {
		t.Errorf("lock file modified by failed AcquireOwner: got %+v", after)
	}
}

func TestAcquireOwnerStealsFromDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	stale := &Info{PID: cmd.ProcessState.Pid(), OwnerPID: cmd.ProcessState.Pid(), Start: time.Now().Add(-time.Hour), Hostname: localHostnameForTest(t), Session: "dead"}
	if err := writeInfo(filepath.Join(dir, ".lock"), stale); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireOwner(dir, os.Getpid(), "new"); err != nil {
		t.Fatalf("steal from dead owner: %v", err)
	}
}

func TestHeldByRejectsForeignHostname(t *testing.T) {
	// A record naming this process's own PID and live Start (so the liveness
	// conjunct alone would pass) but a foreign Hostname must not read as held.
	dir := t.TempDir()
	start, _ := processStart(os.Getpid())
	foreign := &Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Start: start, Hostname: "some-other-host", Acquired: time.Now()}
	if err := writeInfo(filepath.Join(dir, ".lock"), foreign); err != nil {
		t.Fatal(err)
	}
	if HeldBy(dir, os.Getpid()) {
		t.Error("HeldBy = true for a foreign hostname")
	}
}

func TestHeldByRejectsDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	dead := &Info{PID: deadPID, OwnerPID: deadPID, Start: time.Now().Add(-time.Hour), Hostname: localHostnameForTest(t), Acquired: time.Now().Add(-time.Hour)}
	if err := writeInfo(filepath.Join(dir, ".lock"), dead); err != nil {
		t.Fatal(err)
	}
	if HeldBy(dir, deadPID) {
		t.Error("HeldBy = true for a dead owner")
	}
}

func TestAcquireOwnerRefusesDeadOwner(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("cmd", "/c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	deadPID := cmd.ProcessState.Pid()
	if _, err := AcquireOwner(dir, deadPID, "s"); !errors.Is(err, ErrOwnerDead) {
		t.Errorf("err = %v, want ErrOwnerDead", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("lock file created for a dead owner: stat err = %v", err)
	}
}

func TestAcquireNamedOwnerDistinctFiles(t *testing.T) {
	// A named lock and the plain .lock must coexist in the same directory as
	// two distinct files. Asserting HeldByNamed alone would prove nothing: an
	// implementation that ignores name and always keys on .lock would satisfy
	// both predicates too, since the shipped acquire loop is idempotent for
	// the same pid. The discriminating assertion is on disk.
	dir := t.TempDir()
	if _, err := Acquire(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), "watch"); err != nil {
		t.Fatalf("AcquireNamedOwner: %v", err)
	}
	if !HeldByNamed(dir, ".watch.lock", os.Getpid()) {
		t.Error("HeldByNamed(.watch.lock) = false")
	}
	if !HeldByNamed(dir, ".lock", os.Getpid()) {
		t.Error("HeldByNamed(.lock) = false")
	}
	if _, err := os.Stat(filepath.Join(dir, ".watch.lock")); err != nil {
		t.Errorf(".watch.lock missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".lock")); err != nil {
		t.Errorf(".lock missing: %v", err)
	}
}

func TestReleaseNamed(t *testing.T) {
	dir := t.TempDir()
	if _, err := AcquireNamedOwner(dir, ".watch.lock", os.Getpid(), ""); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseNamed(dir, ".watch.lock"); err != nil {
		t.Fatalf("ReleaseNamed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".watch.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stat err = %v, want ErrNotExist", err)
	}
}

func TestAcquireExclusiveNamedContendsButRetainsItsOwnVerifiedRecord(t *testing.T) {
	dir := t.TempDir()
	name := ".spawn-task.lock"
	if _, err := AcquireExclusiveNamed(dir, name); err != nil {
		t.Fatalf("AcquireExclusiveNamed: %v", err)
	}
	if _, err := AcquireExclusiveNamed(dir, name); !errors.Is(err, ErrHeld) {
		t.Fatalf("second AcquireExclusiveNamed error = %v, want ErrHeld", err)
	}
	if err := ReleaseNamed(dir, name); err != nil {
		t.Fatalf("ReleaseNamed: %v", err)
	}

	self, status := ownerInfo(os.Getpid(), "")
	if status == statusDead {
		t.Fatal("current process unexpectedly dead")
	}
	if err := writeInfo(filepath.Join(dir, name), self); err != nil {
		t.Fatal(err)
	}
	if _, err := acquire(dir, name, self, false); err != nil {
		t.Fatalf("exclusive acquire did not retain its own verified record: %v", err)
	}
}

func TestReleaseExclusiveNamedRetriesTransientRemovalThenAllowsRetry(t *testing.T) {
	dir := t.TempDir()
	name := ".spawn-task.lock"
	if _, err := AcquireExclusiveNamed(dir, name); err != nil {
		t.Fatalf("AcquireExclusiveNamed: %v", err)
	}

	attempts := 0
	sleeps := 0
	err := releaseExclusiveNamed(dir, name, func(path string) error {
		attempts++
		if attempts < 3 {
			return errors.New("sharing violation")
		}
		return os.Remove(path)
	}, func(time.Duration) {
		sleeps++
	})
	if err != nil {
		t.Fatalf("releaseNamed: %v", err)
	}
	if attempts != 3 || sleeps != 2 {
		t.Fatalf("release attempts=%d sleeps=%d, want 3 and 2", attempts, sleeps)
	}
	if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lock stat error = %v, want ErrNotExist", err)
	}
	if _, err := AcquireExclusiveNamed(dir, name); err != nil {
		t.Fatalf("AcquireExclusiveNamed after retrying release: %v", err)
	}
	if err := ReleaseNamed(dir, name); err != nil {
		t.Fatalf("ReleaseNamed cleanup: %v", err)
	}
}

func TestAcquireExclusiveNamedReclaimsAbandonedLeaseAfterReleaseFailure(t *testing.T) {
	dir := t.TempDir()
	name := ".spawn-task.lock"
	first, err := AcquireExclusiveNamed(dir, name)
	if err != nil {
		t.Fatalf("first AcquireExclusiveNamed: %v", err)
	}

	attempts := 0
	err = releaseExclusiveNamed(dir, name, func(string) error {
		attempts++
		return errors.New("sharing violation")
	}, func(time.Duration) {})
	if err == nil || attempts != 10 {
		t.Fatalf("releaseExclusiveNamed error=%v attempts=%d, want terminal failure after 10 attempts", err, attempts)
	}
	holder, err := ReadNamed(dir, name)
	if err != nil {
		t.Fatalf("ReadNamed abandoned lock: %v", err)
	}
	if holder.Acquired != first.Acquired {
		t.Fatalf("abandoned holder = %+v, want first lease %+v", holder, first)
	}

	second, err := AcquireExclusiveNamed(dir, name)
	if err != nil {
		t.Fatalf("second AcquireExclusiveNamed did not reclaim abandoned lease: %v", err)
	}
	if second.Acquired.Equal(first.Acquired) {
		t.Fatalf("reclaimed lease retained first acquisition time: first=%s second=%s", first.Acquired, second.Acquired)
	}
	if _, err := AcquireExclusiveNamed(dir, name); !errors.Is(err, ErrHeld) {
		t.Fatalf("concurrent same-process acquire after recovery error = %v, want ErrHeld", err)
	}
	if err := ReleaseExclusiveNamed(dir, name); err != nil {
		t.Fatalf("ReleaseExclusiveNamed cleanup: %v", err)
	}
}

func TestAcquireExclusiveNamedReclaimsOnlyAbandonedExclusiveLease(t *testing.T) {
	t.Run("unmarked self lock", func(t *testing.T) {
		dir := t.TempDir()
		name := ".spawn-task.lock"
		self, status := ownerInfo(os.Getpid(), "")
		if status == statusDead {
			t.Fatal("current process unexpectedly dead")
		}
		if err := writeInfo(filepath.Join(dir, name), self); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireExclusiveNamed(dir, name); !errors.Is(err, ErrHeld) {
			t.Fatalf("AcquireExclusiveNamed over unmarked self lock error = %v, want ErrHeld", err)
		}
	})

	t.Run("live foreign process", func(t *testing.T) {
		dir := t.TempDir()
		name := ".spawn-task.lock"
		cmd := exec.Command("cmd", "/c", "ping -n 5 127.0.0.1 >NUL")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
		foreign, status := ownerInfo(cmd.Process.Pid, exclusiveSpawnSession)
		if status == statusDead {
			t.Fatal("child process unexpectedly dead")
		}
		if err := writeInfo(filepath.Join(dir, name), foreign); err != nil {
			t.Fatal(err)
		}
		if _, err := AcquireExclusiveNamed(dir, name); !errors.Is(err, ErrHeld) {
			t.Fatalf("AcquireExclusiveNamed over foreign live lock error = %v, want ErrHeld", err)
		}
	})
}

func TestExclusiveLeaseKeyCanonicalizesRelativePaths(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	name := ".spawn-task.lock"
	if relative, absolute := exclusiveLeaseKey("state", name), exclusiveLeaseKey(filepath.Join(cwd, "state"), name); relative != absolute {
		t.Fatalf("exclusiveLeaseKey relative=%q absolute=%q, want one canonical key", relative, absolute)
	}
	dir := t.TempDir()
	want := exclusiveLeaseKey(dir, name)
	for _, variant := range []string{
		strings.ToUpper(filepath.Clean(dir)),
		filepath.ToSlash(dir),
		`\\?\` + filepath.Clean(dir),
	} {
		if got := exclusiveLeaseKey(variant, name); got != want {
			t.Fatalf("exclusiveLeaseKey(%q)=%q, want %q", variant, got, want)
		}
	}
}

func TestAcquireExclusiveNamedContendsAcrossWindowsCaseVariant(t *testing.T) {
	dir := t.TempDir()
	name := ".spawn-task.lock"
	first, err := AcquireExclusiveNamed(dir, name)
	if err != nil {
		t.Fatalf("first AcquireExclusiveNamed: %v", err)
	}
	caseVariant := strings.ToUpper(filepath.Clean(dir))
	if _, err := AcquireExclusiveNamed(caseVariant, name); !errors.Is(err, ErrHeld) {
		t.Fatalf("case-variant AcquireExclusiveNamed error = %v, want ErrHeld", err)
	}
	holder, err := ReadNamed(dir, name)
	if err != nil {
		t.Fatalf("ReadNamed after case-variant attempt: %v", err)
	}
	if !holder.Acquired.Equal(first.Acquired) {
		t.Fatalf("case-variant acquire replaced active lease: first=%s holder=%s", first.Acquired, holder.Acquired)
	}
	if err := ReleaseExclusiveNamed(dir, name); err != nil {
		t.Fatalf("ReleaseExclusiveNamed cleanup: %v", err)
	}
}
