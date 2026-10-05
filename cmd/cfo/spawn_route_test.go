package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

// shippedRoutingJSON is the lane table that ships in data/routing.json, so a
// routed spawn is tested against the table the fleet actually runs.
func shippedRoutingJSON(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "data", "routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func routedRuntime(t *testing.T, routingJSON string) (home.Home, commandRuntime) {
	t.Helper()
	h := testHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "routing.json"), []byte(routingJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	return h, testCommandRuntimeForHome(h)
}

func briefWith(t *testing.T, task string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "brief.md")
	body := "# Brief g\n\n## Task\n\n" + task + "\n\n## Delivery\n\nkind: ship\nmode: direct-PR\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureQuota(t *testing.T, name string) func(context.Context) (quota.Report, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "quota", "testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := quota.Parse(data, time.Date(2026, 9, 17, 12, 31, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (quota.Report, string) { return report, "" }
}

func captureSpawn(deps *commandRuntime, output string) *spawn.Request {
	var got spawn.Request
	deps.spawn = func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
		got = request
		return spawn.Result{Output: output}, nil
	}
	return &got
}

func TestRunSpawnHonorsFivePercentWeeklyReserve(t *testing.T) {
	for _, isExplicit := range []bool{false, true} {
		for _, testCase := range []struct {
			name, kind, used, remaining string
			isStale, shouldRefuse       bool
		}{
			{name: "weekly 4.99 remaining", kind: "weekly", used: "95.01", remaining: "4.99", shouldRefuse: true},
			{name: "weekly 5 remaining", kind: "weekly", used: "95", remaining: "5", shouldRefuse: true},
			{name: "weekly 5.01 remaining", kind: "weekly", used: "94.99", remaining: "5.01"},
			{name: "session only", kind: "session", used: "98", remaining: "2"},
			{name: "weekly use unknown", kind: "weekly", used: `"unknown"`, remaining: "2"},
			{name: "stale provider", kind: "weekly", used: "98", remaining: "2", isStale: true},
		} {
			t.Run(fmt.Sprintf("%s explicit %v", testCase.name, isExplicit), func(t *testing.T) {
				_, deps := routedRuntime(t, `{"rules":[],"default_lane":"build","lanes":{"build":{"harness":"codex","model":"weekly-model","effort":"high"}}}`)
				got := captureSpawn(&deps, "spawned weekly-task")
				now := time.Now().UTC().Truncate(time.Second)
				reset := now.Add(7 * 24 * time.Hour)
				seconds := 604800
				if testCase.kind == "session" {
					seconds = 18000
				}
				data := fmt.Sprintf(`{"generatedAt":%q,"providers":[{"provider":"codex","state":{"stale":%t},
					"windows":[{"id":"window","kind":%q,"windowSeconds":%d,"percentUsed":%s,"resetsAt":%q}],
					"quotaSemantics":{"status":"known","effectiveAvailability":[{"scope":"all_models","status":"known",
						"effectivePercentRemaining":%s,"boundedBy":["window"],"runway":{"limitingWindowId":"window"}}]}}]}`,
					now.Format(time.RFC3339), testCase.isStale, testCase.kind, seconds, testCase.used, reset.Format(time.RFC3339), testCase.remaining)
				report, err := quota.Parse([]byte(data), now)
				if err != nil {
					t.Fatal(err)
				}
				deps.quota = func(context.Context) (quota.Report, string) { return report, "" }
				args := []string{"spawn", "weekly-task", "--project", `C:\project`, "--brief", briefWith(t, "Repair the weekly allowance reserve.")}
				if isExplicit {
					args = append(args, "--harness", "codex", "--model", "weekly-model")
				}
				var stdout, stderr bytes.Buffer

				exit := runWithRuntime(args, &stdout, &stderr, deps)

				if testCase.shouldRefuse {
					if exit != 1 || got.ID != "" || !strings.Contains(stderr.String(), "5 percent weekly") || !strings.Contains(stderr.String(), reset.Format(time.RFC3339)) {
						t.Fatalf("weekly reserve refusal exit=%d spawn=%+v stderr=%s", exit, *got, stderr.String())
					}
				} else if exit != 0 || got.ID != "weekly-task" {
					t.Fatalf("usable or unknown quota prevented spawn: exit=%d spawn=%+v stderr=%s", exit, *got, stderr.String())
				}
			})
		}
	}
}

func TestRunSpawnRoutesFromTheFleetTableWithoutHarness(t *testing.T) {
	h, deps := routedRuntime(t, shippedRoutingJSON(t))
	got := captureSpawn(&deps, "spawned g10")
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g10", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "opus" || got.Effort != "high" {
		t.Errorf("request = %+v, want the build lane (claude/opus/high)", *got)
	}
	if got.BriefPath != brief || got.Capsule != nil {
		t.Errorf("brief = %q capsule = %v, want the original brief and no capsule when the project has no manifest", got.BriefPath, got.Capsule != nil)
	}
	want := "spawned g10\nrouted lane=build class=implementation risk=normal source=fleet table " + filepath.Join(h.Data, "routing.json") + " quota=check skipped (no quota reader in this runtime)\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunSpawnRoutesHighRiskWorkToTheDeepLane(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	got := captureSpawn(&deps, "spawned g11")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g11", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "fable" || got.Effort != "xhigh" {
		t.Errorf("request = %+v, want the deep lane (claude/fable/xhigh)", *got)
	}
	if !strings.Contains(stdout.String(), "routed lane=deep class=security risk=high source=fleet table ") {
		t.Errorf("stdout = %q, want the deep lane reported", stdout.String())
	}
}

func TestRunSpawnAutoIsAnAliasForTheDefaultRouting(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	got := captureSpawn(&deps, "spawned g12")
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g12", "--project", `C:\project`, "--brief", brief, "--auto"}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "opus" || !strings.Contains(stdout.String(), "routed lane=build class=implementation") {
		t.Errorf("request = %+v stdout = %q, want --auto to route exactly as the default does", *got, stdout.String())
	}
}

func TestRunSpawnAppliesAModelFlagOverTheRoutedLaneAndSaysSo(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	got := captureSpawn(&deps, "spawned g22")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g22", "--project", `C:\project`, "--brief", brief, "--model", "opus"}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "opus" || got.Effort != "xhigh" {
		t.Errorf("request = %+v, want the deep lane with the given model (claude/opus/xhigh)", *got)
	}
	if !strings.Contains(stdout.String(), "routed lane=deep class=security risk=high ") || !strings.HasSuffix(stdout.String(), " overrides=--model opus\n") {
		t.Errorf("stdout = %q, want the routed line to end with the override", stdout.String())
	}
}

func TestRunSpawnQuotaChecksTheOverriddenModel(t *testing.T) {
	// codex is exhausted_now in the fixture, so the spawn falls to build, and
	// build is judged on the model:fable scope of the model that will run
	// rather than the all-models scope of the lane's own opus.
	_, deps := routedRuntime(t, codexDeepTable)
	deps.quota = fixtureQuota(t, "exhausted")
	got := captureSpawn(&deps, "spawned g23")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g23", "--project", `C:\project`, "--brief", brief, "--model", "fable", "--effort", "low"}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "fable" || got.Effort != "low" {
		t.Errorf("request = %+v, want the build lane with both overrides applied", *got)
	}
	if !strings.Contains(stdout.String(), " quota=deep: codex all_models exhausted_now, resets 2026-09-20T12:17:37Z; build: claude model:fable 97% remaining, runway through_reset overrides=--model fable --effort low\n") {
		t.Errorf("stdout = %q, want the fable scope judged on the fallback lane and both overrides reported", stdout.String())
	}
}

func TestRunSpawnRoutedFailsOnAnInvalidLaneTableButAnExplicitHarnessDoesNot(t *testing.T) {
	_, deps := routedRuntime(t, `{"rules":[],"default_lane":"buld","lanes":{"build":{"harness":"claude"}}}`)
	captureSpawn(&deps, "spawned g24")
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g24", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 1 || !strings.Contains(stderr.String(), `default_lane "buld" is not a defined lane`) {
		t.Errorf("routed: exit = %d stderr = %q, want the lane table refused", exit, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := runWithRuntime([]string{"spawn", "g24", "--project", `C:\project`, "--brief", brief, "--harness", "codex"}, &stdout, &stderr, deps); exit != 0 {
		t.Errorf("explicit: exit = %d stderr = %q, want a spawn that never routes to ignore the lane table", exit, stderr.String())
	}
}

func TestRunSpawnExplicitHarnessWinsOverTheTableAndSaysSo(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	got := captureSpawn(&deps, "spawned g13")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g13", "--project", `C:\project`, "--brief", brief, "--harness", "codex", "--model", "gpt-5", "--effort", "low"}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "codex" || got.Model != "gpt-5" || got.Effort != "low" || got.BriefPath != brief {
		t.Errorf("request = %+v, want the explicit flags untouched", *got)
	}
	if want := "spawned g13\nrouted lane=explicit class=security risk=high source=--harness flag\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunSpawnProjectManifestOverridesTheFleetTable(t *testing.T) {
	h, deps := routedRuntime(t, shippedRoutingJSON(t))
	manifestPath := filepath.Join(h.Data, "projects", "project", "project.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project":"project","routing":{"default_lane":"open","escalate_to":"open","lanes":{"open":{"harness":"pi","model":"open-model","effort":"low"}}},"budgets":{"implementation":{"max_repair_rounds":4}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	got := captureSpawn(&deps, "spawned g14")
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g14", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "pi" || got.Model != "open-model" || got.Effort != "low" {
		t.Errorf("request = %+v, want the project's own lane", *got)
	}
	if !strings.Contains(stdout.String(), "routed lane=open class=implementation risk=normal source=project override "+manifestPath) {
		t.Errorf("stdout = %q, want the override named with its manifest", stdout.String())
	}
	if got.Capsule == nil {
		t.Error("capsule = nil, want the manifest's capsule handed to the spawn")
	}
}

// A routed spawn for a project with a manifest once wrote its capsule into
// state/tasktmp/<id> before the service ran, and the service's alias check
// refuses any existing directory of the id, so the spawn was refused and the
// directory it left behind refused every retry too. The command now hands the
// service a capsule to write once the id is proven free, which the spawn
// package's capsule tests prove it does; here the capsule is written where
// the service would, and holds the brief, the frozen class, lane and budget.
func TestRunSpawnWritesTheManifestCapsuleAndDispatches(t *testing.T) {
	fixture := newFleetE2EFixture(t)
	manifestPath := filepath.Join(fixture.home.Data, "projects", filepath.Base(fixture.project), "project.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project":"disposable-project","routing":{"default_lane":"open","escalate_to":"open","lanes":{"open":{"harness":"claude","model":"open-model","effort":"low"}}},"budgets":{"implementation":{"max_repair_rounds":4}}}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(fixture.home.Root, "g-fresh1.brief.md")
	if err := os.WriteFile(brief, []byte("Add a dark-mode toggle to the settings page.\n\nDelivery contract: mode=local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	taskTmp := filepath.Join(fixture.home.State, "tasktmp", "g-fresh1")
	var capsuleBrief string
	fixture.runtime.spawn = func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
		if request.Capsule == nil || request.Model != "open-model" {
			return spawn.Result{}, fmt.Errorf("request = %+v, want a capsule on the project's lane", request)
		}
		var err error
		capsuleBrief, err = request.Capsule(taskTmp)
		return spawn.Result{Output: "spawned g-fresh1 window=native"}, err
	}

	stdout, _ := runFleetCommand(t, fixture.runtime, "spawn", "g-fresh1", "--project", fixture.project, "--brief", brief, "--mode", "local-only")
	if !strings.Contains(stdout, "spawned g-fresh1 ") || !strings.Contains(stdout, "routed lane=open class=implementation risk=normal source=project override "+manifestPath) {
		t.Errorf("stdout = %q, want the goblin spawned on the project's own lane", stdout)
	}
	if capsuleBrief != filepath.Join(taskTmp, "brief.md") {
		t.Errorf("capsule brief = %q, want the capsule-augmented brief in the task's own directory", capsuleBrief)
	}
	capsule, err := os.ReadFile(filepath.Join(taskTmp, "task-capsule.json"))
	if err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		TaskClass string `json:"task_class"`
		RouteLane string `json:"route_lane"`
		Budget    struct {
			MaxRepairRounds int `json:"max_repair_rounds"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(capsule, &frozen); err != nil {
		t.Fatal(err)
	}
	if frozen.TaskClass != "implementation" || frozen.RouteLane != "open" || frozen.Budget.MaxRepairRounds != 4 {
		t.Errorf("capsule = %+v, want the frozen class, lane, and repair budget", frozen)
	}
}

const codexDeepTable = `{"rules":[],"default_lane":"build","escalate_to":"deep","lanes":{
	"deep":{"harness":"codex","model":"gpt-5.5","effort":"xhigh","note":"high risk"},
	"build":{"harness":"claude","model":"opus","effort":"high","note":"ordinary"}}}`

func TestRunSpawnFallsToTheNextUsableLaneWhenTheWantedOneIsExhausted(t *testing.T) {
	// The machine on 2026-09-17: codex exhausted_now until the 20th, claude
	// fresh. A high-risk brief wants deep, which is codex here.
	h, deps := routedRuntime(t, codexDeepTable)
	deps.quota = fixtureQuota(t, "exhausted")
	got := captureSpawn(&deps, "spawned g15")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g15", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "claude" || got.Model != "opus" || got.Effort != "high" {
		t.Errorf("request = %+v, want the build lane used instead of the exhausted deep lane", *got)
	}
	want := "spawned g15\nrouted lane=build wanted=deep class=security risk=high source=fleet table " + filepath.Join(h.Data, "routing.json") +
		" quota=deep: codex all_models exhausted_now, resets 2026-09-20T12:17:37Z; build: claude all_models 97% remaining, runway through_reset\n"
	if stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunSpawnRefusesWhenNoLaneIsUsable(t *testing.T) {
	_, deps := routedRuntime(t, `{"rules":[],"default_lane":"build","lanes":{"build":{"harness":"codex","model":"gpt-5.5","effort":"high"}}}`)
	deps.quota = fixtureQuota(t, "exhausted")
	called := false
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		called = true
		return spawn.Result{}, nil
	}
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g16", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps)
	if exit != 1 || called {
		t.Fatalf("exit = %d called = %v, want a refusal before anything is spawned", exit, called)
	}
	if want := "cfo spawn: no usable lane: build: codex all_models exhausted_now, resets 2026-09-20T12:17:37Z\n"; stderr.String() != want || stdout.Len() != 0 {
		t.Errorf("stderr = %q stdout = %q, want %q", stderr.String(), stdout.String(), want)
	}
}

func TestRunSpawnKeepsALaneThatIsOnlyProjectedToExhaust(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	deps.quota = fixtureQuota(t, "projected")
	got := captureSpawn(&deps, "spawned g17")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g17", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if got.Model != "fable" {
		t.Errorf("request = %+v, want the deep lane kept: projected exhaustion is not exhausted now", *got)
	}
	if !strings.Contains(stdout.String(), "routed lane=deep class=security risk=high source=fleet table ") || !strings.Contains(stdout.String(), " quota=deep: claude model:fable 12% remaining, runway projected_exhaustion, projected exhausted 2026-09-17T14:05:00Z\n") {
		t.Errorf("stdout = %q, want the runway reported on the lane it applies to", stdout.String())
	}
}

func TestRunSpawnRoutesAsConfiguredWhenQuotaAxiIsMissing(t *testing.T) {
	_, deps := routedRuntime(t, codexDeepTable)
	deps.quota = func(context.Context) (quota.Report, string) {
		return quota.Report{}, `axi: quota-axi --json: exec: "quota-axi": executable file not found in %PATH%`
	}
	got := captureSpawn(&deps, "spawned g18")
	brief := briefWith(t, "Security review of the session cookie signing before launch.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g18", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "codex" {
		t.Errorf("request = %+v, want the configured deep lane with no quota evidence", *got)
	}
	if !strings.Contains(stdout.String(), `routed lane=deep class=security risk=high source=fleet table `) || !strings.Contains(stdout.String(), ` quota=check skipped (axi: quota-axi --json: exec: "quota-axi": executable file not found in %PATH%)`) {
		t.Errorf("stdout = %q, want the skipped check said out loud", stdout.String())
	}
}

func TestRunSpawnWarnsAnExplicitHarnessThatIsExhaustedWithoutBlocking(t *testing.T) {
	_, deps := routedRuntime(t, shippedRoutingJSON(t))
	deps.quota = fixtureQuota(t, "exhausted")
	got := captureSpawn(&deps, "spawned g19")
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	if exit := runWithRuntime([]string{"spawn", "g19", "--project", `C:\project`, "--brief", brief, "--harness", "codex"}, &stdout, &stderr, deps); exit != 0 {
		t.Fatalf("exit = %d; stderr=%s", exit, stderr.String())
	}
	if string(got.Harness) != "codex" {
		t.Errorf("request = %+v, want the explicit harness dispatched anyway", *got)
	}
	if want := "spawned g19\nrouted lane=explicit class=implementation risk=normal source=--harness flag quota=codex all_models exhausted_now, resets 2026-09-20T12:17:37Z; explicit --harness wins\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunSpawnRefusesToRouteWithoutLanes(t *testing.T) {
	deps := testCommandRuntime(t)
	called := false
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		called = true
		return spawn.Result{}, nil
	}
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g20", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps)
	if exit != 1 || called || !strings.Contains(stderr.String(), "no execution lanes") {
		t.Errorf("exit = %d called = %v stderr = %q, want a refusal naming the missing lanes", exit, called, stderr.String())
	}
}

func TestRunSpawnRefusesALaneNamingAnUnknownHarness(t *testing.T) {
	_, deps := routedRuntime(t, `{"rules":[],"default_lane":"build","lanes":{"build":{"harness":"gemini"}}}`)
	called := false
	deps.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
		called = true
		return spawn.Result{}, nil
	}
	brief := briefWith(t, "Add a dark-mode toggle to the settings page.")

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"spawn", "g21", "--project", `C:\project`, "--brief", brief}, &stdout, &stderr, deps)
	if exit != 1 || called || !strings.Contains(stderr.String(), `lane "build" names harness "gemini"`) {
		t.Errorf("exit = %d called = %v stderr = %q", exit, called, stderr.String())
	}
}
