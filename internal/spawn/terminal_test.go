package spawn

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/terminal/terminaltest"
)

// fakeTerminal is a backend with one task terminal to hand out, whose agent
// accepts every prompt and goes straight to work.
func fakeTerminal() *terminaltest.Fake {
	return &terminaltest.Fake{
		Session:   "fleet",
		Kinds:     map[string]bool{"claude": true},
		Container: herdr.Container{Session: "fleet", WorkspaceID: "w1"},
		Endpoint:  herdr.Endpoint{Target: herdr.Target{Session: "fleet", Pane: "w1:p7"}, WorkspaceID: "w1", TabID: "w1:t7", PaneID: "w1:p7"},
		Detail:    herdr.AgentDetail{Agent: "claude", Status: "idle"},
		Working:   herdr.SubmitWorking,
	}
}

// A spawn makes every terminal step through the backend it is given, so a
// second backend can host a goblin without spawn changing.
func TestSpawnDrivesTheTaskTerminalThroughTheBackend(t *testing.T) {
	f := newFixture(t)
	fake := fakeTerminal()
	var opened []string
	f.service.Terminals = func(session string) terminal.Backend {
		opened = append(opened, session)
		return fake
	}

	result, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v (calls %q)", err, fake.Calls())
	}
	if len(opened) != 1 || opened[0] != "fleet" {
		t.Errorf("opened the backend in %q, want once in the request's session", opened)
	}
	project := result.Meta.Project
	if missing := fake.Missing("EnsureServer", "Preflight", "AgentKinds", "EnsureContainer "+project, "CreateTask gb-task-7 "+project,
		"AgentStart fleet:w1:p7 gb-task-7 claude", "AgentPrompt fleet:w1:p7 ", "WaitForWorking fleet:w1:p7"); missing != "" {
		t.Errorf("no %q call in order: %q", missing, fake.Calls())
	}
	if result.Meta.HerdrPaneID != "w1:p7" || result.Meta.HerdrTabID != "w1:t7" || result.Meta.HerdrWorkspaceID != "w1" {
		t.Errorf("task metadata = %+v, want the backend's terminal", result.Meta)
	}
}

// A launch that fails closes the task terminal through the same backend, so a
// failed spawn leaves no terminal behind whichever backend made it.
func TestSpawnClosesTheTaskTerminalWhenTheLaunchFails(t *testing.T) {
	f := newFixture(t)
	fake := fakeTerminal()
	fake.Fail = map[string]error{"AgentStart": errors.New("the harness did not start")}
	f.service.Terminals = func(string) terminal.Backend { return fake }

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "the harness did not start") {
		t.Fatalf("Spawn error = %v, want the failed start", err)
	}
	if !fake.Asked("CloseTab fleet w1:t7") {
		t.Errorf("the task terminal was not closed: %q", fake.Calls())
	}
	if _, err := state.ReadTaskMeta(f.stateDir, f.request.ID); err == nil {
		t.Error("the failed task's metadata was kept")
	}
}

// A switch opens the backend in the task's own session and reads the
// terminal's agent through it before changing anything.
func TestSwitchReadsTheTaskTerminalThroughTheBackend(t *testing.T) {
	fixture := newSwitchFixture(t)
	fake := fakeTerminal()
	fake.Status = herdr.AgentAlive
	var opened []string
	fixture.service.Terminals = func(session string) terminal.Backend {
		opened = append(opened, session)
		return fake
	}

	_, err := fixture.service.Switch(context.Background(), SwitchRequest{
		ID:      fixture.meta.ID,
		Harness: harness.Kind(fixture.meta.Harness),
		Model:   fixture.meta.Model,
		Effort:  fixture.meta.Effort,
	})

	if err == nil || !strings.Contains(err.Error(), "nothing to switch") {
		t.Fatalf("Switch error = %v, want the running harness left alone", err)
	}
	if len(opened) != 1 || opened[0] != fixture.meta.HerdrSession {
		t.Errorf("opened the backend in %q, want once in the task's session %q", opened, fixture.meta.HerdrSession)
	}
	if want := "AgentStatus " + fixture.meta.HerdrSession + ":" + fixture.meta.HerdrPaneID; !fake.Asked(want) {
		t.Errorf("no %q call: %q", want, fake.Calls())
	}
}

// A pane is live only when the backend reports a live agent in it.
func TestHerdrLivenessAsksTheBackend(t *testing.T) {
	meta := state.TaskMeta{HerdrSession: "fleet", HerdrPaneID: "w1:p7"}
	for status, want := range map[herdr.AgentStatus]bool{herdr.AgentAlive: true, herdr.AgentDead: false, herdr.AgentMissing: false} {
		fake := fakeTerminal()
		fake.Status = status

		live := HerdrLiveness{Client: fake}.Live(context.Background(), meta)

		if live != want || !fake.Asked("AgentStatus fleet:w1:p7") {
			t.Errorf("%s: live = %v, want %v (calls %q)", status, live, want, fake.Calls())
		}
	}
}
