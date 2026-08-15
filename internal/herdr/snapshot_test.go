package herdr

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const testSnapshotEnvelope = `{"id":"cli:api:snapshot","result":{"type":"session_snapshot","snapshot":{"version":"0.8.0-test","protocol":19,"workspaces":[{"workspace_id":"w3","label":"firstmate"}],"tabs":[{"tab_id":"w3:t4","workspace_id":"w3","label":"fm-task"}],"panes":[{"pane_id":"w3:p4","tab_id":"w3:t4","workspace_id":"w3"}],"agents":[{"pane_id":"w3:p4","tab_id":"w3:t4","workspace_id":"w3","agent":"claude","agent_status":"done"}]}}}`

func TestSnapshotParsesTypedEnvelopeWithoutJSONFlag(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply(testSnapshotEnvelope)}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.Protocol != 19 || snapshot.Version != "0.8.0-test" {
		t.Errorf("Snapshot metadata = %+v, want protocol 19 and version", snapshot)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != "w3" || snapshot.Workspaces[0].Label != "firstmate" {
		t.Errorf("workspaces = %+v", snapshot.Workspaces)
	}
	if len(snapshot.Tabs) != 1 || snapshot.Tabs[0].ID != "w3:t4" || snapshot.Tabs[0].WorkspaceID != "w3" || snapshot.Tabs[0].Label != "fm-task" {
		t.Errorf("tabs = %+v", snapshot.Tabs)
	}
	if len(snapshot.Panes) != 1 || snapshot.Panes[0].ID != "w3:p4" || snapshot.Panes[0].TabID != "w3:t4" {
		t.Errorf("panes = %+v", snapshot.Panes)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].PaneID != "w3:p4" || snapshot.Agents[0].Status != "done" {
		t.Errorf("agents = %+v", snapshot.Agents)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "api", "snapshot", "--session", "fleet"),
	})
}

func TestSnapshotRefusesWrongShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"malformed", `{`, "decode api snapshot"},
		{"missing result", `{"id":"cli:api:snapshot"}`, "decode api snapshot"},
		{"wrong type", `{"result":{"type":"workspace_list","snapshot":{"protocol":19}}}`, `type "workspace_list"`},
		{"missing protocol", `{"result":{"type":"session_snapshot","snapshot":{}}}`, "missing protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{rawReply(test.body)}}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)

			_, err := client.Snapshot(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Snapshot error = %v, want %q", err, test.want)
			}
		})
	}

	t.Run("structured error envelope", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{{result: execx.Result{Stderr: []byte(`{"error":{"code":"server_unavailable"}}`), ExitCode: 1}}}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)

		if _, err := client.Snapshot(context.Background()); err == nil {
			t.Fatal("Snapshot accepted a failed request")
		}
	})
}

func TestCaptureEvidencePinsBoundedUnwrappedRead(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply("line one\nline two\n")}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	capture, err := client.CaptureEvidence(context.Background(), Target{Session: "fleet", Pane: "w3:p4"})
	if err != nil {
		t.Fatalf("CaptureEvidence: %v", err)
	}
	if string(capture) != "line one\nline two\n" {
		t.Errorf("capture = %q, want the full bounded read", capture)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "pane", "read", "w3:p4", "--source", "recent-unwrapped", "--lines", "200", "--session", "fleet"),
	})
}

func TestCaptureEvidenceRefusesEmptyRead(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply("")}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if _, err := client.CaptureEvidence(context.Background(), Target{Session: "fleet", Pane: "w3:p4"}); err == nil {
		t.Fatal("CaptureEvidence accepted an empty read")
	}
}

func TestEffectiveSessionDefaults(t *testing.T) {
	if got := (&Client{}).EffectiveSession(); got != "default" {
		t.Errorf("EffectiveSession = %q, want default", got)
	}
	if got := (&Client{Session: "fleet"}).EffectiveSession(); got != "fleet" {
		t.Errorf("EffectiveSession = %q, want fleet", got)
	}
}
