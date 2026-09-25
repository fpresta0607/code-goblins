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

// staleCFOTabReplies are the replies for a cfo tab whose pane holds no agent,
// up to the fresh cfo tab created in the project beside it.
var staleCFOTabReplies = []runnerReply{
	jsonReply(`{"result":{"tabs":[{"tab_id":"tab-old","label":"cfo"}]}}`),
	jsonReply(`{"result":{"panes":[{"pane_id":"pane-old","tab_id":"tab-old"}]}}`),
	jsonReply(`{"result":{"pane":{"pane_id":"pane-old"}}}`),
	{result: execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}},
	jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
}

var staleCFOTabRequests = []execx.Request{
	command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
	command("herdr", "pane", "list", "--workspace", "ws-1", "--session", "fleet"),
	command("herdr", "pane", "get", "pane-old", "--session", "fleet"),
	command("herdr", "agent", "get", "pane-old", "--session", "fleet"),
	command("herdr", "tab", "create", "--workspace", "ws-1", "--cwd", `C:\repo`, "--label", "cfo", "--no-focus", "--session", "fleet"),
	command("herdr", "pane", "process-info", "--pane", "pane-old", "--session", "fleet"),
}

var freshCFOTab = Endpoint{Target: Target{Session: "fleet", Pane: "pane-new"}, WorkspaceID: "ws-1", TabID: "tab-new", PaneID: "pane-new"}

// A cfo tab with no agent sitting at its shell prompt, such as the factory
// tab EnsureContainer adopted or one whose CFO exited, is in another
// directory than the project: a fresh cfo tab is created in the project
// first, and then the old one is closed.
func TestCFOTabReplacesAnIdleCFOTabWithOneInTheProject(t *testing.T) {
	runner := &fakeRunner{replies: append(append([]runnerReply{}, staleCFOTabReplies...),
		jsonReply(`{"result":{"process_info":{"foreground_process_group_id":40,"shell_pid":40}}}`),
		jsonReply(`{"result":{}}`),
	)}
	var sleeps []time.Duration

	endpoint, running, err := newTestClient(runner, &sleeps).CFOTab(context.Background(), cfoContainer, `C:\repo`)

	if err != nil || running || endpoint != freshCFOTab {
		t.Fatalf("CFOTab = %+v, %v, %v; want the fresh tab %+v", endpoint, running, err, freshCFOTab)
	}
	assertRequests(t, runner.Requests(), append(append([]execx.Request{}, staleCFOTabRequests...),
		command("herdr", "tab", "close", "tab-old", "--session", "fleet"),
	))
}

// A cfo tab with no agent whose pane runs something else, goblins itself
// among them, or whose process cannot be read, is never closed: it is renamed
// to shell so the cfo label names only the fresh tab.
func TestCFOTabRenamesABusyCFOTabWithNoAgentToShell(t *testing.T) {
	for name, processInfo := range map[string]runnerReply{
		"a program in the foreground": jsonReply(`{"result":{"process_info":{"foreground_process_group_id":41,"shell_pid":40}}}`),
		"process info unreadable":     {result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &fakeRunner{replies: append(append([]runnerReply{}, staleCFOTabReplies...),
				processInfo,
				jsonReply(`{"result":{}}`),
			)}
			var sleeps []time.Duration

			endpoint, running, err := newTestClient(runner, &sleeps).CFOTab(context.Background(), cfoContainer, `C:\repo`)

			if err != nil || running || endpoint != freshCFOTab {
				t.Fatalf("CFOTab = %+v, %v, %v; want the fresh tab %+v", endpoint, running, err, freshCFOTab)
			}
			assertRequests(t, runner.Requests(), append(append([]execx.Request{}, staleCFOTabRequests...),
				command("herdr", "tab", "rename", "tab-old", "shell", "--session", "fleet"),
			))
		})
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
