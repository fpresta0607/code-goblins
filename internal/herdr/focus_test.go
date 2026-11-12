package herdr

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Focus brings the CEO's workspace and tab to the front for the next client.
func TestFocusBringsTheWorkspaceAndTabToTheFront(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{}}`),
		jsonReply(`{"result":{}}`),
	}}
	var sleeps []time.Duration

	err := newTestClient(runner, &sleeps).Focus(context.Background(), Endpoint{Target: Target{Session: "fleet", Pane: "pane-ceo"}, WorkspaceID: "ws-1", TabID: "tab-ceo", PaneID: "pane-ceo"})

	if err != nil {
		t.Fatal(err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "workspace", "focus", "ws-1", "--session", "fleet"),
		command("herdr", "tab", "focus", "tab-ceo", "--session", "fleet"),
	})
}
