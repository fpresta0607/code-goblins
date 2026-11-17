package verify

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// A run whose giving back of the line's lock failed, and which then makes no
// further change of custody, as a run waiting behind others in line makes
// none, gives the lock back by itself, so another process joins the line and
// takes the free turn. On 2026-11-11 one such waiter kept the lock for over
// 40 minutes and no run took a free turn (#2377).
func TestALineLockThatCouldNotBeGivenBackIsGivenBackWithoutAFurtherChange(t *testing.T) {
	// Arrange
	a := admission(t)
	var isFailed atomic.Bool
	releaseCustodyLock = func(ctx context.Context, dir, name string, owner lock.Info) error {
		if isFailed.CompareAndSwap(false, true) {
			return errors.New("the disk did not answer in time")
		}
		return lock.ReleaseExclusiveOwned(ctx, dir, name, owner)
	}
	t.Cleanup(func() {
		custodyMutations.Lock()
		defer custodyMutations.Unlock()
		releaseCustodyLock = lock.ReleaseExclusiveOwned
	})
	unlock, err := custodyMutation(a.Dir)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	releaseError := unlock()

	// Assert
	if releaseError == nil {
		t.Fatal("the first giving back succeeded; want it to fail, as the incident's did")
	}
	deadline := time.Now().Add(time.Minute)
	for {
		_, err := lock.ReadNamedStrict(a.Dir, custodyMutationName)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the line's lock is still held a minute after its giving back failed (read: %v); want this process to give it back by itself", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	other := exec.Command(os.Args[0])
	other.Env = append(os.Environ(), "VERIFY_TEST_HOLD_DIR="+a.Dir)
	stdin, err := other.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := other.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		if err := other.Wait(); err != nil {
			t.Error(err)
		}
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || strings.TrimSpace(line) != "holding" {
		t.Errorf("another process said %q, %v; want it to take the free turn", line, err)
	}
}

// StuckLock names the line's lock only while a live owner has held it
// StuckLockAfter or longer: a lock held for a moment, a dead owner's lock,
// which the next change reclaims, and no lock name nothing.
func TestStuckLockNamesOnlyALiveOwnerHoldingTheLinesLockPastItsBound(t *testing.T) {
	for _, tc := range []struct {
		name        string
		isHeld      bool
		heldFor     time.Duration
		isDead      bool
		isNamedWant bool
	}{
		{name: "a live owner past the bound", isHeld: true, heldFor: StuckLockAfter + time.Second, isNamedWant: true},
		{name: "a live owner for a moment", isHeld: true, heldFor: time.Second},
		{name: "a dead owner past the bound", isHeld: true, heldFor: time.Hour, isDead: true},
		{name: "no lock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			dir := t.TempDir()
			var owner *lock.Info
			if tc.isHeld {
				owner = recordLineLock(t, dir, tc.isDead)
			}
			now := time.Now()
			if owner != nil {
				now = owner.Acquired.Add(tc.heldFor)
			}

			// Act
			stuck, err := StuckLock(dir, now)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if isNamed := stuck != nil; isNamed != tc.isNamedWant {
				t.Fatalf("StuckLock = %+v; want named %v", stuck, tc.isNamedWant)
			}
			if stuck != nil && stuck.PID != os.Getpid() {
				t.Errorf("StuckLock named pid %d; want this process, %d", stuck.PID, os.Getpid())
			}
		})
	}
}

// recordLineLock writes the line's lock in dir as this process's, or, when
// isDead, as a process of this pid that started an hour earlier, which is
// not alive.
func recordLineLock(t *testing.T, dir string, isDead bool) *lock.Info {
	t.Helper()
	other := filepath.Join(dir, "self")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	owner, err := lock.AcquireExclusiveNamedStrict(other, custodyMutationName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(other, custodyMutationName) })
	// A dead owner is an earlier run of this pid: Windows knows a run by
	// its start, Linux and macOS by its instance.
	if isDead {
		owner.Start = owner.Start.Add(-time.Hour)
		if owner.Instance != "" {
			owner.Instance += "0"
		}
	}
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, custodyMutationName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return owner
}
