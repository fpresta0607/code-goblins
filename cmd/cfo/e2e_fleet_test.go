package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// TestFleetEndToEnd covers the public fleet command flow without reaching an
// installed Herdr server, treehouse pool, harness binary, or credential.
// The fixture is intentionally introduced after this test so this test first
// proves the exact end-to-end behavior the fake transport must support.
func TestFleetEndToEnd(t *testing.T) {
	fixture := newFleetE2EFixture(t)

	for _, harness := range []string{"claude", "codex", "pi"} {
		fixture.Spawn(harness)
	}
	fixture.AssertTaskMetadataIsIsolated()
	fixture.SendAndPeek("claude")

	fixture.ScanActive("claude")
	fixture.ScanBusyProtected("claude")
	fixture.ScanStaleEscalation("claude")
	fixture.AssertHeartbeatPersistsAcrossRestart()
	fixture.AssertDurableWakesAndDeepInspection("claude")
	fixture.ScanUnknownEndpoint("codex")
	fixture.AssertFleetJSONAndMarkdownParity()
	fixture.AssertVisibleTabsAndNoLifecycleDeletes()
}

// fleetE2EFixture is a real command-path fixture. It drives the command
// parser and the spawn, send, peek, monitor, wake, and fleet packages while
// replacing only subprocesses with an in-memory Herdr and treehouse model.
// No installed tool, network service, credential, or production checkout is
// reachable from this test.
type fleetE2EFixture struct {
	t        *testing.T
	home     home.Home
	project  string
	briefs   map[string]string
	now      time.Time
	runner   *fleetE2ERunner
	git      *fleetE2EGit
	client   *herdr.Client
	runtime  commandRuntime
	prober   *fleetE2EProber
	monitors int
}

func newFleetE2EFixture(t *testing.T) *fleetE2EFixture {
	t.Helper()
	root := t.TempDir()
	h := home.Home{
		Root:  filepath.Join(root, "home"),
		State: filepath.Join(root, "home", "state"),
		Data:  filepath.Join(root, "home", "data"),
	}
	for _, path := range []string{h.Root, h.State, h.Data} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(root, "disposable-project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	fixture := &fleetE2EFixture{
		t:       t,
		home:    h,
		project: project,
		briefs:  make(map[string]string),
		now:     time.Now().UTC().Truncate(time.Second),
	}
	fixture.runner = &fleetE2ERunner{
		fixture:   fixture,
		tabs:      make(map[string]fleetE2ETab),
		worktrees: make(map[string]string),
		busy:      make(map[string]herdr.BusyState),
		missing:   make(map[string]bool),
	}
	fixture.git = &fleetE2EGit{fixture: fixture}
	fixture.client = &herdr.Client{
		Commands: fixture.runner,
		Session:  "fleet-e2e",
		Sleep:    noWait,
	}
	fixture.prober = &fleetE2EProber{fixture: fixture, calls: make(map[string]int)}
	fixture.runtime = commandRuntime{
		resolveHome: func() (home.Home, error) { return fixture.home, nil },
		spawn:       fixture.spawn,
		sendText:    fixture.sendText,
		sendKey:     fixture.sendKey,
		peek:        fixture.peek,
		snapshot:    fixture.snapshot,
	}
	// cfo drain intentionally resolves its own home. This test never runs in
	// parallel, so setting the test-local environment is safe and lets the
	// command's normal code path render the durable wake queue.
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("HERDR_SESSION", "fleet-e2e")
	return fixture
}

func (f *fleetE2EFixture) Spawn(harnessName string) {
	f.t.Helper()
	brief := filepath.Join(f.home.Root, harnessName+".brief.md")
	if err := os.WriteFile(brief, []byte("Delivery contract: mode=local-only\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.briefs[harnessName] = brief
	stdout, stderr := runFleetCommand(f.t, f.runtime, "spawn", harnessName,
		"--project", f.project,
		"--brief", brief,
		"--harness", harnessName,
		"--mode", "local-only")
	if !strings.Contains(stdout, "spawned "+harnessName+" ") || stderr != "" {
		f.t.Fatalf("spawn %s stdout=%q stderr=%q", harnessName, stdout, stderr)
	}
	if err := state.AppendStatus(f.home.State, harnessName, "working: deterministic e2e fixture"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleetE2EFixture) AssertTaskMetadataIsIsolated() {
	f.t.Helper()
	for _, id := range []string{"claude", "codex", "pi"} {
		meta, err := state.ReadTaskMeta(f.home.State, id)
		if err != nil {
			f.t.Fatal(err)
		}
		if meta.Project != f.project || meta.Worktree == "" || samePath(meta.Worktree, f.project) {
			f.t.Fatalf("%s metadata is not an isolated worker: %+v", id, meta)
		}
		if info, err := os.Stat(meta.Worktree); err != nil || !info.IsDir() {
			f.t.Fatalf("%s worktree missing after spawn: %v", id, err)
		}
		if meta.Backend != "herdr" || meta.HerdrSession != "fleet-e2e" || meta.HerdrPaneID == "" {
			f.t.Fatalf("%s Herdr metadata is incomplete: %+v", id, meta)
		}
		target, err := herdr.ParseTarget(meta.Window)
		if err != nil || target.Session != "fleet-e2e" || target.Pane != meta.HerdrPaneID {
			f.t.Fatalf("%s window=%q parsed target=%+v err=%v", id, meta.Window, target, err)
		}
		if !strings.Contains(target.Pane, ":") {
			f.t.Fatalf("%s pane %q does not prove first-colon target parsing", id, target.Pane)
		}
		if _, exists := f.runner.tabs["fm-"+id]; !exists {
			f.t.Fatalf("fake workspace is missing visible fm-%s tab", id)
		}
	}
	if len(f.git.freshened) != 3 {
		f.t.Fatalf("treehouse freshen calls=%v, want one per spawned task", f.git.freshened)
	}

	// The real cleanup command is not in this task's scope. This direct
	// service boundary proves an ambiguous return is refused and cannot remove
	// the primary checkout while the test keeps every worker alive.
	err := (treehouse.Service{Git: f.git}).Return(context.Background(), f.project, f.project)
	if err == nil {
		f.t.Fatal("ambiguous treehouse return unexpectedly succeeded")
	}
	if info, statErr := os.Stat(f.project); statErr != nil || !info.IsDir() {
		f.t.Fatalf("ambiguous return changed primary project: %v", statErr)
	}
}

func (f *fleetE2EFixture) SendAndPeek(id string) {
	f.t.Helper()
	stdout, stderr := runFleetCommand(f.t, f.runtime, "send", "fm-"+id, "print", "the", "acceptance", "marker")
	if stdout != "sent fm-"+id+"\n" || stderr != "" {
		f.t.Fatalf("send stdout=%q stderr=%q", stdout, stderr)
	}
	if got := f.runner.lastText; got != "print the acceptance marker" {
		f.t.Fatalf("sent text=%q", got)
	}

	stdout, stderr = runFleetCommand(f.t, f.runtime, "peek", "fm-"+id, "5")
	if stderr != "" || !strings.Contains(stdout, "acceptance marker") {
		f.t.Fatalf("peek stdout=%q stderr=%q", stdout, stderr)
	}
}

func (f *fleetE2EFixture) ScanActive(id string) {
	f.t.Helper()
	result := f.scan()
	observation := observationFor(f.t, result.Observations, id)
	if observation.Health != monitor.HealthActive || result.Event != nil {
		f.t.Fatalf("first scan observation=%+v event=%+v, want active with no event", observation, result.Event)
	}
}

func (f *fleetE2EFixture) ScanBusyProtected(id string) {
	f.t.Helper()
	f.now = f.now.Add(time.Minute)
	f.runner.busy[id] = herdr.BusyWorking
	result := f.scan()
	observation := observationFor(f.t, result.Observations, id)
	if observation.Health != monitor.HealthBusy || observation.StaleSince != nil || result.Event != nil {
		f.t.Fatalf("busy scan observation=%+v event=%+v, want protected busy", observation, result.Event)
	}
	if result.Heartbeat.NoChangeStreak != 1 || !result.Heartbeat.NextDue.After(f.now) {
		f.t.Fatalf("busy heartbeat=%+v, want persisted backoff", result.Heartbeat)
	}
}

func (f *fleetE2EFixture) ScanStaleEscalation(id string) {
	f.t.Helper()
	f.runner.busy[id] = herdr.BusyIdle
	f.now = f.now.Add(time.Second)
	first := f.scan()
	firstObservation := observationFor(f.t, first.Observations, id)
	if first.Event == nil || first.Event.Kind != "stale" || firstObservation.Health != monitor.HealthStale || firstObservation.Escalation != 0 {
		f.t.Fatalf("first stale scan observation=%+v event=%+v", firstObservation, first.Event)
	}
	f.publish(*first.Event)

	f.now = f.now.Add(time.Minute)
	escalated := f.scan()
	observation := observationFor(f.t, escalated.Observations, id)
	if escalated.Event == nil || observation.Escalation != 1 || observation.DemandDeepInspection {
		f.t.Fatalf("first escalation observation=%+v event=%+v", observation, escalated.Event)
	}
	f.publish(*escalated.Event)
}

func (f *fleetE2EFixture) AssertHeartbeatPersistsAcrossRestart() {
	f.t.Helper()
	before, err := monitor.ReadHeartbeat(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	f.monitors++
	after, err := monitor.ReadHeartbeat(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	if before != after || before.LastCycle.IsZero() || before.NextDue.IsZero() {
		f.t.Fatalf("restart heartbeat changed before=%+v after=%+v", before, after)
	}
}

func (f *fleetE2EFixture) AssertDurableWakesAndDeepInspection(id string) {
	f.t.Helper()
	heartbeat, err := monitor.ReadHeartbeat(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	f.now = heartbeat.NextDue
	heartbeatResult := f.scan()
	if heartbeatResult.Event == nil || heartbeatResult.Event.Source != monitor.HeartbeatEvent {
		f.t.Fatalf("heartbeat scan event=%+v, want durable heartbeat", heartbeatResult.Event)
	}
	f.publish(*heartbeatResult.Event)

	f.now = f.now.Add(time.Second)
	deep := f.scan()
	observation := observationFor(f.t, deep.Observations, id)
	if deep.Event == nil || observation.Escalation != 2 || !observation.DemandDeepInspection || !strings.Contains(deep.Event.Detail, "demand-deep-inspection") {
		f.t.Fatalf("deep-inspection scan observation=%+v event=%+v", observation, deep.Event)
	}
	f.publish(*deep.Event)

	records, err := wake.Pending(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	var stale, heartbeatWake bool
	for _, record := range records {
		stale = stale || record.Kind == "stale"
		heartbeatWake = heartbeatWake || record.Kind == "heartbeat"
	}
	if !stale || !heartbeatWake {
		f.t.Fatalf("durable wakes=%+v, want stale and heartbeat records", records)
	}

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"drain"}, &stdout, &stderr); exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "stale") || !strings.Contains(stdout.String(), "heartbeat") {
		f.t.Fatalf("cfo drain exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func (f *fleetE2EFixture) ScanUnknownEndpoint(id string) {
	f.t.Helper()
	meta, err := state.ReadTaskMeta(f.home.State, id)
	if err != nil {
		f.t.Fatal(err)
	}
	f.runner.missing[meta.HerdrPaneID] = true
	f.now = f.now.Add(time.Second)
	result := f.scan()
	observation := observationFor(f.t, result.Observations, id)
	if result.Event == nil || observation.Health != monitor.HealthUnknown || observation.Reason != monitor.EndpointMissing {
		f.t.Fatalf("unknown endpoint observation=%+v event=%+v", observation, result.Event)
	}
	f.publish(*result.Event)
}

func (f *fleetE2EFixture) AssertFleetJSONAndMarkdownParity() {
	f.t.Helper()
	jsonOutput, jsonStderr := runFleetCommand(f.t, f.runtime, "fleet-view", "--json")
	if jsonStderr != "" {
		f.t.Fatalf("fleet JSON stderr=%q", jsonStderr)
	}
	var snapshot fleet.Snapshot
	if err := json.Unmarshal([]byte(jsonOutput), &snapshot); err != nil {
		f.t.Fatalf("decode fleet JSON: %v\n%s", err, jsonOutput)
	}
	if len(snapshot.Tasks) != 3 {
		f.t.Fatalf("fleet tasks=%d, want 3", len(snapshot.Tasks))
	}
	claude := taskRowFor(f.t, snapshot.Tasks, "claude")
	if claude.Current.State != crewstate.Working || claude.Monitor.Health != monitor.HealthStale || claude.Monitor.Escalation != 2 || !claude.Monitor.DemandDeepInspection || claude.Monitor.LastSeen == nil {
		f.t.Fatalf("Claude fleet row=%+v", claude)
	}
	codex := taskRowFor(f.t, snapshot.Tasks, "codex")
	if codex.Current.State != crewstate.Unknown || codex.Monitor.Health != monitor.HealthUnknown || codex.Endpoint.Exists == nil || *codex.Endpoint.Exists {
		f.t.Fatalf("Codex unknown fleet row=%+v", codex)
	}
	if samePath(claude.Path, f.project) || claude.Path == "" || claude.Endpoint.Target != "fleet-e2e:pane:claude" {
		f.t.Fatalf("Claude fleet identity=%+v", claude)
	}

	markdown, markdownStderr := runFleetCommand(f.t, f.runtime, "fleet-view")
	if markdownStderr != "" {
		f.t.Fatalf("fleet Markdown stderr=%q", markdownStderr)
	}
	for _, value := range []string{
		"| claude | working / status | stale |",
		"| codex | unknown / none | unknown |",
		claude.Monitor.LastSeen.UTC().Format(time.RFC3339),
		"| 2 | yes |",
		claude.Path,
	} {
		if !strings.Contains(markdown, value) {
			f.t.Fatalf("fleet Markdown missing %q:\n%s", value, markdown)
		}
	}
}

func (f *fleetE2EFixture) AssertVisibleTabsAndNoLifecycleDeletes() {
	f.t.Helper()
	if len(f.runner.tabs) != 3 {
		f.t.Fatalf("visible fake Herdr tabs=%v, want one task tab per worker", f.runner.tabLabels())
	}
	for _, id := range []string{"claude", "codex", "pi"} {
		meta, err := state.ReadTaskMeta(f.home.State, id)
		if err != nil {
			f.t.Fatal(err)
		}
		if _, ok := f.runner.tabs["fm-"+id]; !ok {
			f.t.Fatalf("monitoring removed fm-%s tab", id)
		}
		if info, err := os.Stat(meta.Worktree); err != nil || !info.IsDir() {
			f.t.Fatalf("monitoring removed %s worktree: %v", id, err)
		}
	}
	if len(f.git.returned) != 1 || f.git.returned[0].worktree != f.project {
		f.t.Fatalf("treehouse return calls=%+v, want only explicit ambiguous refusal", f.git.returned)
	}
	for _, request := range f.runner.requests {
		if request.Name != "herdr" {
			if (request.Name == "claude" || request.Name == "codex") && len(request.Args) == 1 && request.Args[0] == "--version" {
				continue
			}
			if request.Name == "pi" && len(request.Args) == 1 && request.Args[0] == "--help" {
				continue
			}
			f.t.Fatalf("unexpected fake external request=%+v", request)
		}
		if len(request.Args) < 2 || request.Args[len(request.Args)-2] != "--session" || request.Args[len(request.Args)-1] != "fleet-e2e" {
			f.t.Fatalf("Herdr request missing explicit session: %+v", request)
		}
		for _, argument := range request.Args {
			if argument == "close" || argument == "delete" || argument == "return" || argument == "restart" {
				f.t.Fatalf("monitoring or fleet command issued lifecycle action: %+v", request)
			}
		}
	}
}

func (f *fleetE2EFixture) spawn(ctx context.Context, h home.Home, request spawn.Request) (spawn.Result, error) {
	service := spawn.Service{
		Herdr: f.client,
		Treehouse: treehouse.Service{
			Git:     f.git,
			Poll:    time.Millisecond,
			Timeout: time.Second,
			Sleep:   noWait,
		},
		Harness:  harness.DefaultRegistry(),
		StateDir: h.State,
		Sleep:    noWait,
	}
	return service.Spawn(ctx, request)
}

func (f *fleetE2EFixture) sendText(ctx context.Context, h home.Home, target, text string) error {
	return fleet.Sender{
		Resolve: fleet.Resolver{StateDir: h.State},
		Herdr:   f.client,
		Sleep:   noWait,
	}.Text(ctx, target, text)
}

func (f *fleetE2EFixture) sendKey(ctx context.Context, h home.Home, target, key string) error {
	return fleet.Sender{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: f.client}.Key(ctx, target, key)
}

func (f *fleetE2EFixture) peek(ctx context.Context, h home.Home, target string, lines int) (string, error) {
	return fleet.Peeker{Resolve: fleet.Resolver{StateDir: h.State}, Herdr: f.client}.Tail(ctx, target, lines)
}

func (f *fleetE2EFixture) snapshot(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
	return fleet.BuildSnapshot(ctx, h, fleetE2EEndpoint{fixture: f})
}

func (f *fleetE2EFixture) monitorService() monitor.Service {
	f.monitors++
	return monitor.Service{
		StateDir:              f.home.State,
		Probe:                 f.prober,
		Now:                   func() time.Time { return f.now },
		StaleEscalateAfter:    time.Minute,
		BusyTurnMax:           10 * time.Minute,
		DemandInspectionAfter: 2,
		Heartbeat:             time.Minute,
		HeartbeatMax:          4 * time.Minute,
	}
}

func (f *fleetE2EFixture) scan() monitor.ScanResult {
	f.t.Helper()
	result, err := f.monitorService().Scan(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func (f *fleetE2EFixture) publish(event monitor.Event) {
	f.t.Helper()
	if _, err := f.monitorService().Publish(event); err != nil {
		f.t.Fatal(err)
	}
}

type fleetE2ERunner struct {
	fixture   *fleetE2EFixture
	workspace bool
	tabs      map[string]fleetE2ETab
	worktrees map[string]string
	busy      map[string]herdr.BusyState
	missing   map[string]bool
	lastText  string
	requests  []execx.Request
}

type fleetE2ETab struct {
	id   string
	pane string
}

func (r *fleetE2ERunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	request.Args = append([]string(nil), request.Args...)
	r.requests = append(r.requests, request)
	if request.Name == "claude" || request.Name == "codex" {
		return result("version 1.0\n"), nil
	}
	if request.Name == "pi" {
		return result("Usage:\nOptions:\n  --model\n  --thinking low medium high xhigh max\n  --extension\n  --tui-mode\n"), nil
	}
	if request.Name != "herdr" {
		return execx.Result{}, fmt.Errorf("unexpected fake executable %q", request.Name)
	}
	args, err := r.herdrArgs(request.Args)
	if err != nil {
		return execx.Result{}, err
	}
	switch {
	case matches(args, "workspace", "list"):
		if !r.workspace {
			return resultEnvelope(map[string]any{"workspaces": []any{}}), nil
		}
		return resultEnvelope(map[string]any{"workspaces": []any{map[string]string{"workspace_id": "workspace-e2e", "label": "firstmate"}}}), nil
	case matches(args, "workspace", "create"):
		r.workspace = true
		return resultEnvelope(map[string]any{
			"workspace": map[string]string{"workspace_id": "workspace-e2e"},
			"tab":       map[string]string{"tab_id": "seeded-default"},
		}), nil
	case matches(args, "tab", "list"):
		tabs := make([]map[string]string, 0, len(r.tabs))
		for _, label := range r.tabLabels() {
			tab := r.tabs[label]
			tabs = append(tabs, map[string]string{"tab_id": tab.id, "label": label})
		}
		return resultEnvelope(map[string]any{"tabs": tabs}), nil
	case matches(args, "tab", "create"):
		label, ok := flagValue(args, "--label")
		if !ok || !strings.HasPrefix(label, "fm-") {
			return execx.Result{}, fmt.Errorf("tab create is missing fm- label: %v", args)
		}
		id := strings.TrimPrefix(label, "fm-")
		pane := "pane:" + id
		tab := fleetE2ETab{id: "tab-" + id, pane: pane}
		r.tabs[label] = tab
		worktree := filepath.Join(r.fixture.home.Root, "worktrees", id)
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			return execx.Result{}, err
		}
		r.worktrees[pane] = worktree
		return resultEnvelope(map[string]any{
			"tab":       map[string]string{"tab_id": tab.id},
			"root_pane": map[string]string{"pane_id": pane},
		}), nil
	case matches(args, "pane", "run"):
		if len(args) < 4 || args[3] != "treehouse get" {
			return execx.Result{}, fmt.Errorf("unexpected pane run: %v", args)
		}
		return resultEnvelope(map[string]any{}), nil
	case matches(args, "pane", "get"):
		if len(args) < 3 {
			return execx.Result{}, fmt.Errorf("pane get is missing pane: %v", args)
		}
		pane := args[2]
		worktree, ok := r.worktrees[pane]
		if !ok {
			return resultEnvelope(map[string]any{"pane": map[string]string{"pane_id": pane, "foreground_cwd": r.fixture.project}}), nil
		}
		return resultEnvelope(map[string]any{"pane": map[string]string{"pane_id": pane, "foreground_cwd": worktree}}), nil
	case matches(args, "pane", "send-text"):
		if len(args) < 4 {
			return execx.Result{}, fmt.Errorf("pane send-text is incomplete: %v", args)
		}
		r.lastText = args[3]
		return resultEnvelope(map[string]any{}), nil
	case matches(args, "pane", "send-keys"):
		return resultEnvelope(map[string]any{}), nil
	case matches(args, "pane", "read"):
		if hasArgument(args, "--format") {
			return result(strings.Repeat("terminal line\n", 199) + "❯\n"), nil
		}
		return result(strings.Repeat("terminal line\n", 199) + "acceptance marker\n"), nil
	case matches(args, "agent", "get"):
		return resultEnvelope(map[string]any{"agent": map[string]string{"agent": "claude", "agent_status": "working"}}), nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected fake Herdr command: %v", args)
	}
}

func (r *fleetE2ERunner) herdrArgs(args []string) ([]string, error) {
	if len(args) < 3 || args[len(args)-2] != "--session" || args[len(args)-1] != "fleet-e2e" {
		return nil, fmt.Errorf("Herdr command must use explicit fleet-e2e session: %v", args)
	}
	return args[:len(args)-2], nil
}

func (r *fleetE2ERunner) tabLabels() []string {
	labels := make([]string, 0, len(r.tabs))
	for label := range r.tabs {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

type fleetE2EGit struct {
	fixture   *fleetE2EFixture
	freshened []string
	returned  []fleetE2EReturn
}

type fleetE2EReturn struct {
	project  string
	worktree string
}

func (g *fleetE2EGit) WorktreeTop(_ context.Context, dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (g *fleetE2EGit) FetchAndFreshen(_ context.Context, dir string) error {
	if samePath(dir, g.fixture.project) {
		return fmt.Errorf("fixture treehouse refuses to freshen primary checkout %q", dir)
	}
	g.freshened = append(g.freshened, dir)
	return nil
}

func (g *fleetE2EGit) Return(_ context.Context, project, worktree string) error {
	g.returned = append(g.returned, fleetE2EReturn{project: project, worktree: worktree})
	if samePath(project, worktree) || samePath(worktree, g.fixture.project) {
		return fmt.Errorf("fixture treehouse refuses ambiguous return of primary checkout %q", worktree)
	}
	return fmt.Errorf("fixture treehouse return is intentionally unavailable in deterministic e2e")
}

type fleetE2EProber struct {
	fixture *fleetE2EFixture
	calls   map[string]int
}

func (p *fleetE2EProber) Inspect(_ context.Context, meta state.TaskMeta) (monitor.EndpointSample, error) {
	p.calls[meta.ID]++
	if p.fixture.runner.missing[meta.HerdrPaneID] {
		return monitor.EndpointSample{Verdict: monitor.ProbeMissing, Detail: "fixture deliberately removed endpoint"}, nil
	}
	capture := "unchanged"
	if meta.ID != "claude" {
		capture = fmt.Sprintf("%s-progress-%d", meta.ID, p.calls[meta.ID])
	}
	busy := p.fixture.runner.busy[meta.ID]
	if busy == "" {
		busy = herdr.BusyIdle
	}
	return monitor.EndpointSample{
		Verdict: monitor.ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "fm-" + meta.ID,
		Agent:    herdr.AgentAlive,
		Busy:     busy,
		Capture:  []byte(strings.Repeat(capture+"\n", 200)),
	}, nil
}

type fleetE2EEndpoint struct {
	fixture *fleetE2EFixture
}

func (e fleetE2EEndpoint) Exists(_ context.Context, target herdr.Target) (bool, error) {
	_, known := e.fixture.runner.worktrees[target.Pane]
	return known && !e.fixture.runner.missing[target.Pane], nil
}

func (e fleetE2EEndpoint) BusyState(_ context.Context, target herdr.Target) (herdr.BusyState, error) {
	id := strings.TrimPrefix(target.Pane, "pane:")
	if e.fixture.runner.missing[target.Pane] {
		return herdr.BusyUnknown, nil
	}
	if busy := e.fixture.runner.busy[id]; busy != "" {
		return busy, nil
	}
	return herdr.BusyIdle, nil
}

func (e fleetE2EEndpoint) Validate(_ context.Context, meta state.TaskMeta) (bool, error) {
	tab, ok := e.fixture.runner.tabs["fm-"+meta.ID]
	return ok && tab.id == meta.HerdrTabID && tab.pane == meta.HerdrPaneID, nil
}

func noWait(context.Context, time.Duration) error { return nil }

func runFleetCommand(t *testing.T, runtime commandRuntime, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime(args, &stdout, &stderr, runtime); exit != 0 {
		t.Fatalf("cfo %s exit=%d stdout=%q stderr=%q", strings.Join(args, " "), exit, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func observationFor(t *testing.T, observations []monitor.Observation, id string) monitor.Observation {
	t.Helper()
	for _, observation := range observations {
		if observation.TaskID == id {
			return observation
		}
	}
	t.Fatalf("monitor observations missing %q: %+v", id, observations)
	return monitor.Observation{}
}

func taskRowFor(t *testing.T, rows []fleet.TaskRow, id string) fleet.TaskRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("fleet rows missing %q: %+v", id, rows)
	return fleet.TaskRow{}
}

func result(text string) execx.Result {
	return execx.Result{Stdout: []byte(text)}
}

func resultEnvelope(value any) execx.Result {
	data, err := json.Marshal(map[string]any{"result": value})
	if err != nil {
		panic(err)
	}
	return execx.Result{Stdout: data}
}

func matches(args []string, first, second string) bool {
	return len(args) >= 2 && args[0] == first && args[1] == second
}

func flagValue(args []string, flag string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return args[index+1], true
		}
	}
	return "", false
}

func hasArgument(args []string, value string) bool {
	for _, argument := range args {
		if argument == value {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

var _ execx.Runner = (*fleetE2ERunner)(nil)
var _ treehouse.Git = (*fleetE2EGit)(nil)
var _ monitor.Prober = (*fleetE2EProber)(nil)
var _ fleet.EndpointReader = fleetE2EEndpoint{}
var _ crewstate.StructuralValidator = fleetE2EEndpoint{}
