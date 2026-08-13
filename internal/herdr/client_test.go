package herdr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
		if len(request.Args) < 2 || request.Args[len(request.Args)-2] != "--session" || request.Args[len(request.Args)-1] != "fleet" {
			t.Errorf("request %q does not end in explicit fleet session routing", request.Args)
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
	if got, want := sleeps, []time.Duration{500 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Errorf("sleeps = %v, want %v", got, want)
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
		command("herdr", "workspace", "list", "--json", "--session", "fleet"),
		command("herdr", "workspace", "create", "--cwd", `C:\repo`, "--label", "firstmate", "--no-focus", "--json", "--session", "fleet"),
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
			body: `{"result":{"workspaces":[{"workspace_id":"ws-other","label":"other"},{"workspace_id":"ws-1","label":"firstmate"}]}}`,
			want: Container{Session: "fleet", WorkspaceID: "ws-1"},
		},
		{
			name:    "matching workspaces are ambiguous",
			body:    `{"result":{"workspaces":[{"workspace_id":"ws-1","label":"firstmate"},{"workspace_id":"ws-2","label":"firstmate"}]}}`,
			wantErr: true,
		},
		{
			name:    "missing workspace id is rejected",
			body:    `{"result":{"workspaces":[{"label":"firstmate"}]}}`,
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
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-existing","label":"fm-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-existing","tab_id":"tab-existing"}]}}`),
		jsonReply(`{"result":{"pane":{"pane_id":"pane-existing"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	_, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1"}, "fm-task", `C:\repo`)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("CreateTask error = %v, want duplicate refusal", err)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "pane", "get", "pane-existing", "--json", "--session", "fleet"),
		command("herdr", "agent", "get", "pane-existing", "--json", "--session", "fleet"),
	})
}

func TestCreateTaskReplacesHuskAndPrunesOnlyExactSafeSeed(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-husk","label":"fm-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-husk","tab_id":"tab-husk"}]}}`),
		{result: execx.Result{Stdout: []byte(`{"error":{"code":"pane_not_found"}}`), ExitCode: 1}},
		jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-seeded","label":"1"},{"tab_id":"tab-husk","label":"fm-task"},{"tab_id":"tab-new","label":"fm-task"}]}}`),
		jsonReply(`{"result":{"panes":[{"pane_id":"pane-seeded","tab_id":"tab-seeded"}]}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"idle"}}}`),
		{result: execx.Result{ExitCode: 1, Stderr: []byte("already closed")}},
		rawReply(""),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-new","label":"fm-task"}]}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	got, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}, "fm-task", `C:\repo`)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	want := Endpoint{Target: Target{Session: "fleet", Pane: "pane-new"}, WorkspaceID: "ws-1", TabID: "tab-new", PaneID: "pane-new"}
	if got != want {
		t.Errorf("CreateTask() = %#v, want %#v", got, want)
	}
	assertRequests(t, runner.Requests(), []execx.Request{
		command("herdr", "tab", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "pane", "get", "pane-husk", "--json", "--session", "fleet"),
		command("herdr", "tab", "create", "--workspace", "ws-1", "--cwd", `C:\repo`, "--label", "fm-task", "--no-focus", "--json", "--session", "fleet"),
		command("herdr", "tab", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "pane", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
		command("herdr", "agent", "get", "pane-seeded", "--json", "--session", "fleet"),
		command("herdr", "pane", "close", "pane-seeded", "--session", "fleet"),
		command("herdr", "tab", "close", "tab-husk", "--session", "fleet"),
		command("herdr", "tab", "list", "--workspace", "ws-1", "--json", "--session", "fleet"),
	})
}

func TestCreateTaskDoesNotPruneSeedWhoseLiveLabelChanged(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"tabs":[]}}`),
		jsonReply(`{"result":{"tab":{"tab_id":"tab-new"},"root_pane":{"pane_id":"pane-new"}}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-seeded","label":"renamed"},{"tab_id":"tab-new","label":"fm-task"}]}}`),
		jsonReply(`{"result":{"tabs":[{"tab_id":"tab-new","label":"fm-task"}]}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)

	if _, err := client.CreateTask(context.Background(), Container{Session: "fleet", WorkspaceID: "ws-1", SeededDefaultTab: "tab-seeded"}, "fm-task", `C:\repo`); err != nil {
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
	if err := client.RunPane(context.Background(), target, "treehouse get"); err != nil {
		t.Fatalf("RunPane: %v", err)
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
		command("herdr", "pane", "run", "w1:p2", "treehouse get", "--session", "fleet"),
		command("herdr", "pane", "send-text", "w1:p2", "hello", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "enter", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "escape", "--session", "fleet"),
		command("herdr", "pane", "send-keys", "w1:p2", "ctrl+c", "--session", "fleet"),
	})
}

func TestForegroundCWDAndPaneAdapter(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"pane":{"foreground_cwd":"C:\\worktree"}}}`),
		rawReply(""),
		jsonReply(`{"result":{"pane":{"foreground_cwd":"C:\\worktree"}}}`),
	}}
	var sleeps []time.Duration
	client := newTestClient(runner, &sleeps)
	target := Target{Session: "fleet", Pane: "w1:p2"}

	if got, err := client.ForegroundCWD(context.Background(), target); err != nil || got != `C:\worktree` {
		t.Fatalf("ForegroundCWD = %q, %v", got, err)
	}
	pane := Pane{Client: client, Target: target}
	if err := pane.Run(context.Background(), "treehouse get"); err != nil {
		t.Fatalf("Pane.Run: %v", err)
	}
	if got, err := pane.ForegroundCWD(context.Background()); err != nil || got != `C:\worktree` {
		t.Fatalf("Pane.ForegroundCWD = %q, %v", got, err)
	}
	if got, want := pane.Target.String(), "fleet:w1:p2"; got != want {
		t.Errorf("Pane target = %q, want %q", got, want)
	}
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
			name: "agent not found",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				{result: execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`), ExitCode: 1}},
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
			name: "unknown agent status",
			replies: []runnerReply{
				jsonReply(`{"result":{"pane":{"pane_id":"w1:p2"}}}`),
				jsonReply(`{"result":{"agent":{"agent_status":"starting"}}}`),
			},
			want:      AgentUnreadable,
			wantError: true,
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
				jsonReply(`{"result":{"agent":{"agent_status":"blocked"}}}`),
			},
			want:       SubmitIdle,
			wantSleeps: []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
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
