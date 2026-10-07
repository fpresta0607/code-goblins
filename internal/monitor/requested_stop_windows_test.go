package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// Wakes 5842, 5982 and 5987 on 2026-10-07 each came seconds after a pause
// the CFO asked for ran out of time: the lifecycle ended failed with the
// goblin's host gone, as asked, and the monitor woke the CFO that the host
// did not answer. A pause or stop that failed while its host is gone ended
// the way it was asked to; a resume that failed with no host is a goblin
// missing, and wakes.
func TestAFailedPauseOrStopWhoseHostIsGoneDoesNotWake(t *testing.T) {
	for _, test := range []struct {
		name       string
		action     string
		shouldWake bool
	}{
		{"a pause that ran out of time", "pause", false},
		{"a stop that ran out of time", "stop", false},
		{"a resume that failed", "resume", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 7, 14, 41, 0, 0, time.UTC)
			meta := nativeMeta("g1", "claude")
			meta.SpawnGen = "s1"
			writeTask(t, stateDir, meta)
			if err := WriteObservation(stateDir, Observation{Schema: Schema, TaskID: "g1", Endpoint: endpointString(meta), EndpointVerdict: ProbePresent, Digest: "seen", LastSeen: now.Add(-time.Minute), LastProgress: now.Add(-time.Minute), LastObserved: now.Add(-time.Minute), Health: HealthBusy, Reason: None}); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(stateDir, state.Lifecycle{ID: "g1", Generation: "s1", RequestGeneration: "s1", Operation: "op-1", Action: test.action, Phase: "failed", Reason: "Requested by the operator"}); err != nil {
				t.Fatal(err)
			}
			probe := BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf()}}

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
