package herdr

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type runnerReply struct {
	result execx.Result
	err    error
}

type fakeRunner struct {
	mu            sync.Mutex
	replies       []runnerReply
	requests      []execx.Request
	startRequests []execx.Request
	startErr      error
}

func (r *fakeRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	if len(r.replies) == 0 {
		return execx.Result{}, fmt.Errorf("unexpected command: %s %s", req.Name, strings.Join(req.Args, " "))
	}
	reply := r.replies[0]
	r.replies = r.replies[1:]
	return reply.result, reply.err
}

func (r *fakeRunner) Start(_ context.Context, req execx.Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startRequests = append(r.startRequests, req)
	return r.startErr
}

func (r *fakeRunner) Requests() []execx.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]execx.Request(nil), r.requests...)
}

func (r *fakeRunner) StartRequests() []execx.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]execx.Request(nil), r.startRequests...)
}

func jsonReply(body string) runnerReply {
	return runnerReply{result: execx.Result{Stdout: []byte(body)}}
}

func rawReply(body string) runnerReply {
	return runnerReply{result: execx.Result{Stdout: []byte(body)}}
}

func newTestClient(r *fakeRunner, sleeps *[]time.Duration) *Client {
	return &Client{
		Commands: r,
		Session:  "fleet",
		Sleep: func(_ context.Context, duration time.Duration) error {
			*sleeps = append(*sleeps, duration)
			return nil
		},
	}
}

func command(name string, args ...string) execx.Request {
	return execx.Request{Name: name, Args: args}
}

func assertRequests(t *testing.T, got, want []execx.Request) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %#v, want %#v", got, want)
	}
	for _, request := range got {
		// The session flag must be present; with a `--` agent-args separator
		// (agent start) it sits before the separator rather than at the tail.
		sessionAt := slices.Index(request.Args, "--session")
		if sessionAt < 0 || sessionAt+1 >= len(request.Args) || request.Args[sessionAt+1] != "fleet" {
			t.Errorf("request %q is missing explicit fleet session routing", request.Args)
		}
		if separator := slices.Index(request.Args, "--"); separator >= 0 && sessionAt > separator {
			t.Errorf("request %q routes the session flag to the agent", request.Args)
		}
	}
}

func TestEnsureServerStartsOnceAndPollsStatus(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"server":{"running":false}}`),
		jsonReply(`{"server":{"running":false}}`),
		jsonReply(`{"server":{"running":true}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if err := client.EnsureServer(context.Background()); err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "status", "--json", "--session", "fleet"),
		command("herdr", "status", "--json", "--session", "fleet"),
		command("herdr", "status", "--json", "--session", "fleet"),
	})
	if got, want := runner.StartRequests(), []execx.Request{command("herdr", "server", "--session", "fleet")}; !reflect.DeepEqual(got, want) {
		t.Fatalf("server starts = %#v, want %#v", got, want)
	}
	if got, want := sleeps, []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Errorf("sleeps = %v, want %v", got, want)
	}
}

func TestEnsureServerAcceptsRunningAtLastAllowedInterval(t *testing.T) {
	replies := []runnerReply{jsonReply(`{"server":{"running":false}}`)}
	for range 19 {
		replies = append(replies, jsonReply(`{"server":{"running":false}}`))
	}
	replies = append(replies, jsonReply(`{"server":{"running":true}}`))
	runner := &fakeRunner{replies: replies}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if err := client.EnsureServer(context.Background()); err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	if got, want := len(sleeps), 20; got != want {
		t.Errorf("sleep count = %d, want %d", got, want)
	}
	if got, want := len(runner.Requests()), 21; got != want {
		t.Errorf("status probes = %d, want %d", got, want)
	}
}

func TestEnsureServerFailsExplicitlyAfterTenSeconds(t *testing.T) {
	replies := make([]runnerReply, 0, 21)
	for range 21 {
		replies = append(replies, jsonReply(`{"server":{"running":false}}`))
	}
	runner := &fakeRunner{replies: replies}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	err := client.EnsureServer(context.Background())
	if err == nil || !strings.Contains(err.Error(), "did not report running within 10s") {
		t.Fatalf("EnsureServer error = %v, want explicit 10s failure", err)
	}
	if got, want := len(sleeps), 20; got != want {
		t.Errorf("sleep count = %d, want %d", got, want)
	}
}

func TestEnsureContainerUsesFlatLabelAndExplicitSession(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		{result: execx.Result{Stdout: []byte(`{"result":{"workspaces":[]}}`), Stderr: []byte("diagnostic only")}},
		jsonReply(`{"result":{"workspace":{"workspace_id":"ws-1"},"tab":{"tab_id":"tab-seeded"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	got, err := client.EnsureContainer(context.Background(), `C:\repo`)
	if err != nil {
		t.Fatalf("EnsureContainer: %v", err)
	}
	want := Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}
	if got != want {
		t.Errorf("EnsureContainer() = %#v, want %#v", got, want)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "workspace", "list", "--session", "fleet"),
		command("herdr", "workspace", "create", "--cwd", `C:\repo`, "--label", "cfo", "--no-focus", "--session", "fleet"),
	})
}

func TestEnsureContainerAdoptsOnlyOneMatchingWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    Container
		wantErr bool
	}{
		{
			name: "one matching workspace is adopted",
			body: `{"result":{"workspaces":[{"workspace_id":"ws-other","label":"other"},{"workspace_id":"ws-1","label":"cfo"}]}}`,
			want: Container{Session: "fleet", WorkspaceID: "ws-1"},
		},
		{
			name:    "matching workspaces are ambiguous",
			body:    `{"result":{"workspaces":[{"workspace_id":"ws-1","label":"cfo"},{"workspace_id":"ws-2","label":"cfo"}]}}`,
			wantErr: true,
		},
		{
			name:    "missing workspace id is rejected",
			body:    `{"result":{"workspaces":[{"label":"cfo"}]}}`,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: []runnerReply{jsonReply(test.body)}}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)

			got, err := client.EnsureContainer(context.Background(), `C:\repo`)
			if (err != nil) != test.wantErr {
				t.Fatalf("EnsureContainer error = %v, want error = %t", err, test.wantErr)
			}
			if got != test.want {
				t.Errorf("EnsureContainer() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestCreateTaskRefusesDuplicateWithLiveAgent(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-existing","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-existing","tab_id":"tab-existing"}]}}`),
		jsonReply(`{"result":{"pane":{"pane_id":"pane-existing"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	_, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1"}, "gb-task", `C:\repo`)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateTask error = %v, want duplicate refusal", err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "get", "pane-existing", "--session", "fleet"),
		command("herdr", "agent", "get", "pane-existing", "--session", "fleet"),
	})
}

func TestCreateTaskRefusesDuplicateWhenAnyPaneIsAlive(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-existing","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-husk","tab_id":"tab-existing"},{"pane_id":"pane-live","tab_id":"tab-existing"}]}}`),
		{result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}},
		jsonReply(`{"result":{"pane":{"pane_id":"pane-live"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	_, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1"}, "gb-task", `C:\repo`)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateTask error = %v, want duplicate refusal after a live second pane", err)
	}
	for _, request := range runner.Requests() {
		if len(request.Args) >= 2 && request.Args[0] == "tab" && (request.Args[1] == "create" || request.Args[1] == "close") {
			t.Fatalf("CreateTask issued unsafe duplicate-tab mutation: %q", request.Args)
		}
	}
}

func TestCreateTaskReplacesHuskAndPrunesOnlyExactSafeSeed(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-husk","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-husk","tab_id":"tab-husk"}]}}`),
		{result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}},
		{result: execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"tab_not_found"}}`)}},
		jsonReply(`{"result":{"tabs":[]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-seeded","label":"1"},{"tab_id":"tab-new","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-seeded","tab_id":"tab-seeded"}]}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		{result: execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"pane_not_found"}}`)}},
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	got, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}, "gb-task", `C:\repo`)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	want := Endpoint{Target: Target{Session: "fleet", Pane: "pane-new"}, WorkspaceID: "ws-1", TabID: "tab-new", PaneID: "pane-new"}
	if got != want {
		t.Errorf("CreateTask() = %#v, want %#v", got, want)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "get", "pane-husk", "--session", "fleet"),
		command("herdr", "tab", "close", "tab-husk", "--session", "fleet"),
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "tab", "create", "--workspace", "ws-1", "--cwd", `C:\repo`, "--label", "gb-task", "--no-focus", "--session", "fleet"),
		command("herdr", "tab", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--session", "fleet"),
		command("herdr", "agent", "get", "pane-seeded", "--session", "fleet"),
		command("herdr", "pane", "close", "pane-seeded", "--session", "fleet"),
	})
}

func TestCreateTaskKeepsEndpointWhenOptionalSeedCloseFails(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-seeded","label":"1"},{"tab_id":"tab-new","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-seeded","tab_id":"tab-seeded"}]}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		{result: execx.Result{ExitCode: 1, Stderr: []byte("permission denied")}},
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-new","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-new","tab_id":"tab-new"}]}}`),
		jsonReply(`{"result":{"pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	container := Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}
	endpoint, err := client.CreateTask(context.Background(), container, "gb-task", `C:\repo`)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if endpoint.TabID != "tab-new" || endpoint.PaneID != "pane-new" {
		t.Fatalf("CreateTask endpoint = %#v, want created tab and pane", endpoint)
	}
	if _, err := client.CreateTask(context.Background(), container, "gb-task", `C:\repo`); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("retry error = %v, want duplicate refusal", err)
	}
	createCount := 0
	for _, request := range runner.Requests() {
		if len(request.Args) >= 2 && request.Args[0] == "tab" && request.Args[1] == "create" {
			createCount++
		}
	}
	if createCount != 1 {
		t.Fatalf("tab create count = %d, want one created task tab", createCount)
	}
}

func TestCreateTaskRefusesUnsafeHuskCloseBeforeCreatingTask(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-husk","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-husk","tab_id":"tab-husk"}]}}`),
		{result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}},
		{result: execx.Result{ExitCode: 1, Stderr: []byte("permission denied")}},
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	_, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1"}, "gb-task", `C:\repo`)
	if err == nil || !strings.Contains(err.Error(), "tab close") {
		t.Fatalf("CreateTask error = %v, want unsafe husk-close refusal", err)
	}
	for _, request := range runner.Requests() {
		if len(request.Args) >= 2 && request.Args[0] == "tab" && request.Args[1] == "create" {
			t.Fatalf("CreateTask created a replacement after unsafe husk close: %q", request.Args)
		}
	}
}

func TestCreateTaskDoesNotPruneSeedWhoseLiveLabelChanged(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-seeded","label":"renamed"},{"tab_id":"tab-new","label":"gb-task"}]}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-new","label":"gb-task"}]}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if _, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}, "gb-task", `C:\repo`); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	for _, req := range runner.Requests() {
		if reflect.DeepEqual(req, command("herdr", "pane", "close", "pane-seeded", "--session", "fleet")) {
			t.Fatal("CreateTask closed a seeded tab after its label changed")
		}
	}
}

func TestPaneOperationsUseCorrectPrimitiveAndCaptureTail(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		rawReply("one\ntwo\nthree\nfour\n"),
		rawReply("ansi-output"),
		rawReply(""),
		rawReply(""),
		rawReply(""),
		rawReply(""),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	target := Target{Session: "fleet", Pane: "w1:p2"}

	got, err := client.Capture(context.Background(), target, 2, false)
	if err != nil {
		t.Fatalf("Capture text: %v", err)
	}
	if want := "three\nfour\n"; got != want {
		t.Errorf("Capture text = %q, want %q", got, want)
	}
	if got, err := client.Capture(context.Background(), target, 250, true); err != nil || got != "ansi-output" {
		t.Fatalf("Capture ANSI = %q, %v", got, err)
	}
	if err := client.SendLiteral(context.Background(), target, "hello"); err != nil {
		t.Fatalf("SendLiteral: %v", err)
	}
	for _, key := range []string{"Enter", "Esc", "Ctrl+C"} {
		if err := client.SendKey(context.Background(), target, key); err != nil {
			t.Fatalf("SendKey(%q): %v", key, err)
		}
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "pane", "read", "w1:p2", "--source", "recent", "--lines", "200", "--session", "fleet"),
		command("herdr", "pane", "read", "w1:p2", "--source", "recent", "--lines", "250", "--format", "ansi", "--session", "fleet"),
		command("herdr", "pane", "send-text", "w1:p2", "hello", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "enter", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "escape", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "ctrl+c", "--session", "fleet"),
	})
}

func TestAgentStartAndPromptUseNativeCommands(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{rawReply(""), rawReply(""), rawReply(""), rawReply("")}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	target := Target{Session: "fleet", Pane: "w1:p2"}

	if err := client.AgentStart(context.Background(), target, "gb-task-7", "claude", []string{"--dangerously-skip-permissions"}); err != nil {
		t.Fatalf("AgentStart: %v", err)
	}
	if err := client.AgentPrompt(context.Background(), target, "Read the brief at C:\\briefs\\task.md and follow it exactly."); err != nil {
		t.Fatalf("AgentPrompt: %v", err)
	}
	if err := client.AgentStart(context.Background(), target, "gb-task-8", "kimi", nil); err != nil {
		t.Fatalf("AgentStart without args: %v", err)
	}
	if err := client.AgentStart(context.Background(), Target{Session: "fleet"}, "gb-task-9", "kimi", nil); err == nil {
		t.Fatal("AgentStart without pane returned nil error")
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "agent", "start", "gb-task-7", "--kind", "claude", "--pane", "w1:p2", "--timeout", "120000", "--session", "fleet", "--", "--dangerously-skip-permissions"),
		command("herdr", "agent", "prompt", "w1:p2", "Read the brief at C:\\briefs\\task.md and follow it exactly.", "--session", "fleet"),
		command("herdr", "agent", "start", "gb-task-8", "--kind", "kimi", "--pane", "w1:p2", "--timeout", "120000", "--session", "fleet"),
	})
}

func TestAgentKindsParsesManifestsAndRequiresAtLeastOne(t *testing.T) {
	t.Run("parses advertised kinds", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{
			jsonReply(`{"result":{"manifests":[{"agent":"claude"},{"agent":"codex"},{"agent":"pi"},{"agent":"kimi"}]}}`),
		}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)

		got, err := client.AgentKinds(context.Background())
		if err != nil {
			t.Fatalf("AgentKinds: %v", err)
		}
		want := map[string]bool{"claude": true, "codex": true, "pi": true, "kimi": true}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AgentKinds = %#v, want %#v", got, want)
		}
		assertRequests(t, runner.Requests(), []execx.Request{
			command("herdr", "server", "agent-manifests", "--json", "--session", "fleet"),
		})
	})

	t.Run("refuses empty manifest list", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{jsonReply(`{"result":{"manifests":[]}}`)}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)

		if _, err := client.AgentKinds(context.Background()); err == nil || !strings.Contains(err.Error(), "lists no agents") {
			t.Fatalf("AgentKinds error = %v, want empty-manifest refusal", err)
		}
	})
}

func TestAgentStatusClassifiesLivenessFromJSONRatherThanExitCode(t *testing.T) {
	tests := []struct {
		name      string
		replies   []runnerReply
		want      AgentStatus
		wantError bool
	}{
		{
			name:    "pane not found",
			replies: []runnerReply{{result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}}},
			want:    AgentMissing,
		},
		{
			name:    "pane not found on stderr",
			replies: []runnerReply{{result: execx.Result{Stderr: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}}},
			want:    AgentMissing,
		},
		{
			name: "agent not found",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				{result: execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}},
			},
			want: AgentDead,
		},
		{
			name: "agent not found on stderr",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				{result: execx.Result{Stderr: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}},
			},
			want: AgentDead,
		},
		{
			name:      "malformed pane JSON",
			replies:   []runnerReply{jsonReply(`{`)},
			want:      AgentUnreadable,
			wantError: true,
		},
		{
			name: "working agent",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
			},
			want: AgentAlive,
		},
		{
			name: "idle agent",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
			},
			want: AgentAlive,
		},
		{
			name: "done agent",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"done"}}}`),
			},
			want: AgentAlive,
		},
		{
			name: "blocked agent",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`),
			},
			want: AgentAlive,
		},
		{
			name: "unknown agent is still registered",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"unknown"}}}`),
			},
			want: AgentAlive,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: test.replies}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)
			got, err := client.AgentStatus(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})
			if (err != nil) != test.wantError {
				t.Fatalf("AgentStatus error = %v, want error = %t", err, test.wantError)
			}
			if got != test.want {
				t.Errorf("AgentStatus = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAgentDetailRequiresExactAgentIdentity(t *testing.T) {
	t.Run("returns typed identity and status", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent":"pi","agent_status":"idle"}}}`)}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)

		got, err := client.AgentDetail(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})
		if err != nil {
			t.Fatalf("AgentDetail: %v", err)
		}
		if got != (AgentDetail{Agent: "pi", Status: "idle"}) {
			t.Errorf("AgentDetail = %#v, want pi idle", got)
		}
		assertRequests(t, runner.Requests(), []execx.Request{
			command("herdr", "agent", "get", "w1:p2", "--session", "fleet"),
		})
	})

	t.Run("refuses missing identity", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`)}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)

		if _, err := client.AgentDetail(context.Background(), Target{Session: "fleet", Pane: "w1:p2"}); err == nil {
			t.Fatal("AgentDetail accepted a response without an exact identity")
		}
	})
}

func TestBusyStateAndWaitForWorking(t *testing.T) {
	t.Run("blocked is idle for watcher liveness", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`)}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)
		got, err := client.BusyState(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})
		if err != nil || got != BusyIdle {
			t.Fatalf("BusyState = %q, %v; want idle", got, err)
		}
	})

	t.Run("unknown registered agent is unknown but readable", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent_status":"unknown"}}}`)}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)
		got, err := client.BusyState(context.Background(), Target{Session: "fleet", Pane: "w1:p2"})
		if err != nil || got != BusyUnknown {
			t.Fatalf("BusyState = %q, %v; want unknown with nil error", got, err)
		}
	})

	tests := []struct {
		name       string
		replies    []runnerReply
		want       SubmitState
		wantSleeps []time.Duration
	}{
		{
			name:    "working returns immediately",
			replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`)},
			want:    SubmitWorking,
		},
		{
			name: "all idle reads return idle",
			replies: []runnerReply{
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"done"}}}`),
			},
			want:       SubmitIdle,
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name:    "blocked remains distinct for submit confirmation",
			replies: []runnerReply{jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`)},
			want:    SubmitBlocked,
		},
		{
			name: "every unreadable read returns unknown",
			replies: []runnerReply{
				jsonReply(`{`),
				jsonReply(`{`),
				jsonReply(`{`),
			},
			want:       SubmitUnknown,
			wantSleeps: []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
		},
		{
			name: "mixed idle and unreadable reads stay pending",
			replies: []runnerReply{
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
				jsonReply(`{`),
			},
			want:       SubmitPending,
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name: "all unknown reads stay pending",
			replies: []runnerReply{
				jsonReply(`{"result":{"agent":{"agent_status":"unknown"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"unknown"}}}`),
			},
			want:       SubmitPending,
			wantSleeps: []time.Duration{time.Second},
		},
		{
			name: "mixed unknown and idle reads stay pending",
			replies: []runnerReply{
				jsonReply(`{"result":{"agent":{"agent_status":"unknown"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
			},
			want:       SubmitPending,
			wantSleeps: []time.Duration{time.Second},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeRunner{replies: test.replies}
			var sleeps []time.Duration
			client := newTestClient(runner, &sleeps)
			got, err := client.WaitForWorking(context.Background(), Target{Session: "fleet", Pane: "w1:p2"}, time.Second, len(test.replies))
			if err != nil {
				t.Fatalf("WaitForWorking: %v", err)
			}
			if got != test.want {
				t.Errorf("WaitForWorking = %q, want %q", got, test.want)
			}
			if !reflect.DeepEqual(sleeps, test.wantSleeps) {
				t.Errorf("sleeps = %v, want %v", sleeps, test.wantSleeps)
			}
		})
	}

	t.Run("cancelled context is actionable", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var sleeps []time.Duration
		client := newTestClient(&fakeRunner{}, &sleeps)
		if _, err := client.WaitForWorking(ctx, Target{Session: "fleet", Pane: "w1:p2"}, time.Second, 1); !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForWorking error = %v, want context cancellation", err)
		}
	})

	t.Run("runner start failure is actionable", func(t *testing.T) {
		runner := &fakeRunner{replies: []runnerReply{{err: &exec.Error{Name: "herdr", Err: errors.New("not found")}}}}
		var sleeps []time.Duration
		client := newTestClient(runner, &sleeps)
		if _, err := client.WaitForWorking(context.Background(), Target{Session: "fleet", Pane: "w1:p2"}, time.Second, 1); err == nil {
			t.Fatal("WaitForWorking returned nil for a runner start failure")
		}
	})

	t.Run("empty pane is an actionable local request error", func(t *testing.T) {
		var sleeps []time.Duration
		client := newTestClient(&fakeRunner{}, &sleeps)
		if _, err := client.WaitForWorking(context.Background(), Target{Session: "fleet"}, time.Second, 1); err == nil {
			t.Fatal("WaitForWorking returned nil for an empty target pane")
		}
	})
}

func TestCommandFailuresPreserveOperationTargetAndStderr(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{{result: execx.Result{ExitCode: 7, Stderr: []byte("permission denied")}}}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	target := Target{Session: "fleet", Pane: "w1:p2"}

	err := client.SendLiteral(context.Background(), target, "hello")
	if err == nil {
		t.Fatal("SendLiteral returned nil for failed command")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("SendLiteral error = %T %v, want CommandError", err, err)
	}
	if commandErr.Operation != "pane send-text" || commandErr.Target != target || commandErr.Stderr != "permission denied" {
		t.Errorf("CommandError = %#v, want operation, target, and stderr", commandErr)
	}
}
