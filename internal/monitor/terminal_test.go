package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// The monitor reads each cycle's schema, structure, agents and screen evidence
// through the terminal backend, so it watches a second backend's goblins the
// same way.
func TestHerdrProberReadsThroughTheTerminalBackend(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	fake := &terminaltest.Fake{
		Session: meta.HerdrSession,
		Structure: herdr.SessionSnapshot{
			Protocol:   herdr.SupportedProtocol,
			Workspaces: []herdr.SnapshotWorkspace{{ID: "ws", Label: "cfo"}},
			Tabs:       []herdr.SnapshotTab{{ID: meta.HerdrTabID, WorkspaceID: "ws", Label: "gb-" + meta.ID}},
			Panes:      []herdr.SnapshotPane{{ID: meta.HerdrPaneID, TabID: meta.HerdrTabID, WorkspaceID: "ws"}},
			Agents:     []herdr.SnapshotAgent{{PaneID: meta.HerdrPaneID, TabID: meta.HerdrTabID, WorkspaceID: "ws", Agent: "claude", Status: "working"}},
		},
		Agents:   []herdr.AgentRecord{{Agent: "claude", Status: "working", PaneID: meta.HerdrPaneID, TabID: meta.HerdrTabID, WorkspaceID: "ws"}},
		Evidence: capture("g1 text"),
	}
	service := testService(stateDir, NewHerdrProber(fake), &now)

	result, err := service.Scan(context.Background())

	if err != nil {
		t.Fatalf("Scan: %v (calls %q)", err, fake.Calls())
	}
	if len(result.Observations) != 1 || result.Observations[0].EndpointVerdict != ProbePresent {
		t.Fatalf("observations = %+v, want the task present", result.Observations)
	}
	if missing := fake.Missing("CheckSchema", "Snapshot", "CaptureEvidence "+meta.HerdrSession+":"+meta.HerdrPaneID); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
	if !fake.Asked("AgentList") {
		t.Errorf("the agents were not read: %q", fake.Calls())
	}
}
