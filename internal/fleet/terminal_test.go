package fleet

import (
	"context"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

var taskTerminal = herdr.Target{Session: "fleet", Pane: "w1:p7"}

// fakeTerminal is a backend whose task terminal holds a live, working agent
// that accepts every prompt.
func fakeTerminal() *terminaltest.Fake {
	return &terminaltest.Fake{
		Session: "fleet",
		Status:  herdr.AgentAlive,
		Detail:  herdr.AgentDetail{Agent: "claude", Status: "idle"},
		Busy:    herdr.BusyWorking,
		Screen:  "the goblin's last line\n",
	}
}

func taskSelector() *fakeResolver {
	return &fakeResolver{target: taskTerminal, meta: state.TaskMeta{ID: "task-7", HerdrSession: "fleet", HerdrPaneID: "w1:p7"}}
}

// Text reaches the goblin's agent through the terminal backend, and counts as
// delivered once the backend reports the agent took it.
func TestSenderSubmitsTextThroughTheTerminalBackend(t *testing.T) {
	fake := fakeTerminal()

	err := Sender{Resolve: taskSelector(), Terminal: fake, Sleep: noSleep}.Text(context.Background(), "task-7", "ship it")

	if err != nil {
		t.Fatalf("Text: %v (calls %q)", err, fake.Calls())
	}
	if missing := fake.Missing("AgentStatus fleet:w1:p7", "AgentDetail fleet:w1:p7", "AgentPrompt fleet:w1:p7 CFO: ship it", "AgentDetail fleet:w1:p7"); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
}

// A key goes to the goblin's terminal through the backend.
func TestSenderSendsKeysThroughTheTerminalBackend(t *testing.T) {
	fake := fakeTerminal()

	err := Sender{Resolve: taskSelector(), Terminal: fake, Sleep: noSleep}.Key(context.Background(), "task-7", "esc")

	if err != nil || !fake.Asked("SendKey fleet:w1:p7 Escape") {
		t.Fatalf("Key: %v (calls %q), want Escape sent to the task terminal", err, fake.Calls())
	}
}

// A peek is the terminal's screen as the backend reads it.
func TestPeekerReadsTheScreenFromTheTerminalBackend(t *testing.T) {
	fake := fakeTerminal()

	tail, err := Peeker{Resolve: taskSelector(), Terminal: fake}.Tail(context.Background(), "task-7", 5)

	if err != nil || tail != fake.Screen || !fake.Asked("Capture fleet:w1:p7 5") {
		t.Fatalf("Tail = %q, %v (calls %q), want the backend's screen", tail, err, fake.Calls())
	}
}

// The fleet snapshot reads each task's agent and activity from the backend.
func TestSnapshotEndpointReadsTheTerminalBackend(t *testing.T) {
	fake := fakeTerminal()
	endpoint := NewTerminalEndpoint(fake)

	exists, existsErr := endpoint.Exists(context.Background(), taskTerminal)
	busy, busyErr := endpoint.BusyState(context.Background(), taskTerminal)

	if existsErr != nil || !exists || busyErr != nil || busy != herdr.BusyWorking {
		t.Fatalf("Exists = %v, %v; BusyState = %q, %v; want a live, working agent", exists, existsErr, busy, busyErr)
	}
	if missing := fake.Missing("AgentStatus fleet:w1:p7", "BusyState fleet:w1:p7"); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
}
