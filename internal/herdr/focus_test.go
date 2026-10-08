package herdr

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

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
