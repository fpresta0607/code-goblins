package digest

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBothDigestsShowPauseReasonAndResumeCondition(t *testing.T) {
	h := newDigestHome(t)
	meta := state.TaskMeta{ID: "paused-task", SpawnGen: "generation-1", Harness: "codex"}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	condition, err := state.NewPauseCondition("allowance", "2026-10-03T00:00:00Z", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
		t.Fatal(err)
	}
	var full bytes.Buffer
	writeMetaEntry(h.State, meta.ID, 5, &werr{w: &full})
	for _, text := range []string{fleetLine(h.State, meta.ID), full.String()} {
		if !strings.Contains(text, "Allowance; resumes at 2026-10-03T00:00:00Z") {
			t.Fatalf("digest hid the pause condition: %s", text)
		}
	}
}
