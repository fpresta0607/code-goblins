package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

func TestSpawnRejectsInvalidIDBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state-does-not-exist")
	service := Service{StateDir: stateDir}

	_, err := service.Spawn(context.Background(), Request{ID: "../escape"})
	if err == nil || !strings.Contains(err.Error(), "task ID") {
		t.Fatalf("Spawn invalid ID error = %v, want task ID refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid ID created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRejectsTrailingDotIDBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state-does-not-exist")
	service := Service{StateDir: stateDir}

	_, err := service.Spawn(context.Background(), Request{ID: "task."})
	if err == nil || !strings.Contains(err.Error(), "must not end with '.'") {
		t.Fatalf("Spawn trailing-dot ID error = %v, want task ID refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("trailing-dot ID created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRefusesMissingBriefAndDeliveryMismatchBeforeLock(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "missing-brief",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "brief") {
		t.Fatalf("Spawn missing brief error = %v, want brief refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing brief created state directory: stat error = %v", statErr)
	}

	writeFile(t, brief, "Delivery contract: mode=direct-PR\n")
	_, err = service.Spawn(context.Background(), Request{
		ID:        "mode-mismatch",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "delivery mismatch") {
		t.Fatalf("Spawn delivery mismatch error = %v, want delivery mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, ".spawn-mode-mismatch.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("delivery mismatch acquired a spawn lock: stat error = %v", statErr)
	}
}

func TestSpawnRefusesUnsupportedHarnessBeforeFilesystemMutation(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, "Delivery contract: mode=no-mistakes\n")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "unsupported",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Kind("grok"),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported harness") {
		t.Fatalf("Spawn unsupported harness error = %v, want harness refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsupported harness created state directory: stat error = %v", statErr)
	}
}

func TestSpawnRefusesEmptyDeliveryModeLine(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := makeDir(t, filepath.Join(root, "project"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, "Delivery contract: mode=\n")
	service := Service{StateDir: stateDir, Project: project}

	_, err := service.Spawn(context.Background(), Request{
		ID:        "empty-contract",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Harness:   harness.Claude,
	})
	if err == nil || !strings.Contains(err.Error(), "delivery contract") {
		t.Fatalf("Spawn empty delivery contract error = %v, want malformed contract refusal", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("empty delivery contract created state directory: stat error = %v", statErr)
	}
}

func TestSpawnShipPublishesMetadataAndLaunchesInOrder(t *testing.T) {
	fixture := newFixture(t)
	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	wantOutput := fmt.Sprintf("spawned task-7 harness=claude kind=ship mode=no-mistakes yolo=on window=fleet:pane-1 worktree=%s", fixture.worktree)
	if result.Output != wantOutput {
		t.Errorf("Output = %q\nwant %q", result.Output, wantOutput)
	}
	if result.Endpoint != (herdr.Endpoint{
		Target:      herdr.Target{Session: "fleet", Pane: "pane-1"},
		WorkspaceID: "workspace-1",
		TabID:       "tab-1",
		PaneID:      "pane-1",
	}) {
		t.Errorf("Endpoint = %#v, want created Herdr endpoint", result.Endpoint)
	}

	meta, err := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID)
	if err != nil {
		t.Fatalf("ReadTaskMeta: %v", err)
	}
	if meta.Window != "fleet:pane-1" || meta.EndpointTaskID != fixture.request.ID || meta.Worktree != fixture.worktree || meta.Project != fixture.project || meta.Harness != "claude" || meta.Kind != "ship" || meta.Mode != "no-mistakes" || meta.Yolo != "on" || meta.Model != "model-a" || meta.Effort != "high" || meta.Backend != "herdr" || meta.HerdrSession != "fleet" || meta.HerdrWorkspaceID != "workspace-1" || meta.HerdrTabID != "tab-1" || meta.HerdrPaneID != "pane-1" {
		t.Errorf("metadata = %+v, want complete ship record", meta)
	}
	if meta.TaskTmp == "" || meta.SpawnGen == "" {
		t.Errorf("metadata = %+v, want tasktmp and spawn generation", meta)
	}
	if info, statErr := os.Stat(filepath.Join(meta.TaskTmp, "gotmp")); statErr != nil || !info.IsDir() {
		t.Fatalf("GOTMPDIR = %q, stat = %v, want existing directory", filepath.Join(meta.TaskTmp, "gotmp"), statErr)
	}
	if got, want := sortedKeys(t, fixture.stateDir, fixture.request.ID), []string{"backend", "brief", "effort", "endpoint_task_id", "harness", "herdr_pane_id", "herdr_session", "herdr_tab_id", "herdr_workspace_id", "kind", "mode", "model", "project", "spawn_gen", "tasktmp", "window", "worktree", "yolo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("metadata keys = %v, want %v", got, want)
	}

	if got, want := fixture.events, []string{
		"status",
		"schema",
		"status",
		"session-list",
		"agent-manifests",
		"workspace-list",
		"tab-list",
		"tab-create",
		"treehouse-get",
		"validate-worktree",
		"freshen",
		"validate-harness",
		"build-harness",
		"send-literal",
		"settle",
		"send-enter",
		"settle",
		"agent-start",
		"capture",
		"settle",
		"capture",
		"settle",
		"send-literal",
		"settle",
		"capture",
		"send-enter",
		"agent-working",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("operation order = %v\nwant %v", got, want)
	}
	if got, want := fixture.runner.literals[0], "Set-Location -LiteralPath '"+fixture.worktree+"'; $env:CFO_STATE_OVERRIDE = '"+fixture.stateDir+"'; $env:GOTMPDIR = '"+filepath.Join(meta.TaskTmp, "gotmp")+"'"; got != want {
		t.Errorf("launch prefix = %q\nwant %q", got, want)
	}
	if got, want := fixture.runner.startName, "gb-task-7"; got != want {
		t.Errorf("agent start name = %q, want %q", got, want)
	}
	if got, want := fixture.runner.startKind, "claude"; got != want {
		t.Errorf("agent start kind = %q, want %q", got, want)
	}
	if got, want := fixture.runner.startArgs, []string{"--dangerously-skip-permissions"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agent start args = %q, want %q", got, want)
	}
	if got, want := fixture.runner.literals[1], "Read the brief at "+fixture.brief+" and follow it exactly."; !strings.HasPrefix(got, want) {
		t.Errorf("delivered instruction = %q\nwant prefix %q", got, want)
	}
	if got := fixture.runner.enterKeys; got != 2 {
		t.Errorf("Enter sends = %d, want prefix submit plus instruction submit", got)
	}
	if _, statErr := os.Stat(filepath.Join(fixture.stateDir, ".spawn.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("spawn lock persists after success: stat error = %v", statErr)
	}
}

func TestSpawnScoutOmitsShipFields(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.ID = "scout-7"
	fixture.request.Kind = "scout"
	fixture.request.Mode = ""
	fixture.request.Yolo = false
	writeFile(t, fixture.brief, "Investigate the reported behavior.\n")

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn scout: %v", err)
	}
	wantOutput := fmt.Sprintf("spawned scout-7 harness=claude kind=scout window=fleet:pane-1 worktree=%s", fixture.worktree)
	if result.Output != wantOutput {
		t.Errorf("Output = %q\nwant %q", result.Output, wantOutput)
	}
	if result.Meta.Mode != "" || result.Meta.Yolo != "" {
		t.Errorf("scout metadata = %+v, want omitted mode and yolo", result.Meta)
	}
	if got, want := sortedKeys(t, fixture.stateDir, fixture.request.ID), []string{"backend", "brief", "effort", "endpoint_task_id", "harness", "herdr_pane_id", "herdr_session", "herdr_tab_id", "herdr_workspace_id", "kind", "model", "project", "spawn_gen", "tasktmp", "window", "worktree"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scout metadata keys = %v, want %v", got, want)
	}
}

func TestSpawnUsesRequestedHerdrSession(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.Session = "task-session"
	fixture.runner.session = "task-session"

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if result.Endpoint.Target.Session != "task-session" || result.Meta.HerdrSession != "task-session" || !strings.Contains(result.Output, "window=task-session:pane-1") {
		t.Errorf("Spawn did not use requested session: endpoint=%+v meta=%+v output=%q", result.Endpoint, result.Meta, result.Output)
	}
}

func TestSpawnRefusesDirtyWorktreeWithoutLaunching(t *testing.T) {
	fixture := newFixture(t)
	fixture.git.freshenErr = errors.New("treehouse: worktree is dirty")
	primaryMarker := filepath.Join(fixture.project, "primary-marker.txt")
	writeFile(t, primaryMarker, "unchanged")

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("Spawn dirty worktree error = %v, want dirty refusal", err)
	}
	if fixture.runner.literal != "" || fixture.runner.enterKeys != 0 {
		t.Fatalf("dirty worktree launched a harness: literal=%q enter=%d", fixture.runner.literal, fixture.runner.enterKeys)
	}
	if got, readErr := os.ReadFile(primaryMarker); readErr != nil || string(got) != "unchanged" {
		t.Fatalf("primary project changed after dirty refusal: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(fixture.worktree); statErr != nil {
		t.Fatalf("dirty refusal removed acquired worktree: %v", statErr)
	}
	if fixture.git.returned != 1 {
		t.Fatalf("dirty refusal returned the lease %d times, want 1", fixture.git.returned)
	}
}

func TestSpawnLaunchTimeoutAdoptsALivePane(t *testing.T) {
	fixture := newFixture(t)
	// The agent never reports "working", but is alive and idle: a healthy
	// harness sitting at its prompt. Spawn must adopt it, not declare failure
	// and orphan a live pane.
	fixture.runner.agentStatus = "idle"

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(result.Output, "spawned task-7") {
		t.Errorf("Output = %q, want the live pane adopted as launched", result.Output)
	}
	if _, statErr := os.Stat(fixture.worktree); statErr != nil {
		t.Fatalf("adopted launch removed worktree: %v", statErr)
	}
	if fixture.git.returned != 0 {
		t.Fatalf("adopted launch returned the lease %d times, want 0", fixture.git.returned)
	}
	if _, metaErr := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID); metaErr != nil {
		t.Fatalf("adopted launch lost metadata: %v", metaErr)
	}
}

func TestSpawnLaunchFailureTearsDownPaneAndWorktree(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.agentNotFound = true

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "did not report working") {
		t.Fatalf("Spawn launch verification error = %v, want working refusal", err)
	}
	status, readErr := state.TailStatus(fixture.stateDir, fixture.request.ID, 2)
	if readErr != nil {
		t.Fatalf("TailStatus: %v", readErr)
	}
	if got, want := status, []string{"failed: " + err.Error()}; !reflect.DeepEqual(got, want) {
		t.Errorf("status = %v, want %v", got, want)
	}
	if fixture.git.returned != 1 {
		t.Fatalf("launch failure returned the lease %d times, want 1 for a proven-dead agent", fixture.git.returned)
	}
	if _, metaErr := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID); !errors.Is(metaErr, os.ErrNotExist) {
		t.Fatalf("launch failure left metadata behind: %v", metaErr)
	}
	if !slices.Contains(fixture.events, "tab-close") {
		t.Errorf("events = %v, want the tab closed on a proven-dead launch", fixture.events)
	}
	if marker, readErr := os.ReadFile(filepath.Join(fixture.project, "primary-marker.txt")); readErr != nil || string(marker) != "unchanged" {
		t.Fatalf("launch failure rewrote primary project: %q, %v", marker, readErr)
	}
}

func TestSpawnConfirmsBlockingTrustDialogThenLaunches(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.agentStatus = "blocked"
	fixture.runner.trustDialog = true

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn with trust dialog: %v", err)
	}
	if !strings.Contains(result.Output, "spawned task-7") {
		t.Errorf("Output = %q, want successful spawn", result.Output)
	}
	if got, want := fixture.runner.keys, []string{"enter", "enter", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want prefix submit, trust confirmation, then instruction submit", got)
	}
	if !slices.Contains(fixture.events, "capture") {
		t.Errorf("events = %v, want a pane capture before trust confirmation", fixture.events)
	}
}

func TestSpawnConfirmsDialogWithAdapterKeys(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.agentStatus = "blocked"
	fixture.runner.trustDialog = true
	fixture.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &fixture.events, confirmKeys: []string{"up", "enter"}}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn with adapter dialog keys: %v", err)
	}
	if !strings.Contains(result.Output, "spawned task-7") {
		t.Errorf("Output = %q, want successful spawn", result.Output)
	}
	if got, want := fixture.runner.keys, []string{"enter", "up", "enter", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want prefix submit then adapter confirm keys then instruction submit", got)
	}
}

func TestSpawnAgentStartFailureReturnsWorktree(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.startErr = errors.New("herdr binary unavailable")

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "herdr binary unavailable") {
		t.Fatalf("Spawn error = %v, want runner failure surfaced", err)
	}
	if got := fixture.runner.prompt; got != "" {
		t.Errorf("prompt = %q, want no prompt after a failed agent start", got)
	}
	if fixture.git.returned != 1 {
		t.Fatalf("agent start failure returned the lease %d times, want 1 before any agent launched", fixture.git.returned)
	}
}

func TestSpawnStartsNamedAgentThenPromptsNatively(t *testing.T) {
	fixture := newFixture(t)

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(result.Output, "spawned task-7") {
		t.Errorf("Output = %q, want successful spawn", result.Output)
	}
	if len(fixture.runner.literals) != 2 || !strings.Contains(fixture.runner.literals[0], "Set-Location") || !strings.Contains(fixture.runner.literals[1], "Read the brief") {
		t.Fatalf("literals = %q, want the launch prefix then the delivered instruction", fixture.runner.literals)
	}
	start := slices.Index(fixture.events, "agent-start")
	working := slices.Index(fixture.events, "agent-working")
	if start < 0 || working < 0 || !(start < working) {
		t.Errorf("events = %v, want agent-start before agent-working", fixture.events)
	}
}

func TestSpawnPiTypedLaunchTypesFullCommandAndSkipsNativeStart(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.Harness = harness.Pi
	fixture.service.Harness.Adapters = map[harness.Kind]harness.Adapter{
		harness.Pi: typedFixtureAdapter{events: &fixture.events, kind: harness.Pi},
	}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn pi: %v", err)
	}
	if !strings.Contains(result.Output, "harness=pi") {
		t.Errorf("Output = %q, want pi harness", result.Output)
	}
	if got := len(fixture.runner.literals); got != 1 {
		t.Fatalf("literals = %q, want exactly one typed launch line", fixture.runner.literals)
	}
	wantLine := "Set-Location -LiteralPath '" + fixture.worktree + "'; $env:CFO_STATE_OVERRIDE = '" + fixture.stateDir + "'; $env:GOTMPDIR = '" + filepath.Join(result.Meta.TaskTmp, "gotmp") + "'; & 'pi' '--tui-mode' 'regular' 'Read the brief at " + fixture.brief + " and follow it exactly."
	if got := fixture.runner.literal; !strings.HasPrefix(got, wantLine) {
		t.Errorf("typed launch line = %q\nwant prefix %q", got, wantLine)
	}
	if fixture.runner.startName != "" || fixture.runner.startKind != "" || fixture.runner.startArgs != nil {
		t.Errorf("typed launch used native agent start: name=%q kind=%q args=%q", fixture.runner.startName, fixture.runner.startKind, fixture.runner.startArgs)
	}
	if fixture.runner.prompt != "" {
		t.Errorf("typed launch sent a native agent prompt: %q", fixture.runner.prompt)
	}
	if slices.Contains(fixture.events, "agent-start") || slices.Contains(fixture.events, "agent-prompt") {
		t.Errorf("events = %v, want typed launch without native start or prompt", fixture.events)
	}
	if !slices.Contains(fixture.events, "send-enter") || !slices.Contains(fixture.events, "agent-working") {
		t.Errorf("events = %v, want typed submit followed by working confirmation", fixture.events)
	}
}

func TestSpawnPiTypedLaunchConfirmsTrustDialog(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.Harness = harness.Pi
	fixture.request.Model = ""
	fixture.request.Effort = ""
	fixture.runner.agentStatus = "blocked"
	fixture.runner.trustDialog = true
	fixture.runner.trustDialogText = "Accessing workspace:\n\n Trust project folder?\n"
	fixture.service.Harness.Adapters = map[harness.Kind]harness.Adapter{
		harness.Pi: harness.DefaultRegistry().Adapters[harness.Pi],
	}

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn pi with trust dialog: %v", err)
	}
	if !strings.Contains(result.Output, "harness=pi") {
		t.Errorf("Output = %q, want pi harness", result.Output)
	}
	if got, want := fixture.runner.keys, []string{"enter", "enter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("keys = %v, want typed launch submit then pi trust confirmation", got)
	}
}

func TestSpawnNormalizesLaunchFailureStatusToOneFailedEvent(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.agentErr = errors.New("preview unavailable\r\ndone: forged")

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "preview unavailable") {
		t.Fatalf("Spawn launch verification error = %v, want propagated Herdr error", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(fixture.stateDir, fixture.request.ID+".status"))
	if readErr != nil {
		t.Fatalf("ReadFile status: %v", readErr)
	}
	status := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), "\n")
	if len(status) != 1 {
		t.Fatalf("raw status = %q; status events = %q, want exactly one", string(raw), status)
	}
	verb, _, found := strings.Cut(status[0], ":")
	if !found || verb != "failed" {
		t.Fatalf("status event = %q, want one failed event", status[0])
	}
	if strings.ContainsAny(status[0], "\r\n") || strings.HasPrefix(status[0], "done:") {
		t.Fatalf("status event permits forged status line: %q", status[0])
	}
}

func TestSpawnRejectsMetadataBearingControlsBeforeHerdrMutation(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Request)
	}{
		{"project", func(req *Request) { req.Project = "C:\\project\nbad" }},
		{"brief", func(req *Request) { req.BriefPath = "C:\\brief\nbad" }},
		{"kind", func(req *Request) { req.Kind = "ship\nbad" }},
		{"mode", func(req *Request) { req.Mode = "no-mistakes\nbad" }},
		{"harness", func(req *Request) { req.Harness = harness.Kind("claude\nbad") }},
		{"model", func(req *Request) { req.Model = "x\nherdr_pane_id=other" }},
		{"effort", func(req *Request) { req.Effort = "high\rmalformed" }},
		{"session", func(req *Request) { req.Session = "fleet\nbad" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			test.set(&fixture.request)

			_, err := fixture.service.Spawn(context.Background(), fixture.request)
			if err == nil || !strings.Contains(err.Error(), "control character") {
				t.Fatalf("Spawn control injection error = %v, want control character refusal", err)
			}
			if fixture.runner.calls != 0 || fixture.runner.literal != "" || fixture.runner.enterKeys != 0 || len(fixture.events) != 0 {
				t.Fatalf("control injection mutated Herdr: calls=%d literal=%q enter=%d events=%v", fixture.runner.calls, fixture.runner.literal, fixture.runner.enterKeys, fixture.events)
			}
			if _, statErr := os.Stat(filepath.Join(fixture.stateDir, fixture.request.ID+".meta")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Spawn wrote injected metadata: stat error = %v", statErr)
			}
		})
	}
}

func TestSpawnRejectsMalformedHerdrIDsBeforeDownstreamWork(t *testing.T) {
	tests := []struct {
		name   string
		set    func(*herdrRunner)
		events []string
	}{
		{"container", func(r *herdrRunner) { r.workspaceID = "workspace-1\nbad" }, []string{"status", "schema", "status", "session-list", "agent-manifests", "workspace-list"}},
		{"endpoint", func(r *herdrRunner) { r.paneID = "pane-1\nbad" }, []string{"status", "schema", "status", "session-list", "agent-manifests", "workspace-list", "tab-list", "tab-create"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			test.set(fixture.runner)

			_, err := fixture.service.Spawn(context.Background(), fixture.request)
			if err == nil || !strings.Contains(err.Error(), "control character") {
				t.Fatalf("Spawn malformed Herdr ID error = %v, want control character refusal", err)
			}
			if slices.Contains(fixture.events, "treehouse-get") || fixture.runner.literal != "" || fixture.runner.enterKeys != 0 {
				t.Fatalf("malformed Herdr ID reached downstream work: literal=%q enter=%d events=%v", fixture.runner.literal, fixture.runner.enterKeys, fixture.events)
			}
			if !reflect.DeepEqual(fixture.events, test.events) {
				t.Fatalf("malformed Herdr ID events = %v, want %v", fixture.events, test.events)
			}
		})
	}
}

func TestSpawnPostAcquisitionFailuresReturnPartialResultAndStatus(t *testing.T) {
	tests := []struct {
		name string
		set  func(*fixture)
		want string
	}{
		{
			name: "freshen",
			set:  func(f *fixture) { f.git.freshenErr = errors.New("treehouse: worktree is dirty") },
			want: "worktree is dirty",
		},
		{
			name: "build",
			set: func(f *fixture) {
				f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("harness build refused")}
			},
			want: "harness build refused",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newFixture(t)
			test.set(fixture)

			result, err := fixture.service.Spawn(context.Background(), fixture.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Spawn error = %v, want %q", err, test.want)
			}
			if result.Meta.Worktree != fixture.worktree || result.Meta.Project != fixture.project || result.Endpoint.Target != (herdr.Target{Session: "fleet", Pane: "pane-1"}) {
				t.Fatalf("partial result = %+v endpoint=%+v, want worktree, project, and target", result.Meta, result.Endpoint)
			}
			status, statusErr := state.TailStatus(fixture.stateDir, fixture.request.ID, 2)
			if statusErr != nil || len(status) != 1 || !strings.HasPrefix(status[0], "failed: ") || !strings.Contains(status[0], test.want) {
				t.Fatalf("status = %v, %v; want one failed event containing %q", status, statusErr, test.want)
			}
			if _, metaErr := state.ReadTaskMeta(fixture.stateDir, fixture.request.ID); !errors.Is(metaErr, os.ErrNotExist) {
				t.Fatalf("post-acquisition failure wrote success metadata: %v", metaErr)
			}
			if fixture.runner.literal != "" || fixture.runner.enterKeys != 0 {
				t.Fatalf("post-acquisition failure launched harness: literal=%q enter=%d", fixture.runner.literal, fixture.runner.enterKeys)
			}
			if fixture.git.returned != 1 {
				t.Fatalf("post-acquisition failure returned the lease %d times, want 1", fixture.git.returned)
			}
		})
	}
}

func TestSpawnPostAcquireFailureSurfacesReturnError(t *testing.T) {
	fixture := newFixture(t)
	fixture.git.freshenErr = errors.New("treehouse: worktree is dirty")
	fixture.git.returnErr = errors.New("treehouse: return refused")

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "worktree is dirty") || !strings.Contains(err.Error(), "return refused") {
		t.Fatalf("Spawn error = %v, want joined launch and return failures", err)
	}
	if fixture.git.returned != 1 {
		t.Fatalf("post-acquire failure returned the lease %d times, want 1", fixture.git.returned)
	}
}

func TestSpawnSurfacesTaskLockReleaseFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.service.ReleaseLock = func(string, string) error {
			return errors.New("lock is still shared")
		}

		result, err := fixture.service.Spawn(context.Background(), fixture.request)
		if err == nil || !strings.Contains(err.Error(), "release spawn lock") || !strings.Contains(err.Error(), "lock is still shared") {
			t.Fatalf("Spawn release error = %v, want surfaced cleanup failure", err)
		}
		if result.Meta.Worktree != fixture.worktree || result.Endpoint.Target.Pane != "pane-1" {
			t.Fatalf("success result was discarded by cleanup error: %+v endpoint=%+v", result.Meta, result.Endpoint)
		}
		if err := lock.ReleaseNamed(fixture.stateDir, spawnLockName); err != nil {
			t.Fatalf("ReleaseNamed cleanup: %v", err)
		}
	})

	t.Run("primary failure", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.runner.agentNotFound = true
		fixture.service.ReleaseLock = func(string, string) error {
			return errors.New("lock is still shared")
		}

		result, err := fixture.service.Spawn(context.Background(), fixture.request)
		if err == nil || !strings.Contains(err.Error(), "did not report working") || !strings.Contains(err.Error(), "release spawn lock") {
			t.Fatalf("Spawn release error = %v, want joined primary and cleanup failures", err)
		}
		if result.Meta.Worktree != fixture.worktree || result.Endpoint.Target.Pane != "pane-1" {
			t.Fatalf("primary failure discarded recovery result: %+v endpoint=%+v", result.Meta, result.Endpoint)
		}
		if err := lock.ReleaseNamed(fixture.stateDir, spawnLockName); err != nil {
			t.Fatalf("ReleaseNamed cleanup: %v", err)
		}
	})
}

func TestSpawnRejectsMissingHerdrIDsAndContendedLockThenReleases(t *testing.T) {
	t.Run("missing Herdr IDs", func(t *testing.T) {
		fixture := newFixture(t)
		fixture.runner.missingPaneID = true
		_, err := fixture.service.Spawn(context.Background(), fixture.request)
		if err == nil || !strings.Contains(err.Error(), "root pane_id") {
			t.Fatalf("Spawn missing pane ID error = %v, want Herdr ID refusal", err)
		}
		if _, statErr := os.Stat(filepath.Join(fixture.stateDir, fixture.request.ID+".meta")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("missing Herdr IDs wrote metadata: stat error = %v", statErr)
		}
	})

	t.Run("contention releases", func(t *testing.T) {
		fixture := newFixture(t)
		if _, err := lock.AcquireExclusiveNamed(fixture.stateDir, spawnLockName); err != nil {
			t.Fatalf("AcquireExclusiveNamed setup: %v", err)
		}
		_, err := fixture.service.Spawn(context.Background(), fixture.request)
		if !errors.Is(err, lock.ErrHeld) {
			t.Fatalf("Spawn contention error = %v, want ErrHeld", err)
		}
		if err := lock.ReleaseNamed(fixture.stateDir, spawnLockName); err != nil {
			t.Fatalf("ReleaseNamed setup lock: %v", err)
		}
		if _, err := fixture.service.Spawn(context.Background(), fixture.request); err != nil {
			t.Fatalf("Spawn after lock release: %v", err)
		}
	})
}

func TestSpawnRejectsCaseAliasBeforeHerdrOrWorktreeMutation(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.ID = "Foo"
	first, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("first Spawn: %v", err)
	}
	metaPath := filepath.Join(fixture.stateDir, "Foo.meta")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := fixture.runner.calls

	second := fixture.request
	second.ID = "foo"
	if _, err := fixture.service.Spawn(context.Background(), second); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("case-alias Spawn error = %v, want case-insensitive collision refusal", err)
	}
	if fixture.runner.calls != calls {
		t.Errorf("Herdr calls = %d after alias rejection, want %d before any second task mutation", fixture.runner.calls, calls)
	}
	after, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("Foo metadata changed after foo rejection\n got: %q\nwant: %q", after, before)
	}
	if first.Meta.ID != "Foo" {
		t.Errorf("first metadata ID = %q, want Foo", first.Meta.ID)
	}
}

func TestSpawnRejectsCaseInsensitiveMetadataExtensionBeforeHerdrOrWorktreeMutation(t *testing.T) {
	fixture := newFixture(t)
	writeFile(t, filepath.Join(fixture.stateDir, "Foo.META"), "window=fleet:pane-1\n")

	request := fixture.request
	request.ID = "foo"
	if _, err := fixture.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("case-insensitive extension Spawn error = %v, want collision refusal", err)
	}
	if fixture.runner.calls != 0 {
		t.Errorf("Herdr calls = %d, want 0 before metadata-alias refusal", fixture.runner.calls)
	}
}

func TestSpawnRejectsCaseAliasOfRetainedFailedTaskBeforeHerdrOrWorktreeMutation(t *testing.T) {
	fixture := newFixture(t)
	fixture.request.ID = "Foo"
	fixture.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &fixture.events, buildErr: errors.New("harness build refused")}

	if _, err := fixture.service.Spawn(context.Background(), fixture.request); err == nil || !strings.Contains(err.Error(), "harness build refused") {
		t.Fatalf("failed Foo Spawn error = %v, want post-acquisition build failure", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, "Foo.status")); err != nil {
		t.Fatalf("failed Foo status: %v", err)
	}
	if info, err := os.Stat(filepath.Join(fixture.stateDir, "tasktmp", "Foo")); err != nil || !info.IsDir() {
		t.Fatalf("failed Foo tasktmp: info=%v err=%v, want retained directory", info, err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, "Foo.meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed Foo unexpectedly wrote metadata: %v", err)
	}
	calls := fixture.runner.calls

	request := fixture.request
	request.ID = "foo"
	if _, err := fixture.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("failed-task alias Spawn error = %v, want collision refusal", err)
	}
	if fixture.runner.calls != calls {
		t.Errorf("Herdr calls = %d after alias rejection, want %d before a second task mutation", fixture.runner.calls, calls)
	}
}

func TestSpawnRejectsUnsupportedHerdrKindBeforeMutation(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.manifests = []string{"codex", "pi", "kimi"}

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), `does not support harness kind "claude"`) {
		t.Fatalf("Spawn unsupported Herdr kind error = %v, want kind refusal", err)
	}
	if got, want := fixture.events, []string{"status", "schema", "status", "session-list", "agent-manifests"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want preflight plus kind discovery only", got)
	}
	if fixture.runner.literal != "" || fixture.runner.enterKeys != 0 || fixture.git.returned != 0 {
		t.Fatalf("unsupported kind mutated Herdr or treehouse: literal=%q enter=%d returned=%d", fixture.runner.literal, fixture.runner.enterKeys, fixture.git.returned)
	}
}

func TestSpawnConfirmHarnessDialogsFailsFastOnTerminalCaptureError(t *testing.T) {
	fixture := newFixture(t)
	fixture.runner.trustDialog = true
	fixture.runner.captureErr = errors.New("herdr binary unavailable")

	_, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err == nil || !strings.Contains(err.Error(), "herdr binary unavailable") {
		t.Fatalf("Spawn terminal capture error = %v, want runner failure surfaced", err)
	}
	captures := 0
	for _, event := range fixture.events {
		if event == "capture" {
			captures++
		}
	}
	if captures != 1 {
		t.Fatalf("capture attempts = %d, want one fail-fast capture", captures)
	}
	if strings.Contains(err.Error(), "did not clear") {
		t.Fatalf("error = %v, want underlying failure instead of dialog timeout", err)
	}
}

type fixture struct {
	service  Service
	request  Request
	stateDir string
	project  string
	worktree string
	brief    string
	events   []string
	runner   *herdrRunner
	git      *treehouseGit
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	stateDir := makeDir(t, filepath.Join(root, "state"))
	project := makeDir(t, filepath.Join(root, "primary"))
	worktree := makeDir(t, filepath.Join(root, "worktree"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, "Delivery contract: mode=no-mistakes\nDo the work.\n")
	writeFile(t, filepath.Join(project, "primary-marker.txt"), "unchanged")

	fixture := &fixture{stateDir: stateDir, project: project, worktree: worktree, brief: brief}
	fixture.runner = &herdrRunner{events: &fixture.events, worktree: worktree, agentStatus: "working", manifests: []string{"claude", "codex", "pi", "kimi"}}
	fixture.git = &treehouseGit{events: &fixture.events, top: worktree}
	fixture.service = Service{
		Herdr: &herdr.Client{
			Commands: fixture.runner,
			Session:  "fleet",
			Sleep:    func(context.Context, time.Duration) error { return nil },
		},
		Treehouse: treehouse.Service{
			Commands: fixture.runner,
			Git:      fixture.git,
			Sleep:    func(context.Context, time.Duration) error { return nil },
		},
		Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
			harness.Claude: fixtureAdapter{events: &fixture.events},
		}},
		StateDir: stateDir,
		Project:  project,
		Sleep: func(context.Context, time.Duration) error {
			fixture.events = append(fixture.events, "settle")
			return nil
		},
	}
	fixture.request = Request{
		ID:        "task-7",
		Project:   project,
		BriefPath: brief,
		Kind:      "ship",
		Mode:      "no-mistakes",
		Yolo:      true,
		Harness:   harness.Claude,
		Model:     "model-a",
		Effort:    "high",
		Session:   "fleet",
	}
	return fixture
}

type fixtureAdapter struct {
	events      *[]string
	buildErr    error
	confirmKeys []string
}

type typedFixtureAdapter struct {
	events *[]string
	kind   harness.Kind
}

// Control gives the fixture a resume so switch tests can exercise the
// same-harness path; the stop sequence mirrors a real adapter's shape.
func (a typedFixtureAdapter) Control() harness.Control {
	return harness.Control{StopKeys: []string{"escape"}, StopCommand: "/quit"}
}

func (a typedFixtureAdapter) Kind() harness.Kind {
	return a.kind
}

func (a typedFixtureAdapter) Validate(context.Context, execx.Runner) error {
	*a.events = append(*a.events, "validate-harness")
	return nil
}

func (a typedFixtureAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	*a.events = append(*a.events, "build-harness")
	return harness.Launch{
		Args:        []string{"--tui-mode", "regular"},
		Env:         map[string]string{"GOTMPDIR": filepath.Join(spec.TaskTmp, "gotmp")},
		PromptFile:  spec.BriefPath,
		TypedLaunch: true,
		Executable:  "pi",
	}, nil
}

func (a fixtureAdapter) Control() harness.Control {
	return harness.Control{StopKeys: []string{"escape"}, StopCommand: "/exit", ResumeArgs: []string{"--continue"}}
}

func (a fixtureAdapter) Kind() harness.Kind {
	return harness.Claude
}

func (a fixtureAdapter) Validate(context.Context, execx.Runner) error {
	*a.events = append(*a.events, "validate-harness")
	return nil
}

func (a fixtureAdapter) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	*a.events = append(*a.events, "build-harness")
	if a.buildErr != nil {
		return harness.Launch{}, a.buildErr
	}
	confirmKeys := a.confirmKeys
	if len(confirmKeys) == 0 {
		confirmKeys = []string{"enter"}
	}
	return harness.Launch{
		Args:           []string{"--dangerously-skip-permissions"},
		Env:            map[string]string{"GOTMPDIR": filepath.Join(spec.TaskTmp, "gotmp")},
		PromptFile:     spec.BriefPath,
		ConfirmMarkers: []string{"Is this a project you created or one you trust?"},
		ConfirmKeys:    confirmKeys,
	}, nil
}

type treehouseGit struct {
	events     *[]string
	top        string
	freshenErr error
	returnErr  error
	returned   int
}

func (g *treehouseGit) WorktreeTop(context.Context, string) (string, error) {
	*g.events = append(*g.events, "validate-worktree")
	return g.top, nil
}

func (g *treehouseGit) FetchAndFreshen(context.Context, string) error {
	*g.events = append(*g.events, "freshen")
	return g.freshenErr
}

func (g *treehouseGit) Return(context.Context, string, string) error {
	g.returned++
	return g.returnErr
}

func (g *treehouseGit) EnsureSeeded(context.Context, string) (bool, error) {
	return false, nil
}

type herdrRunner struct {
	events          *[]string
	worktree        string
	session         string
	workspaceID     string
	paneID          string
	agentStatus     string
	agentErr        error
	startErr        error
	captureErr      error
	missingPaneID   bool
	trustDialog     bool
	trustDialogText string
	manifests       []string
	calls           int
	literal         string
	literals        []string
	startName       string
	startKind       string
	startArgs       []string
	prompt          string
	keys            []string
	agentCalls      int
	enterKeys       int
	agentNotFound   bool
	captureCount    int
	corruptCaptureAt int
}

func (r *herdrRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls++
	if req.Name == "treehouse" {
		*r.events = append(*r.events, "treehouse-get")
		if len(req.Args) != 5 || req.Args[0] != "get" || req.Args[1] != "--lease" || req.Args[2] != "--json" || req.Args[3] != "--lease-holder" || !strings.HasPrefix(req.Args[4], "gb-") {
			return execx.Result{}, fmt.Errorf("unexpected treehouse request: %#v", req)
		}
		return execx.Result{Stdout: []byte(`{"path":` + quoteJSON(r.worktree) + `,"lease_id":"lease-1","lease_holder":"gb"}`)}, nil
	}
	wantSession := r.session
	if wantSession == "" {
		wantSession = "fleet"
	}
	if req.Name == "pi" {
		*r.events = append(*r.events, "validate-harness")
		if !reflect.DeepEqual(req.Args, []string{"--help"}) {
			return execx.Result{}, fmt.Errorf("unexpected pi probe: %#v", req)
		}
		return execx.Result{Stdout: []byte("Usage: pi [options]\n\nOptions:\n  --tui-mode <mode>              TUI mode: regular (default) or fullscreen\n")}, nil
	}
	if req.Name != "herdr" {
		return execx.Result{}, fmt.Errorf("unexpected Herdr request: %#v", req)
	}
	args := append([]string{}, req.Args...)
	separator := slices.Index(args, "--")
	sessionAt := slices.Index(args, "--session")
	if sessionAt < 0 || sessionAt+1 >= len(args) || args[sessionAt+1] != wantSession || (separator >= 0 && sessionAt > separator) {
		return execx.Result{}, fmt.Errorf("unexpected Herdr request: %#v", req)
	}
	args = slices.Delete(args, sessionAt, sessionAt+2)
	switch {
	case reflect.DeepEqual(args, []string{"status", "--json"}):
		*r.events = append(*r.events, "status")
		return execx.Result{Stdout: []byte(`{"client":{"protocol":19},"server":{"running":true,"protocol":19,"compatible":true}}`)}, nil
	case reflect.DeepEqual(args, []string{"api", "schema", "--json"}):
		*r.events = append(*r.events, "schema")
		return execx.Result{Stdout: []byte(fixtureSchemaJSON())}, nil
	case reflect.DeepEqual(args, []string{"session", "list", "--json"}):
		*r.events = append(*r.events, "session-list")
		return execx.Result{Stdout: []byte(`{"sessions":[{"name":` + quoteJSON(wantSession) + `,"running":true}]}`)}, nil
	case reflect.DeepEqual(args, []string{"server", "agent-manifests", "--json"}):
		*r.events = append(*r.events, "agent-manifests")
		manifests := r.manifests
		if len(manifests) == 0 {
			manifests = []string{"claude", "codex", "pi", "kimi"}
		}
		entries := make([]string, 0, len(manifests))
		for _, kind := range manifests {
			entries = append(entries, `{"agent":`+quoteJSON(kind)+`}`)
		}
		return jsonResult(`{"manifests":[` + strings.Join(entries, ",") + `]}`), nil
	case reflect.DeepEqual(args, []string{"workspace", "list"}):
		*r.events = append(*r.events, "workspace-list")
		workspaceID := r.workspaceID
		if workspaceID == "" {
			workspaceID = "workspace-1"
		}
		return jsonResult(`{"workspaces":[{"workspace_id":` + quoteJSON(workspaceID) + `,"label":"cfo"}]}`), nil
	case reflect.DeepEqual(args, []string{"tab", "list", "--workspace", "workspace-1"}):
		*r.events = append(*r.events, "tab-list")
		return jsonResult(`{"tabs":[]}`), nil
	case len(args) == 9 && args[0] == "tab" && args[1] == "create":
		*r.events = append(*r.events, "tab-create")
		paneID := r.paneID
		if paneID == "" {
			paneID = "pane-1"
		}
		if r.missingPaneID {
			return jsonResult(`{"tab":{"tab_id":"tab-1"},"root_pane":{}}`), nil
		}
		return jsonResult(`{"tab":{"tab_id":"tab-1"},"root_pane":{"pane_id":` + quoteJSON(paneID) + `}}`), nil
	case len(args) == 4 && args[0] == "pane" && args[1] == "send-text" && args[2] == "pane-1":
		*r.events = append(*r.events, "send-literal")
		r.literal = args[3]
		r.literals = append(r.literals, args[3])
		return jsonResult(`{}`), nil
	case reflect.DeepEqual(args, []string{"pane", "get", "pane-1"}):
		return jsonResult(`{"pane":{"pane_id":"pane-1"}}`), nil
	case len(args) == 4 && args[0] == "pane" && args[1] == "send-keys" && args[2] == "pane-1":
		r.keys = append(r.keys, args[3])
		if args[3] == "enter" {
			*r.events = append(*r.events, "send-enter")
			r.enterKeys++
			if r.trustDialog && r.enterKeys > 1 {
				r.trustDialog = false
				r.agentStatus = "working"
			}
		} else {
			*r.events = append(*r.events, "send-key")
		}
		return jsonResult(`{}`), nil
	case len(args) == 3 && args[0] == "tab" && args[1] == "close":
		*r.events = append(*r.events, "tab-close")
		return jsonResult(`{}`), nil
	case len(args) >= 6 && args[0] == "agent" && args[1] == "start":
		*r.events = append(*r.events, "agent-start")
		if r.startErr != nil {
			return execx.Result{}, r.startErr
		}
		r.startName = args[2]
		for index := 3; index < len(args); index++ {
			switch args[index] {
			case "--kind":
				r.startKind = args[index+1]
				index++
			case "--pane":
				index++
			case "--":
				r.startArgs = append([]string{}, args[index+1:]...)
				index = len(args)
			}
		}
		return jsonResult(`{"agent":{"name":` + quoteJSON(r.startName) + `,"agent_status":"idle"}}`), nil
	case len(args) == 4 && args[0] == "agent" && args[1] == "prompt" && args[2] == "pane-1":
		*r.events = append(*r.events, "agent-prompt")
		r.prompt = args[3]
		return jsonResult(`{"agent":{"agent_status":"working"}}`), nil
	case len(args) >= 3 && args[0] == "pane" && args[1] == "read" && args[2] == "pane-1":
		*r.events = append(*r.events, "capture")
		r.captureCount++
		if r.captureErr != nil {
			return execx.Result{}, r.captureErr
		}
		if r.trustDialog {
			text := r.trustDialogText
			if text == "" {
				text = "Accessing workspace:\n\n Quick safety check: Is this a project you created or\n one you trust?\n"
			}
			return execx.Result{Stdout: []byte(text)}, nil
		}
		if r.literal != "" {
			if r.corruptCaptureAt > 0 && r.captureCount == r.corruptCaptureAt {
				// Simulate the first-instruction corruption: leading characters
				// eaten so the remainder reads as a bogus slash command.
				return execx.Result{Stdout: []byte("/ef" + r.literal[2:] + "\n")}, nil
			}
			return execx.Result{Stdout: []byte(r.literal + "\n")}, nil
		}
		return execx.Result{Stdout: []byte("claude is running\n")}, nil
	case reflect.DeepEqual(args, []string{"agent", "get", "pane-1"}):
		r.agentCalls++
		if r.agentNotFound {
			return execx.Result{Stdout: []byte(`{"error":{"code":"agent_not_found"}}`)}, nil
		}
		if r.agentErr != nil {
			return execx.Result{}, r.agentErr
		}
		if r.agentStatus == "working" {
			*r.events = append(*r.events, "agent-working")
		}
		return jsonResult(`{"agent":{"agent_status":"` + r.agentStatus + `"}}`), nil
	default:
		return execx.Result{}, fmt.Errorf("unexpected Herdr args: %q", args)
	}
}

func jsonResult(result string) execx.Result {
	return execx.Result{Stdout: []byte(`{"result":` + result + `}`)}
}

// fixtureSchemaJSON is the minimal protocol-19 schema-1 document satisfying
// the spawn compatibility preflight: both response envelopes plus every
// method CFO uses.
func fixtureSchemaJSON() string {
	methods := []string{
		"server.agent_manifests",
		"session.snapshot",
		"workspace.create",
		"workspace.list",
		"workspace.rename",
		"tab.close",
		"tab.create",
		"tab.list",
		"tab.rename",
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
	var b strings.Builder
	b.WriteString(`{"protocol":19,"schema_version":1,"schemas":{"success_response":{},"error_response":{},"request":{"oneOf":[`)
	for i, method := range methods {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"properties":{"method":{"const":` + quoteJSON(method) + `}}}`)
	}
	b.WriteString(`]}}}`)
	return b.String()
}

func quoteJSON(value string) string {
	return strconv.Quote(value)
}

func makeDir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := fsx.Canonical(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sortedKeys(t *testing.T, stateDir, id string) []string {
	t.Helper()
	values, err := state.ReadMeta(filepath.Join(stateDir, id+".meta"))
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestSpawnRetriesInstructionDeliveryWhenReadbackCorrupts(t *testing.T) {
	fixture := newFixture(t)
	// The first capture after the instruction is typed (capture 3: two dialog
	// probes, then the instruction read-back) comes back with leading
	// characters eaten, so the verification must clear and retype.
	fixture.runner.corruptCaptureAt = 3

	result, err := fixture.service.Spawn(context.Background(), fixture.request)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !strings.Contains(result.Output, "spawned task-7") {
		t.Errorf("Output = %q, want successful spawn after the retry", result.Output)
	}
	if !slices.Contains(fixture.runner.keys, "ctrl+u") {
		t.Errorf("keys = %v, want a composer clear (ctrl+u) after the corrupted read-back", fixture.runner.keys)
	}
	if len(fixture.runner.literals) != 3 {
		t.Errorf("literals = %q, want prefix plus two instruction deliveries", fixture.runner.literals)
	}
}
