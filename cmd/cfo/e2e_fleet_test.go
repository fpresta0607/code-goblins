package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// TestFleetEndToEnd covers the public fleet command flow without reaching an
// installed Herdr server, worktree creation, harness binary, or credential.
// The fixture is intentionally introduced after this test so this test first
// proves the exact end-to-end behavior the fake transport must support.
func TestFleetEndToEnd(t *testing.T) {
	fixture := newFleetE2EFixture(t)

	for _, harness := range []string{"claude", "codex", "pi"} {
		fixture.Seed(harness)
	}
	fixture.AssertTaskMetadataIsIsolated()

	// These direct monitor.Service scans prove the deterministic state-machine
	// boundary only. They do not claim that cfo watch wires a real Herdr prober;
	// the opt-in Windows acceptance script independently requires watch to
	// persist observations and fails closed until that production wiring exists.
	fixture.ScanActive("claude")
	fixture.ScanBusyProtected("claude")
	fixture.ScanStaleEscalation("claude")
	fixture.AssertHeartbeatPersistsAcrossRestart()
	fixture.AssertDurableWakesAndDeepInspection("claude")
	fixture.ScanUnknownEndpoint("codex")
	fixture.AssertFleetJSONAndMarkdownParity()
	fixture.AssertVisibleTabsAndNoLifecycleDeletes()
}

func TestPlan3AcceptanceScriptSelfTests(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Plan 3 acceptance script self-tests require Windows PowerShell")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(wd, "..", "..", "tests", "acceptance", "plan3_windows.ps1")
	cases := []struct {
		name      string
		selfTest  string
		optIn     bool
		wantError bool
		wantText  string
	}{
		{
			name:      "opt-in gate fails closed",
			wantError: true,
			wantText:  "Plan 3 Windows acceptance is opt-in.",
		},
		{
			name:      "missing cleanup fails closed",
			selfTest:  "missing-cleanup",
			optIn:     true,
			wantError: true,
			wantText:  "ACCEPTANCE BLOCKER: this cfo build has no cleanup command.",
		},
		{
			name:     "fixture CFO home meets primary predicates",
			selfTest: "primary-home",
			optIn:    true,
			wantText: "Plan 3 primary-home self-test passed.",
		},
		{
			name:     "fleet Markdown projects every required monitor field",
			selfTest: "fleet-parity",
			optIn:    true,
			wantText: "Plan 3 fleet parity self-test passed.",
		},
		{
			name:      "escaping worker worktree fails containment",
			selfTest:  "escaping-worker-path",
			optIn:     true,
			wantError: true,
			wantText:  "ACCEPTANCE BLOCKER: worker worktree escapes disposable root.",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
			if tt.selfTest != "" {
				args = append(args, "-SelfTest", tt.selfTest)
			}
			command := exec.Command("powershell", args...)
			command.Env = plan3ScriptEnv(tt.optIn)
			output, err := command.CombinedOutput()
			if (err != nil) != tt.wantError {
				t.Fatalf("self-test error = %v, wantError=%v\n%s", err, tt.wantError, output)
			}
			if !strings.Contains(string(output), tt.wantText) {
				t.Fatalf("self-test output missing %q:\n%s", tt.wantText, output)
			}
		})
	}
}

func TestPlan3ScriptEnvRemovesCaseInsensitiveOptIn(t *testing.T) {
	parent := []string{
		"PATH=C:\\Windows",
		"cfo_plan3_real=1",
		"Cfo_Plan3_Real=0",
		"CFO_PLAN3_REALLY=keep",
	}
	for _, value := range plan3ScriptEnvFrom(parent, false) {
		key, _, _ := strings.Cut(value, "=")
		if strings.EqualFold(key, "CFO_PLAN3_REAL") {
			t.Fatalf("child environment retains Plan 3 opt-in variable %q", value)
		}
	}
}

func plan3ScriptEnv(optIn bool) []string {
	return plan3ScriptEnvFrom(os.Environ(), optIn)
}

func plan3ScriptEnvFrom(parent []string, optIn bool) []string {
	env := make([]string, 0, len(parent)+1)
	for _, value := range parent {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, "CFO_PLAN3_REAL") {
			env = append(env, value)
		}
	}
	if optIn {
		env = append(env, "CFO_PLAN3_REAL=1")
	}
	return env
}

// fleetE2EFixture is a real command-path fixture. It drives the command
// parser and the send, peek, monitor, wake, and fleet packages while
// replacing only subprocesses with an in-memory Herdr and Git model.
// No installed tool, network service, credential, or production checkout is
// reachable from this test.
type fleetE2EFixture struct {
	t       *testing.T
	home    home.Home
	project string
	now     time.Time
	runner  *fleetE2ERunner
	git     *fleetE2EGit
	client  *herdr.Client
	runtime commandRuntime
	prober  *fleetE2EProber
}

func newFleetE2EFixture(t *testing.T) *fleetE2EFixture {
	t.Helper()
	// This fixture spawns through the real spawn.Service, which creates each
	// task's Go temporary directory under the user cache directory. Point
	// os.UserCacheDir at a directory of the test's own so the run leaves
	// nothing in the operator's cache, the same isolation CFO_HOME gets. HOME
	// is in the set because os.UserCacheDir reads it on darwin and on Linux
	// whenever XDG_CACHE_HOME is unset, and the resolve pins that the redirect
	// actually took rather than isolating nothing.
	cache := t.TempDir()
	for _, name := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(name, cache)
	}
	if resolved, err := os.UserCacheDir(); err != nil {
		t.Fatalf("UserCacheDir = %v, want the isolated cache directory", err)
	} else if rel, relErr := filepath.Rel(cache, resolved); relErr != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("UserCacheDir = %q, want it under the test's own directory %q", resolved, cache)
	}
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
	// Canonicalize so the fixture's expected paths match what cfo spawn writes
	// (spawn canonicalizes project and worktree paths; GitHub runners may hand
	// back 8.3 short names through os.TempDir).
	var err error
	if h.Root, err = fsx.Canonical(h.Root); err != nil {
		t.Fatal(err)
	}
	if h.State, err = fsx.Canonical(h.State); err != nil {
		t.Fatal(err)
	}
	if h.Data, err = fsx.Canonical(h.Data); err != nil {
		t.Fatal(err)
	}
	if project, err = fsx.Canonical(project); err != nil {
		t.Fatal(err)
	}

	fixture := &fleetE2EFixture{
		t:       t,
		home:    h,
		project: project,
		now:     time.Now().UTC().Truncate(time.Second),
	}
	fixture.runner = &fleetE2ERunner{
		fixture: fixture,
		tabs:    make(map[string]fleetE2ETab),
		busy:    make(map[string]crewstate.Busy),
		missing: make(map[string]bool),
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
		snapshot:    fixture.snapshot,
	}
	// cfo drain intentionally resolves its own home. This test never runs in
	// parallel, so setting the test-local environment is safe and lets the
	// command's normal code path render the durable wake queue.
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("HERDR_SESSION", "fleet-e2e")
	return fixture
}

// Seed records harnessName's goblin the way an older build spawned it in
// Herdr: its tab in the fake session, its worktree and its Herdr task record.
// cfo spawn starts every goblin natively now, and the send, peek, monitor and
// fleet commands this test drives still reach a goblin recorded in Herdr.
func (f *fleetE2EFixture) Seed(harnessName string) {
	f.t.Helper()
	ctx := context.Background()
	container, err := f.client.EnsureContainer(ctx, f.project)
	if err != nil {
		f.t.Fatal(err)
	}
	endpoint, err := f.client.CreateTask(ctx, container, "gb-"+harnessName, f.project)
	if err != nil {
		f.t.Fatal(err)
	}
	worktreePath, err := f.git.Acquire(ctx, f.project, "gb-"+harnessName)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := state.WriteTaskMeta(f.home.State, state.TaskMeta{
		ID:               harnessName,
		Window:           endpoint.Target.String(),
		EndpointTaskID:   harnessName,
		Worktree:         worktreePath,
		Project:          f.project,
		Harness:          harnessName,
		Kind:             "ship",
		Mode:             "local-only",
		Yolo:             "off",
		TaskTmp:          filepath.Join(f.home.State, "tasktmp", harnessName),
		Model:            "default",
		Effort:           "default",
		Backend:          "herdr",
		HerdrSession:     endpoint.Target.Session,
		HerdrWorkspaceID: endpoint.WorkspaceID,
		HerdrTabID:       endpoint.TabID,
		HerdrPaneID:      endpoint.PaneID,
		SpawnGen:         "s1",
	}); err != nil {
		f.t.Fatal(err)
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
			f.t.Fatalf("%s worktree missing after seeding: %v", id, err)
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
		if _, exists := f.runner.tabs["gb-"+id]; !exists {
			f.t.Fatalf("fake workspace is missing visible gb-%s tab", id)
		}
	}

	// The real cleanup command is not in this task's scope. This direct
	// service boundary proves an ambiguous return is refused and cannot remove
	// the primary checkout while the test keeps every worker alive.
	err := (worktree.Service{Git: f.git}).Return(context.Background(), f.project, f.project)
	if err == nil {
		f.t.Fatal("ambiguous worktree return unexpectedly succeeded")
	}
	if info, statErr := os.Stat(f.project); statErr != nil || !info.IsDir() {
		f.t.Fatalf("ambiguous return changed primary project: %v", statErr)
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
	f.runner.busy[id] = crewstate.BusyWorking
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
	f.runner.busy[id] = crewstate.BusyIdle
	f.now = f.now.Add(time.Second)
	idle := f.scan()
	idleObservation := observationFor(f.t, idle.Observations, id)
	if idle.Event != nil || idleObservation.Health != monitor.HealthIdle {
		f.t.Fatalf("idle scan observation=%+v event=%+v, want idle grace without event", idleObservation, idle.Event)
	}

	f.now = f.now.Add(time.Minute)
	stalled := f.scan()
	stalledObservation := observationFor(f.t, stalled.Observations, id)
	if stalled.Event == nil || stalled.Event.Kind != "stale" || stalledObservation.Health != monitor.HealthStale {
		f.t.Fatalf("stall scan observation=%+v event=%+v, want one stall wake", stalledObservation, stalled.Event)
	}
	f.publish(*stalled.Event)

	// Dedupe: the same stall does not re-wake, however long it lasts.
	f.now = f.now.Add(time.Minute)
	quiet := f.scan()
	if quiet.Event != nil {
		f.t.Fatalf("dedupe scan event=%+v, want no re-wake for the unchanged stall", quiet.Event)
	}
}

func (f *fleetE2EFixture) AssertHeartbeatPersistsAcrossRestart() {
	f.t.Helper()
	before, err := monitor.ReadHeartbeat(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	after, err := f.monitorService().Scan(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if before != after.Heartbeat || before.LastCycle.IsZero() || before.NextDue.IsZero() || after.Event != nil {
		f.t.Fatalf("restart heartbeat changed before=%+v after=%+v", before, after)
	}
}

func (f *fleetE2EFixture) AssertDurableWakesAndDeepInspection(id string) {
	f.t.Helper()
	// A stale goblin is doing its job: after its single stall wake, neither the
	// heartbeat nor the escalation ladder re-wakes it.
	f.now = f.now.Add(2 * time.Minute)
	resurfaced := f.scan()
	if resurfaced.Event != nil {
		f.t.Fatalf("stale goblin was re-woken: event=%+v", resurfaced.Event)
	}

	records, err := wake.Pending(f.home.State)
	if err != nil {
		f.t.Fatal(err)
	}
	var stale bool
	for _, record := range records {
		stale = stale || record.Kind == "stale"
	}
	if !stale {
		f.t.Fatalf("durable wakes=%+v, want the stall wake", records)
	}

	var stdout, stderr bytes.Buffer
	if exit := run([]string{"drain"}, &stdout, &stderr); exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "stale") {
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
	if claude.Current.State != crewstate.Working || claude.Current.Source != crewstate.SourceStatus || claude.Monitor.Health != monitor.HealthStale || claude.Monitor.StaleSeconds <= 0 || claude.Monitor.Escalation < 2 || !claude.Monitor.DemandDeepInspection || claude.Monitor.LastSeen == nil || claude.Endpoint.Exists == nil || !*claude.Endpoint.Exists {
		f.t.Fatalf("Claude fleet row=%+v", claude)
	}
	codex := taskRowFor(f.t, snapshot.Tasks, "codex")
	if codex.Current.State != crewstate.Unknown || codex.Monitor.Health != monitor.HealthUnknown || codex.Endpoint.Exists == nil || *codex.Endpoint.Exists {
		f.t.Fatalf("Codex unknown fleet row=%+v", codex)
	}
	if samePath(claude.Path, f.project) || claude.Path == "" || claude.Endpoint.Target != "fleet-e2e:pane:claude" || claude.Endpoint.Session != "fleet-e2e" || claude.Endpoint.PaneID != "pane:claude" {
		f.t.Fatalf("Claude fleet identity=%+v", claude)
	}

	markdown, markdownStderr := runFleetCommand(f.t, f.runtime, "fleet-view")
	if markdownStderr != "" {
		f.t.Fatalf("fleet Markdown stderr=%q", markdownStderr)
	}
	claudeRow := strings.Join([]string{
		"| claude",
		"working / status",
		"stale",
		(time.Duration(claude.Monitor.StaleSeconds) * time.Second).String(),
		claude.Monitor.LastSeen.UTC().Format(time.RFC3339),
		strconv.Itoa(claude.Monitor.Escalation),
		"yes",
		claude.Kind,
		claude.Project,
		claude.Backend,
		claude.Endpoint.Target + " (present)",
		"-",
		claude.Path,
		claude.Actions.Peek + " |",
	}, " | ")
	if !strings.Contains(markdown, claudeRow) {
		f.t.Fatalf("fleet Markdown does not exactly project Claude JSON row %q:\n%s", claudeRow, markdown)
	}
	if !strings.Contains(markdown, "| codex | unknown / none | unknown |") {
		f.t.Fatalf("fleet Markdown missing unknown endpoint/current-state projection:\n%s", markdown)
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
		if _, ok := f.runner.tabs["gb-"+id]; !ok {
			f.t.Fatalf("monitoring removed gb-%s tab", id)
		}
		if info, err := os.Stat(meta.Worktree); err != nil || !info.IsDir() {
			f.t.Fatalf("monitoring removed %s worktree: %v", id, err)
		}
	}
	if len(f.git.returned) != 1 || f.git.returned[0].worktree != f.project {
		f.t.Fatalf("worktree return calls=%+v, want only explicit ambiguous refusal", f.git.returned)
	}
	for _, request := range f.runner.requests {
		if request.Name != "herdr" {
			f.t.Fatalf("unexpected fake external request=%+v", request)
		}
		sessionAt := slices.Index(request.Args, "--session")
		if sessionAt < 0 || sessionAt+1 >= len(request.Args) || request.Args[sessionAt+1] != "fleet-e2e" {
			f.t.Fatalf("Herdr request missing explicit session: %+v", request)
		}
		if separator := slices.Index(request.Args, "--"); separator >= 0 && sessionAt > separator {
			f.t.Fatalf("Herdr session flag leaked past the agent args separator: %+v", request)
		}
		for _, argument := range request.Args {
			if argument == "close" || argument == "delete" || argument == "return" || argument == "restart" {
				f.t.Fatalf("monitoring or fleet command issued lifecycle action: %+v", request)
			}
		}
	}
}

func (f *fleetE2EFixture) snapshot(ctx context.Context, h home.Home) (fleet.Snapshot, error) {
	return fleet.BuildSnapshot(ctx, h, fleetE2EEndpoint{fixture: f})
}

func (f *fleetE2EFixture) monitorService() monitor.Service {
	return monitor.Service{
		StateDir:              f.home.State,
		Probe:                 f.prober,
		Now:                   func() time.Time { return f.now },
		StaleEscalateAfter:    time.Minute,
		StallAfter:            time.Minute,
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
	busy      map[string]crewstate.Busy
	missing   map[string]bool
	requests  []execx.Request
}

type fleetE2ETab struct {
	id   string
	pane string
}

func (r *fleetE2ERunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	request.Args = append([]string(nil), request.Args...)
	r.requests = append(r.requests, request)
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
		return resultEnvelope(map[string]any{"workspaces": []any{map[string]string{"workspace_id": "workspace-e2e", "label": "cfo"}}}), nil
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
		if !ok || !strings.HasPrefix(label, "gb-") {
			return execx.Result{}, fmt.Errorf("tab create is missing gb- label: %v", args)
		}
		id := strings.TrimPrefix(label, "gb-")
		pane := "pane:" + id
		r.tabs[label] = fleetE2ETab{id: "tab-" + id, pane: pane}
		return resultEnvelope(map[string]any{
			"tab":       map[string]string{"tab_id": "tab-" + id},
			"root_pane": map[string]string{"pane_id": pane},
		}), nil
	case matches(args, "pane", "get"):
		if len(args) < 3 {
			return execx.Result{}, fmt.Errorf("pane get is missing pane: %v", args)
		}
		return resultEnvelope(map[string]any{"pane": map[string]string{"pane_id": args[2]}}), nil
	case matches(args, "tab", "close"):
		// A closed tab is gone, so a later task can take its label back.
		if len(args) >= 3 {
			for label, tab := range r.tabs {
				if tab.id == args[2] {
					delete(r.tabs, label)
				}
			}
		}
		return resultEnvelope(map[string]any{}), nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected fake Herdr command: %v", args)
	}
}

func (r *fleetE2ERunner) herdrArgs(args []string) ([]string, error) {
	// The session flag may sit before a `--` agent-args separator rather than
	// at the tail.
	sessionAt := slices.Index(args, "--session")
	if sessionAt < 0 || sessionAt+1 >= len(args) || args[sessionAt+1] != "fleet-e2e" {
		return nil, fmt.Errorf("Herdr command must use explicit fleet-e2e session: %v", args)
	}
	if separator := slices.Index(args, "--"); separator >= 0 && sessionAt > separator {
		return nil, fmt.Errorf("Herdr session flag leaked past the agent args separator: %v", args)
	}
	return slices.Delete(append([]string(nil), args...), sessionAt, sessionAt+2), nil
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
	fixture  *fleetE2EFixture
	returned []fleetE2EReturn
}

type fleetE2EReturn struct {
	project  string
	worktree string
}

func (g *fleetE2EGit) Acquire(_ context.Context, project, holder string) (string, error) {
	if !strings.HasPrefix(holder, "gb-") {
		return "", fmt.Errorf("acquire is missing gb- holder: %q", holder)
	}
	if !samePath(project, g.fixture.project) {
		return "", fmt.Errorf("acquire ran outside the project: %q", project)
	}
	path := filepath.Join(g.fixture.home.Root, "worktrees", strings.TrimPrefix(holder, "gb-"))
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func (g *fleetE2EGit) WorktreeTop(_ context.Context, dir string) (string, error) {
	if _, err := os.Stat(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func (g *fleetE2EGit) EnsureSeeded(context.Context, string) (bool, error) {
	return false, nil
}

func (g *fleetE2EGit) Return(_ context.Context, project, worktree string) error {
	g.returned = append(g.returned, fleetE2EReturn{project: project, worktree: worktree})
	if samePath(project, worktree) || samePath(worktree, g.fixture.project) {
		return fmt.Errorf("fixture git refuses ambiguous return of primary checkout %q", worktree)
	}
	return fmt.Errorf("fixture worktree return is intentionally unavailable in deterministic e2e")
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
	stateChangeSeq := int64(0)
	if meta.ID != "claude" {
		capture = fmt.Sprintf("%s-progress-%d", meta.ID, p.calls[meta.ID])
		// The "progressing" goblins advance their liveness counter so they
		// never stall; claude stays static so the stall test has a subject.
		stateChangeSeq = int64(p.calls[meta.ID])
	}
	busy := p.fixture.runner.busy[meta.ID]
	if busy == "" {
		busy = crewstate.BusyIdle
	}
	status := monitor.StatusIdle
	if busy == crewstate.BusyWorking {
		status = monitor.StatusWorking
	}
	return monitor.EndpointSample{
		Verdict:        monitor.ProbePresent,
		TabLabel:       "gb-" + meta.ID,
		Busy:           busy,
		Status:         status,
		StateChangeSeq: stateChangeSeq,
		Capture:        []byte(strings.Repeat(capture+"\n", 200)),
	}, nil
}

type fleetE2EEndpoint struct {
	fixture *fleetE2EFixture
}

func (e fleetE2EEndpoint) Read(_ context.Context, meta state.TaskMeta) (bool, crewstate.Busy, error) {
	id := strings.TrimPrefix(meta.HerdrPaneID, "pane:")
	_, known := e.fixture.runner.tabs["gb-"+id]
	if !known || e.fixture.runner.missing[meta.HerdrPaneID] {
		return false, crewstate.BusyUnknown, nil
	}
	if busy := e.fixture.runner.busy[id]; busy != "" {
		return true, busy, nil
	}
	return true, crewstate.BusyIdle, nil
}

func (e fleetE2EEndpoint) Validate(_ context.Context, meta state.TaskMeta) (bool, error) {
	tab, ok := e.fixture.runner.tabs["gb-"+meta.ID]
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
var _ worktree.Git = (*fleetE2EGit)(nil)
var _ monitor.Prober = (*fleetE2EProber)(nil)
var _ fleet.EndpointReader = fleetE2EEndpoint{}
var _ crewstate.StructuralValidator = fleetE2EEndpoint{}
