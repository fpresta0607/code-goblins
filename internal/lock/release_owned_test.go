package lock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedReleaseRefusesChangedLeaseIdentity(t *testing.T) {
	dir, name := t.TempDir(), "controlled-lease"
	owner, err := AcquireExclusiveNamedStrict(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	defer ReleaseExclusiveNamed(dir, name)
	changed := *owner
	changed.Acquired = changed.Acquired.Add(time.Second)
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, changed); err == nil {
		t.Fatal("released a different lease")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, *owner); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedReleaseRefusesReplacedRecord(t *testing.T) {
	dir, name := t.TempDir(), "controlled-lease"
	owner, err := AcquireExclusiveNamedStrict(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changed := *owner
	changed.Acquired = changed.Acquired.Add(time.Second)
	if err := writeInfo(path, &changed); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, *owner); err == nil {
		t.Fatal("released a replaced record")
	}
	current, err := ReadNamedStrict(dir, name)
	if err != nil || *current != changed {
		t.Fatalf("changed foreign record: %+v, %v", current, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := writeInfo(path, owner); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, *owner); err != nil {
		t.Fatal(err)
	}
}

func TestOwnedReleaseDeadlineCoversRegistryContention(t *testing.T) {
	dir, name := t.TempDir(), "controlled-lease"
	owner, err := AcquireExclusiveNamedStrict(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	defer ReleaseExclusiveNamed(dir, name)
	exclusiveLeases.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = ReleaseExclusiveOwned(ctx, dir, name, *owner)
	exclusiveLeases.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("registry ignored deadline: %v", err)
	}
	if err := ReleaseExclusiveOwned(context.Background(), dir, name, *owner); err != nil {
		t.Fatal(err)
	}
}

func TestContextAcquisitionKeepsLiveOrUncertainCustody(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		dir, name := t.TempDir(), "controlled-lease"
		self, _ := ownerInfo(os.Getpid(), exclusiveSpawnSession)
		if uncertain {
			self.Hostname = "another-machine"
		}
		if err := writeInfo(filepath.Join(dir, name), self); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := AcquireExclusiveNamedStrictContext(ctx, dir, name)
		cancel()
		if !errors.Is(err, ErrHeld) {
			t.Fatalf("live or uncertain custody was reclaimed: %v", err)
		}
		current, err := ReadNamedStrict(dir, name)
		if err != nil || *current != *self {
			t.Fatalf("custody changed: %+v, %v", current, err)
		}
	}
}

func TestContextAcquisitionDeadlineCoversRegistryContention(t *testing.T) {
	dir := t.TempDir()
	exclusiveLeases.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := AcquireExclusiveNamedStrictContext(ctx, dir, "controlled-lease")
	exclusiveLeases.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("registry ignored deadline: %v", err)
	}
}
