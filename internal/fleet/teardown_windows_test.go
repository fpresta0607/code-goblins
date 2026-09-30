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
