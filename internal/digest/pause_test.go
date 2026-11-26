package digest

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/handoff"
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
	if err := ComposeReadOnly(h, "inspection", &full); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fleetLine(context.Background(), h.State, meta.ID, fleet.NewTerminalEndpoint(h.State, nil)), full.String()} {
		if !strings.Contains(text, "Paused for the allowance; resumes at 2026-10-03T00:00:00Z") {
			t.Fatalf("digest hid the pause condition: %s", text)
		}
	}
}

// The digest's fleet counts the tasks handed off to a cloud session in their
// current session, naming each with its pull request, and not a task whose
// handoff ended or one paused for anything else.
func TestBothDigestsCountTheTasksHandedOffToACloudSession(t *testing.T) {
	// Arrange
	h := newDigestHome(t)
	now := time.Now().UTC()
	handoffPause, err := state.NewPauseCondition("handoff", "", now)
	if err != nil {
		t.Fatal(err)
	}
	overlordPause, err := state.NewPauseCondition("overlord", "", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id      string
		pause   state.PauseCondition
		isEnded bool
	}{
		{"handed-task", handoffPause, false},
		{"returned-task", handoffPause, true},
		{"paused-task", overlordPause, false},
	} {
		meta := state.TaskMeta{ID: c.id, SpawnGen: "generation-1", Harness: "claude"}
		if err := state.WriteTaskMeta(h.State, meta); err != nil {
			t.Fatal(err)
		}
		if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: c.id, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused", Pause: &c.pause}); err != nil {
			t.Fatal(err)
		}
		record := handoff.Record{Task: c.id, Started: now, PullRequest: "https://github.com/o/r/pull/12", HandedOff: &now}
		if c.isEnded {
			record.Ended = &now
		}
		if err := handoff.WriteRecord(h.State, record); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	brief, _ := briefFleet(sampleFleet(context.Background(), h.State, fleet.NewTerminalEndpoint(h.State, nil)), 3000)
	var full bytes.Buffer
	if err := ComposeReadOnly(h, "inspection", &full); err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, text := range []string{string(brief), full.String()} {
		if !strings.Contains(text, "Handed off to a cloud session: 1 (handed-task at https://github.com/o/r/pull/12)") {
			t.Fatalf("the digest did not count the handed-off task alone: %s", text)
		}
	}
}
