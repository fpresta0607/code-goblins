package herdr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// preflightSchemaJSON builds a protocol-19 schema-1 document; omit drops one
// advertised method and envelopes drops one response envelope.
func preflightSchemaJSON(protocol, schemaVersion int, omit string, envelopes ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"protocol":%d,"schema_version":%d,"schemas":{`, protocol, schemaVersion)
	for _, envelope := range envelopes {
		fmt.Fprintf(&b, `%q:{},`, envelope)
	}
	b.WriteString(`"request":{"oneOf":[`)
	first := true
	for _, method := range requiredMethods {
		if method == omit {
			continue
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"properties":{"method":{"const":%q}}}`, method)
	}
	b.WriteString(`]}}}`)
	return b.String()
}

func fullSchemaJSON() string {
	return preflightSchemaJSON(19, 1, "", "success_response", "error_response")
}

func statusJSON(clientProtocol, serverProtocol int, running, compatible bool) string {
	return fmt.Sprintf(`{"client":{"protocol":%d},"server":{"running":%t,"protocol":%d,"compatible":%t}}`, clientProtocol, running, serverProtocol, compatible)
}

func sessionListJSON(names ...string) string {
	var entries []string
	for _, name := range names {
		entries = append(entries, fmt.Sprintf(`{"name":%q,"running":true}`, name))
	}
	return `{"sessions":[` + strings.Join(entries, ",") + `]}`
}

func TestPreflightPinsDiscoveryArgumentSplits(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		rawReply(fullSchemaJSON()),
		rawReply(statusJSON(19, 19, true, true)),
		rawReply(sessionListJSON("fleet", "other")),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if err := client.Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "api", "schema", "--json", "--session", "fleet"),
		command("herdr", "status", "--json", "--session", "fleet"),
		command("herdr", "session", "list", "--json", "--session", "fleet"),
	})
}

func TestOperationalCommandsOmitJSONFlag(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"workspaces":[{"workspace_id":"ws-1","label":"cfo"}]}}`),
		jsonReply(`{"result":{"tabs":[]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":"w1:p1"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	container, err := client.EnsureContainer(context.Background(), `C:\repo`)
	if err != nil {
		t.Fatalf("EnsureContainer: %v", err)
	}
	if _, err := client.CreateTask(context.Background(), container, "gb-task", `C:\repo`); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, request := range runner.Requests() {
		for _, argument := range request.Args {
			if argument == "--json" {
				t.Fatalf("operational command carried the redundant --json flag: %q", request.Args)
			}
		}
	}
}

func TestCheckSchemaRefusesUnsupportedContracts(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"malformed document", `{`, "decode api schema"},
		{"unsupported schema version", preflightSchemaJSON(19, 2, "", "success_response", "error_response"), "schema version 2"},
		{"unsupported protocol", preflightSchemaJSON(20, 1, "", "success_response", "error_response"), "protocol 20"},
		{"missing success envelope", preflightSchemaJSON(19, 1, "", "error_response"), "success_response"},
		{"missing error envelope", preflightSchemaJSON(19, 1, "", "success_response"), "error_response"},
		{"missing request contract", `{"protocol":19,"schema_version":1,"schemas":{"success_response":{},"error_response":{}}}`, "request contract"},
		{"missing snapshot method", preflightSchemaJSON(19, 1, "session.snapshot", "success_response", "error_response"), "session.snapshot"},
		{"missing pane read method", preflightSchemaJSON(19, 1, "pane.read", "success_response", "error_response"), "pane.read"},
		{"missing agent get method", preflightSchemaJSON(19, 1, "agent.get", "success_response", "error_response"), "agent.get"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{rawReply(test.body)}}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)

			err := client.CheckSchema(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CheckSchema error = %v, want %q", err, test.want)
			}
			assertRequests(t, runner.Requests(), []execx.Request{
				command("herdr", "api", "schema", "--json", "--session", "fleet"),
			})
		})
	}
}

func TestCheckSchemaSurfacesCommandFailure(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{{result: execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"server_unavailable"}}`)}}}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	err := client.CheckSchema(context.Background())
	var commandErr *CommandError
	if err == nil || !errors.As(err, &commandErr) || commandErr.Operation != "api schema" {
		t.Fatalf("CheckSchema error = %v, want api schema CommandError", err)
	}
}

func TestCheckRuntimeRefusesIncompatibleOrAmbiguousFacts(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		list    string
		want    string
		wantReq int
	}{
		{"protocol mismatch", statusJSON(19, 18, true, true), sessionListJSON("fleet"), "protocol mismatch", 1},
		{"client protocol mismatch", statusJSON(18, 19, true, true), sessionListJSON("fleet"), "protocol mismatch", 1},
		{"server not running", statusJSON(19, 19, false, true), sessionListJSON("fleet"), "not running", 1},
		{"incompatible server", statusJSON(19, 19, true, false), sessionListJSON("fleet"), "compatibility", 1},
		{"malformed status", `{`, sessionListJSON("fleet"), "decode status", 1},
		{"session not addressable", statusJSON(19, 19, true, true), sessionListJSON("other"), "not addressable", 2},
		{"ambiguous session", statusJSON(19, 19, true, true), sessionListJSON("fleet", "fleet"), "ambiguous", 2},
		{"malformed session list", statusJSON(19, 19, true, true), `{`, "decode session list", 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{rawReply(test.status), rawReply(test.list)}}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)

			err := client.CheckRuntime(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CheckRuntime error = %v, want %q", err, test.want)
			}
			if got := len(runner.Requests()); got != test.wantReq {
				t.Fatalf("requests = %d, want %d (no session list after a failed status check)", got, test.wantReq)
			}
		})
	}
}

func TestPreflightStopsAtFailedSchema(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply(preflightSchemaJSON(20, 1, "", "success_response", "error_response"))}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	err := client.Preflight(context.Background())
	if err == nil || !strings.Contains(err.Error(), "protocol 20") {
		t.Fatalf("Preflight error = %v, want unsupported protocol", err)
	}
	if got := len(runner.Requests()); got != 1 {
		t.Fatalf("requests = %d, want only the schema probe before any runtime check", got)
	}
}
