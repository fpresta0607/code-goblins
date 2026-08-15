package monitor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// proberRunner fakes the read-only Herdr CLI surface the structural prober
// uses: one local schema document, session snapshots, and bounded pane reads.
type proberRunner struct {
	snapshotBodies []string
	snapshotErr    bool
	schemaErr      bool
	captureText    map[string]string
	captureErr     map[string]bool
	schemaCalls    int
	snapshotCalls  int
	captureCalls   map[string]int
	requests       []execx.Request
}

func (r *proberRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.requests = append(r.requests, req)
	if req.Name != "herdr" || len(req.Args) < 2 || req.Args[len(req.Args)-2] != "--session" || req.Args[len(req.Args)-1] != "fleet" {
		return execx.Result{}, fmt.Errorf("unexpected request: %#v", req)
	}
	args := req.Args[:len(req.Args)-2]
	switch {
	case len(args) == 3 && args[0] == "api" && args[1] == "schema" && args[2] == "--json":
		r.schemaCalls++
		if r.schemaErr {
			return execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"server_unavailable"}}`)}, nil
		}
		return execx.Result{Stdout: []byte(proberSchemaJSON())}, nil
	case len(args) == 2 && args[0] == "api" && args[1] == "snapshot":
		r.snapshotCalls++
		if r.snapshotErr {
			return execx.Result{ExitCode: 1, Stderr: []byte(`{"error":{"code":"server_unavailable"}}`)}, nil
		}
		if len(r.snapshotBodies) == 0 {
			return execx.Result{}, fmt.Errorf("unexpected extra api snapshot")
		}
		body := r.snapshotBodies[0]
		r.snapshotBodies = r.snapshotBodies[1:]
		return execx.Result{Stdout: []byte(body)}, nil
	case len(args) == 7 && args[0] == "pane" && args[1] == "read":
		if args[3] != "--source" || args[4] != "recent-unwrapped" || args[5] != "--lines" || args[6] != "200" {
			return execx.Result{}, fmt.Errorf("pane read is not the bounded unwrapped capture: %v", args)
		}
		if r.captureCalls == nil {
			r.captureCalls = make(map[string]int)
		}
		r.captureCalls[args[2]]++
		if r.captureErr[args[2]] {
			return execx.Result{ExitCode: 1, Stderr: []byte("read failed")}, nil
		}
		return execx.Result{Stdout: []byte(r.captureText[args[2]])}, nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected Herdr args: %q", args)
	}
}

func proberSchemaJSON() string {
	var b strings.Builder
	b.WriteString(`{"protocol":19,"schema_version":1,"schemas":{"success_response":{},"error_response":{},"request":{"oneOf":[`)
	methods := []string{
		"server.agent_manifests",
		"session.snapshot",
		"workspace.create",
		"workspace.list",
		"tab.close",
		"tab.create",
		"tab.list",
		"agent.get",
		"agent.prompt",
		"agent.start",
		"pane.close",
		"pane.get",
		"pane.list",
		"pane.read",
		"pane.send_keys",
		"pane.send_text",
	}
	for i, method := range methods {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"properties":{"method":{"const":%q}}}`, method)
	}
	b.WriteString(`]}}}`)
	return b.String()
}

// proberSnapshotEnvelope renders the typed session.snapshot envelope from the
// given per-task activity states.
func proberSnapshotEnvelope(metas []state.TaskMeta, statuses map[string]string) string {
	var b strings.Builder
	b.WriteString(`{"id":"cli:api:snapshot","result":{"type":"session_snapshot","snapshot":{"version":"0.8.0-test","protocol":19,`)
	b.WriteString(`"workspaces":[{"workspace_id":"ws","label":"cfo"}],"tabs":[`)
	for i, meta := range metas {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"tab_id":%q,"workspace_id":"ws","label":%q}`, meta.HerdrTabID, "gb-"+meta.ID)
	}
	b.WriteString(`],"panes":[`)
	for i, meta := range metas {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"pane_id":%q,"tab_id":%q,"workspace_id":"ws"}`, meta.HerdrPaneID, meta.HerdrTabID)
	}
	b.WriteString(`],"agents":[`)
	first := true
	for _, meta := range metas {
		status, ok := statuses[meta.ID]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, `{"pane_id":%q,"tab_id":%q,"workspace_id":"ws","agent":"claude","agent_status":%q}`, meta.HerdrPaneID, meta.HerdrTabID, status)
	}
	b.WriteString(`]}}}`)
	return b.String()
}

func proberClient(runner *proberRunner) *herdr.Client {
	return &herdr.Client{Commands: runner, Session: "fleet"}
}

func TestHerdrProberSuppliesOneSnapshotPerCycle(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	metas := []state.TaskMeta{metaFor("g1"), metaFor("g2")}
	for _, meta := range metas {
		writeTask(t, stateDir, meta)
	}
	runner := &proberRunner{
		snapshotBodies: []string{
			proberSnapshotEnvelope(metas, map[string]string{"g1": "working", "g2": "idle"}),
			proberSnapshotEnvelope(metas, map[string]string{"g1": "working", "g2": "idle"}),
		},
		captureText: map[string]string{
			"pane-g1": string(capture("g1 text")),
			"pane-g2": string(capture("g2 text")),
		},
	}
	service := testService(stateDir, NewHerdrProber(proberClient(runner)), &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 2 {
		t.Fatalf("observations = %+v, want both tasks", result.Observations)
	}
	for _, observation := range result.Observations {
		if observation.Health != HealthActive || observation.EndpointVerdict != ProbePresent {
			t.Errorf("observation = %+v, want active with a present endpoint", observation)
		}
	}
	if runner.schemaCalls != 1 || runner.snapshotCalls != 1 {
		t.Fatalf("schema=%d snapshots=%d, want one of each for the whole cycle", runner.schemaCalls, runner.snapshotCalls)
	}
	if runner.captureCalls["pane-g1"] != 1 || runner.captureCalls["pane-g2"] != 1 {
		t.Fatalf("captures = %v, want exactly one bounded read per valid task", runner.captureCalls)
	}

	now = now.Add(time.Minute)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if runner.schemaCalls != 1 {
		t.Fatalf("schema=%d after two cycles, want the immutable check cached for the process", runner.schemaCalls)
	}
	if runner.snapshotCalls != 2 {
		t.Fatalf("snapshots=%d after two cycles, want fresh topology per cycle", runner.snapshotCalls)
	}

	for _, request := range runner.requests {
		for _, argument := range request.Args {
			switch argument {
			case "close", "delete", "return", "restart", "send-text", "send-keys":
				t.Fatalf("prober issued a lifecycle command: %q", request.Args)
			}
		}
		if len(request.Args) >= 2 && request.Args[0] == "pane" && request.Args[1] == "read" {
			continue
		}
		if len(request.Args) >= 2 && request.Args[0] == "api" && (request.Args[1] == "schema" || request.Args[1] == "snapshot") {
			continue
		}
		t.Fatalf("prober issued an unexpected command: %q", request.Args)
	}
}

func TestHerdrProberRetainsBusyProtection(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	runner := &proberRunner{
		snapshotBodies: []string{
			proberSnapshotEnvelope([]state.TaskMeta{meta}, map[string]string{"g1": "working"}),
			proberSnapshotEnvelope([]state.TaskMeta{meta}, map[string]string{"g1": "working"}),
		},
		captureText: map[string]string{"pane-g1": string(capture("steady"))},
	}
	service := testService(stateDir, NewHerdrProber(proberClient(runner)), &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	now = now.Add(time.Minute)
	second, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	observation := second.Observations[0]
	if first.Observations[0].Health != HealthActive || observation.Health != HealthBusy || second.Event != nil {
		t.Fatalf("healths = %q then %q event=%+v, want active then protected busy", first.Observations[0].Health, observation.Health, second.Event)
	}
	if runner.captureCalls["pane-g1"] != 2 {
		t.Fatalf("captures = %v, want one bounded read per cycle", runner.captureCalls)
	}
}

func TestHerdrProberStructuralVerdicts(t *testing.T) {
	meta := metaFor("g1")
	paneEntry := fmt.Sprintf(`{"pane_id":%q,"tab_id":%q,"workspace_id":"ws"}`, meta.HerdrPaneID, meta.HerdrTabID)
	agent := func(status string) string {
		return fmt.Sprintf(`{"pane_id":%q,"tab_id":%q,"workspace_id":"ws","agent":"claude","agent_status":%q}`, meta.HerdrPaneID, meta.HerdrTabID, status)
	}
	envelope := func(tabs, panes, agents string) string {
		return `{"result":{"type":"session_snapshot","snapshot":{"protocol":19,"workspaces":[{"workspace_id":"ws","label":"cfo"}],"tabs":[` + tabs + `],"panes":[` + panes + `],"agents":[` + agents + `]}}}`
	}
	tabEntry := fmt.Sprintf(`{"tab_id":%q,"workspace_id":"ws","label":"gb-g1"}`, meta.HerdrTabID)

	tests := []struct {
		name        string
		body        string
		wantVerdict ProbeVerdict
		wantAgent   herdr.AgentStatus
		wantBusy    herdr.BusyState
		wantDetail  string
	}{
		{
			name:        "missing pane is a typed missing verdict",
			body:        envelope(tabEntry, "", ""),
			wantVerdict: ProbeMissing,
			wantDetail:  "absent from the session snapshot",
		},
		{
			name:        "duplicate pane identity is unknown",
			body:        envelope(tabEntry, paneEntry+","+paneEntry, agent("idle")),
			wantVerdict: ProbeUnknown,
			wantDetail:  "copies of pane",
		},
		{
			name:        "cross-linked tab is unknown",
			body:        envelope(`{"tab_id":"`+meta.HerdrTabID+`","workspace_id":"other","label":"gb-g1"}`, paneEntry, agent("idle")),
			wantVerdict: ProbeUnknown,
			wantDetail:  "belongs to workspace other",
		},
		{
			name:        "wrong tab label is unknown",
			body:        envelope(`{"tab_id":"`+meta.HerdrTabID+`","workspace_id":"ws","label":"gb-other"}`, paneEntry, agent("idle")),
			wantVerdict: ProbeUnknown,
			wantDetail:  "label",
		},
		{
			name:        "pane without a registered agent is present but dead",
			body:        envelope(tabEntry, paneEntry, ""),
			wantVerdict: ProbePresent,
			wantAgent:   herdr.AgentDead,
			wantBusy:    herdr.BusyUnknown,
			wantDetail:  "no registered agent",
		},
		{
			name:        "working maps to busy",
			body:        envelope(tabEntry, paneEntry, agent("working")),
			wantVerdict: ProbePresent,
			wantAgent:   herdr.AgentAlive,
			wantBusy:    herdr.BusyWorking,
		},
		{
			name:        "done maps to idle",
			body:        envelope(tabEntry, paneEntry, agent("done")),
			wantVerdict: ProbePresent,
			wantAgent:   herdr.AgentAlive,
			wantBusy:    herdr.BusyIdle,
		},
		{
			name:        "blocked maps to idle",
			body:        envelope(tabEntry, paneEntry, agent("blocked")),
			wantVerdict: ProbePresent,
			wantAgent:   herdr.AgentAlive,
			wantBusy:    herdr.BusyIdle,
		},
		{
			name:        "unrecognized status is unknown activity",
			body:        envelope(tabEntry, paneEntry, agent("sleeping")),
			wantVerdict: ProbePresent,
			wantAgent:   herdr.AgentAlive,
			wantBusy:    herdr.BusyUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &proberRunner{
				snapshotBodies: []string{test.body},
				captureText:    map[string]string{meta.HerdrPaneID: string(capture("text"))},
			}
			prober := NewHerdrProber(proberClient(runner))

			sample, err := prober.Inspect(context.Background(), meta)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if sample.Verdict != test.wantVerdict || sample.Agent != test.wantAgent || sample.Busy != test.wantBusy {
				t.Fatalf("sample = %+v, want verdict=%s agent=%s busy=%s", sample, test.wantVerdict, test.wantAgent, test.wantBusy)
			}
			if test.wantDetail != "" && !strings.Contains(sample.Detail, test.wantDetail) {
				t.Errorf("detail = %q, want %q", sample.Detail, test.wantDetail)
			}
			if sample.Verdict == ProbePresent && sample.Agent == herdr.AgentAlive {
				if len(sample.Capture) == 0 || runner.captureCalls[meta.HerdrPaneID] != 1 {
					t.Errorf("valid task got %d captures, want exactly one bounded read", runner.captureCalls[meta.HerdrPaneID])
				}
			} else if runner.captureCalls[meta.HerdrPaneID] != 0 {
				t.Errorf("invalid task got %d captures, want none", runner.captureCalls[meta.HerdrPaneID])
			}
		})
	}
}

func TestHerdrProberAcceptsShortCaptureFromLiveTask(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	runner := &proberRunner{
		snapshotBodies: []string{proberSnapshotEnvelope([]state.TaskMeta{meta}, map[string]string{"g1": "working"})},
		captureText:    map[string]string{meta.HerdrPaneID: "claude is running\n"},
	}
	service := testService(stateDir, NewHerdrProber(proberClient(runner)), &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %+v, want one task", result.Observations)
	}
	observation := result.Observations[0]
	if observation.Health != HealthActive || observation.EndpointVerdict != ProbePresent {
		t.Fatalf("observation = %+v, want active with present endpoint for a short live capture", observation)
	}
}

func TestHerdrProberUnreadableCaptureIsUnknown(t *testing.T) {
	meta := metaFor("g1")
	runner := &proberRunner{
		snapshotBodies: []string{proberSnapshotEnvelope([]state.TaskMeta{meta}, map[string]string{"g1": "working"})},
		captureErr:     map[string]bool{meta.HerdrPaneID: true},
	}
	prober := NewHerdrProber(proberClient(runner))

	sample, err := prober.Inspect(context.Background(), meta)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if sample.Verdict != ProbeUnknown || !strings.Contains(sample.Detail, "capture is unreadable") {
		t.Fatalf("sample = %+v, want unknown with unreadable capture detail", sample)
	}
}

func TestHerdrProberUnreadableSessionIsDurableUnknown(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	runner := &proberRunner{snapshotErr: true}
	service := testService(stateDir, NewHerdrProber(proberClient(runner)), &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %+v, want one unknown observation", result.Observations)
	}
	observation := result.Observations[0]
	if observation.Health != HealthUnknown || observation.Reason != EndpointUnknown || observation.EndpointVerdict != ProbeUnknown {
		t.Fatalf("observation = %+v, want durable unknown evidence", observation)
	}
	if result.Event == nil {
		t.Fatal("event = nil, want a durable wake for the unreadable session")
	}
	persisted, err := ReadObservation(stateDir, "g1")
	if err != nil {
		t.Fatalf("ReadObservation: %v", err)
	}
	if persisted.Health != HealthUnknown || persisted.PendingEvent == nil {
		t.Fatalf("persisted observation = %+v, want unknown with a pending event", persisted)
	}
	if len(runner.captureCalls) != 0 {
		t.Fatalf("captures = %v, want no reads against an unreadable session", runner.captureCalls)
	}
}

func TestHerdrProberSessionMismatchIsUnknown(t *testing.T) {
	meta := metaFor("g1")
	runner := &proberRunner{
		snapshotBodies: []string{proberSnapshotEnvelope([]state.TaskMeta{meta}, map[string]string{"g1": "idle"})},
		captureText:    map[string]string{meta.HerdrPaneID: string(capture("text"))},
	}
	prober := NewHerdrProber(proberClient(runner))

	drifted := meta
	drifted.HerdrSession = "other"
	sample, err := prober.Inspect(context.Background(), drifted)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if sample.Verdict != ProbeUnknown || !strings.Contains(sample.Detail, "does not match prober session") {
		t.Fatalf("sample = %+v, want unknown for a drifted session", sample)
	}
	if runner.captureCalls[meta.HerdrPaneID] != 0 {
		t.Fatalf("session drift got %d captures, want none", runner.captureCalls[meta.HerdrPaneID])
	}
}
