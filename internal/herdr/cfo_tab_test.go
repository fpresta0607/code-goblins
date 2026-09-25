package herdr

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

var cfoContainer = Container{Session: "fleet", WorkspaceID: "ws-1"}

// A cfo tab with an agent in it is the CFO already running: its pane is
// returned and nothing is created beside it.
func TestCFOTabFindsTheCFOAlreadyRunning(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-g1","label":"gb-g1"},{"tab_id":"tab-cfo","label":"cfo"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-g1","tab_id":"tab-g1"},{"pane_id":"pane-cfo","tab_id":"tab-cfo"}]}}`),
		jsonReply(`{"result":{"pane":{"pane_id":"pane-cfo"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
	}}
	var sleeps []time.Duration

	endpoint, running, err := newTestClient(runner, &sleeps).CFOTab(context.Background(), cfoContainer, `C:\repo`)

	if err != nil || !running {
		t.Fatalf("CFOTab = %+v, %v, %v; want the running CFO", endpoint, running, err)
	}
	if want := (Endpoint{Target: Target{Session: "fleet", Pane: "pane-cfo"}, WorkspaceID: "ws-1", TabID: "tab-cfo", PaneID: "pane-cfo"}); endpoint != want {
		t.Errorf("endpoint = %#v, want %#v", endpoint, want)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "get", "pane-cfo", "--session", "fleet"),
		command("herdr", "agent", "get", "pane-cfo", "--session", "fleet"),
	})
}

// A cfo tab whose pane holds no agent, such as the factory tab EnsureContainer
// adopted or a CFO that exited, is reused rather than duplicated.
func TestCFOTabReusesACFOTabWithNoAgent(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-cfo","label":"cfo"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-cfo","tab_id":"tab-cfo"}]}}`),
		jsonReply(`{"result":{"pane":{"pane_id":"pane-cfo"}}}`),
		{result: execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}},
	}}
	var sleeps []time.Duration

	endpoint, running, err := newTestClient(runner, &sleeps).CFOTab(context.Background(), cfoContainer, `C:\repo`)

	if err != nil || running || endpoint.PaneID != "pane-cfo" || endpoint.TabID != "tab-cfo" {
		t.Fatalf("CFOTab = %+v, %v, %v; want the idle cfo tab reused", endpoint, running, err)
	}
	for _, request := range runner.Requests() {
		if len(request.Args) >= 2 && request.Args[0] == "tab" && request.Args[1] == "create" {
			t.Fatalf("CFOTab created a second cfo tab: %q", request.Args)
		}
	}
}

// With no cfo tab, one is created in the project, beside the goblins' tabs.
func TestCFOTabCreatesTheCFOsTabInTheProject(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-g1","label":"gb-g1"}]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-cfo"},"root_pane":{"pane_id":"pane-cfo"}}}`),
	}}
	var sleeps []time.Duration

	endpoint, running, err := newTestClient(runner, &sleeps).CFOTab(context.Background(), cfoContainer, `C:\repo`)

	if err != nil || running {
		t.Fatalf("CFOTab = %+v, %v, %v; want a new tab", endpoint, running, err)
	}
	if want := (Endpoint{Target: Target{Session: "fleet", Pane: "pane-cfo"}, WorkspaceID: "ws-1", TabID: "tab-cfo", PaneID: "pane-cfo"}); endpoint != want {
		t.Errorf("endpoint = %#v, want %#v", endpoint, want)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "tab", "create", "--workspace", "ws-1", "--cwd", `C:\repo`, "--label", "cfo", "--no-focus", "--session", "fleet"),
	})
}

// Focus brings the CFO's workspace and tab to the front for the next client.
func TestFocusBringsTheWorkspaceAndTabToTheFront(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{}}`),
		jsonReply(`{"result":{}}`),
	}}
	var sleeps []time.Duration

	err := newTestClient(runner, &sleeps).Focus(context.Background(), Endpoint{Target: Target{Session: "fleet", Pane: "pane-cfo"}, WorkspaceID: "ws-1", TabID: "tab-cfo", PaneID: "pane-cfo"})

	if err != nil {
		t.Fatal(err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "workspace", "focus", "ws-1", "--session", "fleet"),
		command("herdr", "tab", "focus", "tab-cfo", "--session", "fleet"),
	})
}
