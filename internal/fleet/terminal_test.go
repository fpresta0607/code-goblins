package fleet

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// fakeTerminal is a backend whose task terminal holds a live, working agent.
func fakeTerminal() *terminaltest.Fake {
	return &terminaltest.Fake{
		Session: "fleet",
		Status:  herdr.AgentAlive,
		Detail:  herdr.AgentDetail{Agent: "claude", Status: "idle"},
		Busy:    herdr.BusyWorking,
		Screen:  "the goblin's last line\n",
	}
}

// The fleet snapshot reads each task's agent and activity from the backend,
// and never takes a Herdr pane's answer as proof the pane is the task's own.
func TestSnapshotEndpointReadsTheTerminalBackend(t *testing.T) {
	fake := fakeTerminal()
	endpoint := NewTerminalEndpoint(t.TempDir(), fake)
	meta := state.TaskMeta{ID: "task-7", HerdrSession: "fleet", HerdrPaneID: "w1:p7"}

	exists, busy, readErr := endpoint.Read(context.Background(), meta)
	valid, validErr := endpoint.(crewstate.StructuralValidator).Validate(context.Background(), meta)

	if readErr != nil || !exists || busy != herdr.BusyWorking {
		t.Fatalf("Read = %v, %q, %v; want a live, working agent", exists, busy, readErr)
	}
	if validErr != nil || valid {
		t.Errorf("Validate = %v, %v; want a Herdr pane never proven the task's own", valid, validErr)
	}
	if missing := fake.Missing("AgentStatus fleet:w1:p7", "BusyState fleet:w1:p7"); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
}
