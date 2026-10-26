package fleet

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestFleetStatusKeepsWindowsTeardownUntilTheIdentityDisappears(t *testing.T) {
	started, exists := proc.StartTime(os.Getpid())
	if !exists {
		t.Fatal("could not identify the test process")
	}
	for _, isStopped := range []bool{false, true} {
		name := "paused"
		if isStopped {
			name = "stopped"
		}
		t.Run(name, func(t *testing.T) {
			h := snapshotHome(t)
			record := state.Lifecycle{ID: "task", Generation: "generation-1", Operation: "action-1", Action: "pause", Phase: "paused", Teardown: []state.TeardownProcess{{PID: os.Getpid(), Started: started, Name: "chrome.exe"}}}
			if isStopped {
				record.Action, record.Phase = "stop", "stopped"
				if err := state.WriteOutcome(h.State, state.Outcome{ID: record.ID, Generation: record.Generation, Phase: "stopped", Reason: "Requested from the board"}); err != nil {
					t.Fatal(err)
				}
			} else {
				meta := writeSnapshotMeta(t, h, record.ID, t.TempDir(), t.TempDir())
				record.Generation = meta.SpawnGen
			}
			for _, isPresent := range []bool{true, false} {
				if !isPresent {
					record.Teardown[0].Started = started.Add(-time.Hour)
				}
				if err := state.WriteLifecycle(h.State, record); err != nil {
					t.Fatal(err)
				}
				snapshot, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				if err := RenderMarkdown(&output, snapshot); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(output.String(), "finishing Windows teardown: chrome.exe pid") != isPresent {
					t.Fatalf("present=%t status=%s", isPresent, output.String())
				}
			}
		})
	}
}

// A lifecycle record of another generation describes a task that is no longer
// running, so its teardown names processes the current task never started. On
// 2026-10-24 the fleet showed a finished teardown naming git.exe that had since
// become an unrelated protected process. The same record, differing only by
// generation, is the control.
func TestFleetStatusLeavesOutATeardownFromAnotherGeneration(t *testing.T) {
	// Arrange
	started, exists := proc.StartTime(os.Getpid())
	if !exists {
		t.Fatal("could not identify the test process")
	}
	h := snapshotHome(t)
	meta := writeSnapshotMeta(t, h, "task", t.TempDir(), t.TempDir())
	record := state.Lifecycle{ID: "task", Generation: "an-earlier-generation", Operation: "action-1", Action: "pause", Phase: "paused", Teardown: []state.TeardownProcess{{PID: os.Getpid(), Started: started, Name: "git.exe"}}}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}

	// Act
	other, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
	if err != nil {
		t.Fatal(err)
	}
	var otherOut bytes.Buffer
	if err := RenderMarkdown(&otherOut, other); err != nil {
		t.Fatal(err)
	}
	record.Generation = meta.SpawnGen
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Fatal(err)
	}
	own, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
	if err != nil {
		t.Fatal(err)
	}
	var ownOut bytes.Buffer
	if err := RenderMarkdown(&ownOut, own); err != nil {
		t.Fatal(err)
	}

	// Assert
	if strings.Contains(otherOut.String(), "finishing Windows teardown") {
		t.Fatalf("another generation's teardown was shown: %s", otherOut.String())
	}
	if !strings.Contains(ownOut.String(), "finishing Windows teardown: git.exe pid") {
		t.Fatalf("this generation's teardown was hidden: %s", ownOut.String())
	}
}
