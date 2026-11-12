package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// With the turn free, a run waiting and a live process that has held the
// line's lock past its bound, goblins gate turns no longer reads plainly "turn:
// free": it says no run can take the turn and names the lock, its owner's pid
// and how long it has held it, and its JSON carries the lock (#2377).
func TestGateTurnsNamesTheLinesLockALiveOwnerKeepsPastItsBound(t *testing.T) {
	// Arrange
	slots := lineWithAWaitingRun(t)
	owner := keepLineLockSince(t, slots, time.Now().Add(-41*time.Minute))

	// Act
	stdout, jsonOut := gateTurnsBoth(t)

	// Assert
	for _, want := range []string{
		"turn: free, but no run can take it\n",
		fmt.Sprintf("lock: the line's lock (custody-mutation in the line's store) has been held by pid %d for 41m", owner.PID),
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout %q lacks %q", stdout, want)
		}
	}
	if strings.Contains(stdout, "turn: free\n") {
		t.Errorf("stdout %q still reads plainly turn: free", stdout)
	}
	var report struct {
		StuckLock *struct {
			PID   int       `json:"pid"`
			Since time.Time `json:"since"`
		} `json:"stuck_lock"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("--json %q: %v", jsonOut, err)
	}
	if report.StuckLock == nil || report.StuckLock.PID != owner.PID || !report.StuckLock.Since.Equal(owner.Acquired) {
		t.Errorf("--json stuck_lock = %+v; want pid %d since %s", report.StuckLock, owner.PID, owner.Acquired)
	}
}

// A line whose lock is free, or held for a moment as every change holds it,
// reads as it did, and its JSON carries a null lock.
func TestGateTurnsNamesNoLockHeldForAMoment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		isHeld bool
	}{
		{name: "no lock"},
		{name: "a lock held for a moment", isHeld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			slots := lineWithAWaitingRun(t)
			if tc.isHeld {
				keepLineLockSince(t, slots, time.Now())
			}

			// Act
			stdout, jsonOut := gateTurnsBoth(t)

			// Assert
			if !strings.HasPrefix(stdout, "turn: free\n") || strings.Contains(stdout, "lock:") {
				t.Errorf("stdout %q; want plainly turn: free and no lock", stdout)
			}
			if !strings.Contains(jsonOut, `"stuck_lock":null`) {
				t.Errorf("--json %q; want a null stuck_lock", jsonOut)
			}
		})
	}
}

// lineWithAWaitingRun gives the test a store of its own in which one run
// waits for memory with the turn free, and returns the store's slots.
func lineWithAWaitingRun(t *testing.T) string {
	t.Helper()
	store := t.TempDir()
	t.Setenv("GOBLINS_VERIFY_DIR", store)
	slots := filepath.Join(store, "slots")
	short := func() (uint64, error) { return 1 << 30, nil }
	waitInLine(t, verify.Admission{Dir: slots, Floor: 4 << 30, Available: short, Who: "a run that waits", Budget: time.Hour, Limit: time.Minute, Poll: 10 * time.Millisecond})
	return slots
}

// keepLineLockSince writes the line's lock in slots as this live process's,
// taken at since.
func keepLineLockSince(t *testing.T, slots string, since time.Time) *lock.Info {
	t.Helper()
	self := t.TempDir()
	owner, err := lock.AcquireExclusiveNamedStrict(self, "custody-mutation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(self, "custody-mutation") })
	owner.Acquired = since.UTC()
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(slots, "custody-mutation")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return owner
}

// gateTurnsBoth runs goblins gate turns and goblins gate turns --json on a machine
// with plenty of memory, and returns what each printed.
func gateTurnsBoth(t *testing.T) (stdout, jsonOut string) {
	t.Helper()
	var out, errs bytes.Buffer
	if exit := gateTurns(plenty, &out, &errs); exit != 0 {
		t.Fatalf("goblins gate turns exit=%d stderr=%q", exit, errs.String())
	}
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	var jsonBuffer, jsonErrs bytes.Buffer
	if exit := runWithRuntime([]string{"gate", "turns", "--json"}, &jsonBuffer, &jsonErrs, runtime); exit != 0 {
		t.Fatalf("goblins gate turns --json exit=%d stderr=%q", exit, jsonErrs.String())
	}
	return out.String(), jsonBuffer.String()
}
