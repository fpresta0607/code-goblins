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

// On 2026-10-07 pd-discovery-ladders' cfo spawn spent thirteen minutes
// installing its dependencies before its host started, and the monitor woke
// the CFO with endpoint_missing eight minutes in: the launch grace ran from
// the task record, which a spawn writes before it installs anything. A goblin
// whose spawn is still running, or whose switch is relaunching it, is
// launching, however long that takes; one a spawn left behind, or that
// another task's spawn holds the lock for, is not.
func TestScanKeepsAGoblinQuietWhileItsSpawnOrSwitchRuns(t *testing.T) {
	composer := []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}
	for _, test := range []struct {
		name       string
		lockName   string
		isLockLate bool
		hasHost    bool
		wasSeen    bool
		shouldWake bool
	}{
		{"its spawn still installing", ".spawn.lock", false, false, false, false},
		{"its spawn starting its host", ".spawn.lock", false, true, false, false},
		{"its switch relaunching a goblin seen alive", ".switch-g1.lock", false, false, true, false},
		{"another task's spawn holding the lock", ".spawn.lock", true, false, false, true},
		{"its host started a minute ago, its spawn over", "", false, true, false, false},
		{"its spawn gone", "", false, false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			now := time.Now().UTC().Add(time.Minute)
			meta := nativeMeta("g1", "claude")
			if test.lockName != "" && !test.isLockLate {
				acquireForTest(t, stateDir, test.lockName)
			}
			meta.SpawnGen = fmt.Sprintf("s%d", time.Now().Add(-time.Millisecond).UnixNano())
			if test.isLockLate {
				acquireForTest(t, stateDir, test.lockName)
			}
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
// the process running a spawn or a switch does.
func acquireForTest(t *testing.T, stateDir, name string) {
	t.Helper()
	if _, err := lock.AcquireExclusiveNamed(stateDir, name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(stateDir, name) })
}
