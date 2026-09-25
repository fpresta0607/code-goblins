package cleanup

import (
	"context"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// Cleanup proves the task's terminal agent-free and closes it through the
// terminal backend, so a second backend's goblins are cleaned the same way.
func TestCleanupClosesTheTaskTerminalThroughTheBackend(t *testing.T) {
	fixture := newCleanupFixture(t)
	fake := &terminaltest.Fake{
		Session: "fleet",
		Structure: herdr.SessionSnapshot{
			Protocol: herdr.SupportedProtocol,
			Panes:    []herdr.SnapshotPane{{ID: "pane-g1", TabID: "tab-g1", WorkspaceID: "ws"}},
		},
	}
	fixture.service.Terminal = fake

	_, err := fixture.service.Cleanup(context.Background(), "g1")

	if err != nil {
		t.Fatalf("Cleanup: %v (calls %q)", err, fake.Calls())
	}
	if missing := fake.Missing("Snapshot", "CloseTab fleet tab-g1"); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
	if len(fixture.git.returned) != 1 {
		t.Errorf("worktree returns = %v, want one", fixture.git.returned)
	}
}

// A terminal the backend still reports an agent in is never closed.
func TestCleanupRefusesATerminalTheBackendReportsAnAgentIn(t *testing.T) {
	fixture := newCleanupFixture(t)
	fake := &terminaltest.Fake{
		Session: "fleet",
		Structure: herdr.SessionSnapshot{
			Protocol: herdr.SupportedProtocol,
			Panes:    []herdr.SnapshotPane{{ID: "pane-g1", TabID: "tab-g1", WorkspaceID: "ws"}},
			Agents:   []herdr.SnapshotAgent{{PaneID: "pane-g1", Agent: "claude", Status: "working"}},
		},
	}
	fixture.service.Terminal = fake

	_, err := fixture.service.Cleanup(context.Background(), "g1")

	if err == nil || !strings.Contains(err.Error(), "still has agent") {
		t.Fatalf("Cleanup error = %v, want the live agent refused", err)
	}
	for _, call := range fake.Calls() {
		if strings.HasPrefix(call, "CloseTab") {
			t.Fatalf("closed a terminal with a live agent: %q", fake.Calls())
		}
	}
}
