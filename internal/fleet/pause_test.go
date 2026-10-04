package fleet

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestFleetViewShowsWhyPausedWorkWillResume(t *testing.T) {
	h := snapshotHome(t)
	meta := writeSnapshotMeta(t, h, "paused-task", t.TempDir(), t.TempDir())
	condition, err := state.NewPauseCondition("memory", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
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
	if !strings.Contains(output.String(), condition.Description()) {
		t.Fatalf("pause reason missing: %s", output.String())
	}
}
