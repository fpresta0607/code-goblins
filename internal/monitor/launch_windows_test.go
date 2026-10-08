package monitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// On 2026-10-07 pd-discovery-ladders' host started thirteen minutes after its
// cfo spawn wrote its task record, and the monitor woke the CFO with
// endpoint_missing eight minutes in: the launch grace ran from the record. A
// spawn takes the home's spawn lock as its turn before it names the goblin's
// generation and gives it back once the goblin's host runs, and the goblin
// then installs its own dependencies as its first work. So a goblin whose
// spawn holds its turn, or whose switch is relaunching it, is launching, and
// one whose host started within the launch budget is too; one a spawn left
// behind, or another task's turn, is not.
func TestScanKeepsAGoblinQuietWhileItsSpawnOrSwitchRuns(t *testing.T) {
	composer := []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	for _, test := range []struct {
		name     string
		lockName string
		// namedAfterTurn is how long after the lock was taken the goblin's
		// generation was named: after it for the goblin whose spawn holds
		// its turn, before it for another task's turn.
		namedAfterTurn time.Duration
		hasHost        bool
		wasSeen        bool
		shouldWake     bool
	}{
		{"its spawn's turn, before its host starts", ".spawn.lock", time.Millisecond, false, false, false},
		{"its spawn's turn, its host starting", ".spawn.lock", time.Millisecond, true, false, false},
		{"its switch relaunching a goblin seen alive", ".switch-g1.lock", time.Millisecond, false, true, false},
		{"another task's turn", ".spawn.lock", -time.Second, false, false, true},
		{"its host started a minute ago, its spawn over", "", 0, true, false, false},
		{"its spawn gone", "", 0, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			now := time.Now().UTC().Add(time.Minute)
			meta := nativeMeta("g1", "claude")
			named := time.Now()
			if test.lockName != "" {
				named = acquireForTest(t, stateDir, test.lockName).Add(test.namedAfterTurn)
			}
			meta.SpawnGen = fmt.Sprintf("s%d", named.UnixNano())
			writeTask(t, stateDir, meta)
			written := now.Add(-13 * time.Minute)
			if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), written, written); err != nil {
				t.Fatal(err)
			}
			if test.hasHost {
				recordNativeHost(t, stateDir, "g1")
			}
			if test.wasSeen {
				if err := WriteObservation(stateDir, Observation{Schema: Schema, TaskID: "g1", Endpoint: endpointString(meta), EndpointVerdict: ProbePresent, Digest: "seen", LastSeen: now.Add(-time.Hour), LastProgress: now.Add(-time.Hour), LastObserved: now.Add(-time.Minute), Health: HealthBusy, Reason: None}); err != nil {
					t.Fatal(err)
				}
			}
			probe := BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf(composer...)}}

			// Act
			result, err := testService(stateDir, probe, &now).Scan(context.Background())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if (result.Event != nil) != test.shouldWake {
				t.Errorf("event %+v, want a wake %v", result.Event, test.shouldWake)
			}
		})
	}
}

// acquireForTest holds a named lock in stateDir for the rest of the test, as
// the process running a spawn or a switch does, and returns when its record
// says it was taken.
func acquireForTest(t *testing.T, stateDir, name string) time.Time {
	t.Helper()
	held, err := lock.AcquireExclusiveNamed(stateDir, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(stateDir, name) })
	return held.Acquired
}
