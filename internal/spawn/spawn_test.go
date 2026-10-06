package spawn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

func TestSpawnSnapshotsTaskClassPolicy(t *testing.T) {
	f := newQuickFixture(t)
	f.service.PolicyPath = filepath.Join("..", "..", "config", "pipeline.json")
	f.request.Class = "high-risk"
	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := pipeline.LoadSelection(filepath.Join(result.Meta.TaskTmp, "pipeline.json"))
	if err != nil || snapshot.ReviewCycles != 3 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || meta.PipelineClass != "high-risk" || meta.PipelineHash != snapshot.Hash {
		t.Fatalf("metadata: %+v %v", meta, err)
	}
}

// The task's short title is published with its metadata, so the board names
// it from the moment it exists.
func TestSpawnPublishesTheTaskTitle(t *testing.T) {
	f := newQuickFixture(t)
	f.request.Title = "Install and run on any machine, no setup"
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || meta.Title != f.request.Title {
		t.Fatalf("metadata: %+v %v, want the title %q", meta, err, f.request.Title)
	}
}

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

// A harness whose screens no native terminal knows yet is refused before
// anything is created, since a spawn never types into a screen it cannot
// read.
func TestSpawnRefusesAHarnessWithNoNativeScreensBeforeFilesystemMutation(t *testing.T) {
	f := newFixture(t)
	const unscreened = harness.Kind("unscreened")
	f.service.Harness.Adapters[unscreened] = fixtureAdapter{events: &f.events}
	f.request.Harness = unscreened
	stateDir := filepath.Join(t.TempDir(), "state")
	f.service.StateDir = stateDir

	_, err := f.service.Spawn(context.Background(), f.request)

	if err == nil || !strings.Contains(err.Error(), "unscreened cannot run in a native terminal yet") {
		t.Fatalf("Spawn error = %v, want the harness refused", err)
	}
	if _, statErr := os.Stat(stateDir); !errors.Is(statErr, os.ErrNotExist) || len(f.events) != 0 {
		t.Fatalf("the refusal created state (%v) or ran %v", statErr, f.events)
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

// A ship task is published as native, with its terminal the host named by its
// id and no Herdr pane, and its harness starts there with the task's identity
// and its brief.
func TestSpawnShipPublishesANativeTaskAndBriefsItsHarness(t *testing.T) {
	for _, key := range []string{"CODEX_THREAD_ID", "CFO_SESSION_ID", "CFO_SESSION_HARNESS", "CFO_ROOT_SESSION_ID"} {
		t.Setenv(key, "")
	}
	f := newQuickFixture(t)

	result, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	first, _, _ := strings.Cut(result.Output, "\n")
	if want := fmt.Sprintf("spawned task-7 harness=codex kind=ship mode=no-mistakes yolo=on window=native worktree=%s", f.worktree); first != want {
		t.Errorf("Output = %q\nwant %q", first, want)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil {
		t.Fatalf("ReadTaskMeta: %v", err)
	}
	if meta.Window != "native" || meta.EndpointTaskID != f.request.ID || meta.Worktree != f.worktree || meta.Project != f.project || meta.Harness != "codex" || meta.Kind != "ship" || meta.Mode != "no-mistakes" || meta.Yolo != "on" || meta.Model != "default" || meta.Effort != "default" || meta.Backend != "native" || meta.HerdrSession != "" || meta.HerdrPaneID != "" {
		t.Errorf("metadata = %+v, want a complete native ship record", meta)
	}
	if meta.TaskTmp == "" || meta.SpawnGen == "" {
		t.Errorf("metadata = %+v, want tasktmp and spawn generation", meta)
	}
	if got, want := sortedKeys(t, f.stateDir, f.request.ID), []string{"backend", "brief", "effort", "endpoint_task_id", "harness", "kind", "mode", "model", "project", "scratch", "spawn_gen", "tasktmp", "window", "worktree", "yolo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("metadata keys = %v, want %v", got, want)
	}
	scratch := taskScratch(f.stateDir, meta.ID)
	if meta.Scratch != scratch {
		t.Errorf("recorded scratch = %q, want %q", meta.Scratch, scratch)
	}
	if info, statErr := os.Stat(scratch); statErr != nil || !info.IsDir() {
		t.Fatalf("scratch = %q, stat = %v, want existing directory", scratch, statErr)
	}
	// Go writes build and test temporaries under GOTMPDIR and everything else
	// under TEMP, t.TempDir() included. Pointed inside the checkout they made
	// every test a goblin ran create files in the tree it was editing.
	for _, inside := range []string{f.stateDir, f.worktree, f.project} {
		if rel, relErr := filepath.Rel(inside, scratch); relErr == nil && !strings.HasPrefix(rel, "..") {
			t.Errorf("scratch = %q, want it outside %q", scratch, inside)
		}
	}
	env := named(f.events(t), "env")[0].Env
	for name, want := range map[string]string{"CFO_TASK_ID": "task-7", "CFO_ROLE": harness.RoleGoblin, "GOTMPDIR": scratch, "TEMP": scratch, "TMP": scratch, "CFO_STATE_OVERRIDE": f.stateDir} {
		if got := env[name]; got == nil || *got != want {
			t.Errorf("the harness started with %s = %v, want %q", name, got, want)
		}
	}
	submitted := named(f.events(t), "submitted")
	if len(submitted) != 1 || !strings.Contains(delivered(t, submitted[0].Text), "Read the brief at "+f.brief) {
		t.Errorf("submitted = %+v, want the brief instruction once", submitted)
	}
	if _, statErr := os.Stat(filepath.Join(f.stateDir, ".spawn.lock")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("spawn lock persists after success: stat error = %v", statErr)
	}
}

func TestSpawnScoutOmitsShipFields(t *testing.T) {
	f := newQuickFixture(t)
	f.request.Kind = "scout"
	f.request.Mode = ""
	f.request.Yolo = false
	writeFile(t, f.brief, "Investigate the reported behavior.\n")

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn scout: %v", err)
	}
	first, _, _ := strings.Cut(result.Output, "\n")
	if want := fmt.Sprintf("spawned task-7 harness=codex kind=scout window=native worktree=%s", f.worktree); first != want {
		t.Errorf("Output = %q\nwant %q", first, want)
	}
	if result.Meta.Mode != "" || result.Meta.Yolo != "" {
		t.Errorf("scout metadata = %+v, want omitted mode and yolo", result.Meta)
	}
	if got, want := sortedKeys(t, f.stateDir, f.request.ID), []string{"backend", "brief", "effort", "endpoint_task_id", "harness", "kind", "model", "project", "scratch", "spawn_gen", "tasktmp", "window", "worktree"}; !reflect.DeepEqual(got, want) {
		t.Errorf("scout metadata keys = %v, want %v", got, want)
	}
}

func TestSpawnDisclosesATrackedMCPConfigOnlyWhenSomethingWasWithheld(t *testing.T) {
	const disclosure = "the project tracks .mcp.json"
	t.Setenv("STRIPE_KEY", "stripe-token")
	t.Setenv("MISSING_TOKEN", "")
	for _, test := range []struct {
		name      string
		config    string
		disclosed bool
	}{
		{
			name:      "an OAuth connector is withheld",
			config:    `{"mcpServers":{"neon":{"command":"npx"},"supabase":{"url":"https://mcp.supabase.com/mcp"}}}`,
			disclosed: true,
		},
		{
			name:      "a server whose token is not set is withheld",
			config:    `{"mcpServers":{"neon":{"command":"npx"},"stripe":{"url":"https://mcp.stripe.com/","bearerTokenEnvVar":"MISSING_TOKEN"}}}`,
			disclosed: true,
		},
		{
			// Nothing was dropped, so a working-directory-reading harness
			// sees exactly the servers the filtered config would have given
			// it. Claiming otherwise on every dispatch is a false statement.
			name:      "every server qualifies",
			config:    `{"mcpServers":{"neon":{"command":"npx"},"stripe":{"url":"https://mcp.stripe.com/","bearerTokenEnvVar":"STRIPE_KEY"}}}`,
			disclosed: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newQuickFixture(t)
			writeFile(t, filepath.Join(f.project, ".mcp.json"), test.config)
			writeFile(t, filepath.Join(f.worktree, ".mcp.json"), test.config)
			f.runner.mcpTracked = true

			result, err := f.service.Spawn(context.Background(), f.request)
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			if got := strings.Contains(result.Output, disclosure); got != test.disclosed {
				t.Errorf("output contains the tracked-config disclosure = %v, want %v:\n%s", got, test.disclosed, result.Output)
			}
		})
	}
}

// A goblin's worktree lives in the home, named for its project's folder and
// its task, never in the project: the checkout gains no folder and no file.
func TestSpawnPutsTheWorktreeUnderTheHomeAndLeavesTheProjectAlone(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	before, err := os.ReadDir(f.project)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Assert
	want := filepath.Join(f.service.Worktrees.Root, "primary", "task-7")
	if f.git.acquired != want {
		t.Errorf("the worktree was asked for at %q, want %q", f.git.acquired, want)
	}
	after, err := os.ReadDir(f.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Errorf("the project holds %v after the spawn, want only what it held before, %v", after, before)
	}
}

func TestSpawnReportsADefaultLinkSkippedForACheckedOutFile(t *testing.T) {
	f := newQuickFixture(t)
	// The project commits .env, so git worktree add checks it out before
	// provisioning runs. The default link set is not a declaration, so the
	// occupied path is left alone and named on the spawn output instead of
	// tearing the dispatch down.
	writeFile(t, filepath.Join(f.project, ".env"), "K=primary\n")
	writeFile(t, filepath.Join(f.worktree, ".env"), "K=checked-out\n")

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn: %v, want the goblin dispatched with its checked-out .env", err)
	}
	if !strings.Contains(result.Output, "link: .env already present in the worktree") {
		t.Errorf("output = %q, want the skipped default share reported", result.Output)
	}
	data, err := os.ReadFile(filepath.Join(f.worktree, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "K=checked-out" {
		t.Errorf("worktree .env = %q, want the checked-out file untouched", data)
	}
}

func TestSpawnKeepsTheLaunchContractOverCaseAliasedRedirects(t *testing.T) {
	f := newQuickFixture(t)
	// Windows compares environment names without case, so a redirect that
	// differs from a reserved name only by case is the same variable.
	// CFO_STATE_OVERRIDE is only written at harness start, after the manifest
	// is merged, so it proves the reserved set does not depend on what the
	// launch map happens to hold at merge time. LOCALAPPDATA, XDG_CACHE_HOME
	// and HOME are reserved for a different reason: the launch never writes
	// them, but os.UserCacheDir reads them to derive the task's Go temporary
	// directory, so a redirect would leave any cfo command the goblin runs
	// computing a different directory than spawn created.
	writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{
		Project: "primary",
		Env: map[string]string{
			"gotmpdir":                 `C:\hijacked-gotmpdir`,
			"cfo_state_override":       `C:\hijacked-state`,
			"Cfo_Role":                 "overlord",
			"localappdata":             `C:\hijacked-cache`,
			"XDG_Cache_Home":           "/hijacked-cache",
			"Home":                     "/hijacked-cache-home",
			"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`,
		},
	})

	result, err := f.service.Spawn(context.Background(), f.request)

	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	env := named(f.events(t), "env")[0].Env
	for name, value := range env {
		if value != nil && (strings.Contains(*value, "hijacked") || *value == "overlord") {
			t.Errorf("the harness started with %s = %q, want every case-aliased reserved redirect dropped", name, *value)
		}
	}
	for name, want := range map[string]string{
		"GOTMPDIR":                 taskScratch(f.stateDir, result.Meta.ID),
		"CFO_STATE_OVERRIDE":       f.stateDir,
		"CFO_ROLE":                 harness.RoleGoblin,
		"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`,
	} {
		if got := env[name]; got == nil || *got != want {
			t.Errorf("the harness started with %s = %v, want %q", name, got, want)
		}
	}
}

func TestSpawnDispatchesAndReportsWhenTheDependencyInstallFails(t *testing.T) {
	f := newQuickFixture(t)
	// Strategy install is the default and its command is auto-detected from a
	// lockfile, so no project opted into it. A drifted lockfile must not turn
	// every dispatch into this repo into a failure.
	writeFile(t, filepath.Join(f.worktree, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	f.runner.installer = "pnpm"
	f.runner.installerStderr = "ERR_PNPM_OUTDATED_LOCKFILE  Cannot install with frozen-lockfile\n"

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn: %v, want the goblin dispatched anyway", err)
	}
	if !slices.Contains(f.fixture.events, "install") {
		t.Fatalf("events = %v, want the detected installer to have run", f.fixture.events)
	}
	if !strings.Contains(result.Output, "pnpm install --frozen-lockfile") ||
		!strings.Contains(result.Output, "ERR_PNPM_OUTDATED_LOCKFILE") {
		t.Errorf("output = %q, want the failed install command and its cause reported", result.Output)
	}
	// Nothing was torn down: the task is published, its worktree kept, and
	// the harness got its brief.
	if _, err := state.ReadTaskMeta(f.stateDir, f.request.ID); err != nil {
		t.Fatalf("read metadata after a failed install: %v", err)
	}
	if f.git.returned != 0 {
		t.Errorf("worktree returns = %d; want the dispatch left intact", f.git.returned)
	}
	if submitted := named(f.events(t), "submitted"); len(submitted) != 1 {
		t.Errorf("submitted = %+v, want the brief delivered after the failed install", submitted)
	}
}

func TestSpawnRefusesUnvalidatableWorktreeWithoutLaunching(t *testing.T) {
	f := newFixture(t)
	f.git.topErr = errors.New("worktree: not a git worktree")
	primaryMarker := filepath.Join(f.project, "primary-marker.txt")
	writeFile(t, primaryMarker, "unchanged")

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "not a git worktree") {
		t.Fatalf("Spawn unvalidatable worktree error = %v, want a validation refusal", err)
	}
	if slices.Contains(f.events, "build-harness") || hasTerminal(f.stateDir, f.request.ID) {
		t.Fatalf("unvalidatable worktree launched a harness: events=%v", f.events)
	}
	if got, readErr := os.ReadFile(primaryMarker); readErr != nil || string(got) != "unchanged" {
		t.Fatalf("primary project changed after the refusal: %q, %v", got, readErr)
	}
	if _, statErr := os.Stat(f.worktree); statErr != nil {
		t.Fatalf("refusal removed acquired worktree: %v", statErr)
	}
	if f.git.returned != 1 {
		t.Fatalf("refusal returned the lease %d times, want 1", f.git.returned)
	}
}

func TestSpawnNormalizesLaunchFailureStatusToOneFailedEvent(t *testing.T) {
	f := newFixture(t)
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("preview unavailable\r\ndone: forged")}

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "preview unavailable") {
		t.Fatalf("Spawn launch error = %v, want the build refusal propagated", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(f.stateDir, f.request.ID+".status"))
	if readErr != nil {
		t.Fatalf("ReadFile status: %v", readErr)
	}
	status := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n"), "\n")
	if len(status) != 1 {
		t.Fatalf("raw status = %q; status events = %q, want exactly one", string(raw), status)
	}
	_, event := state.SplitStatus(status[0])
	verb, _, found := strings.Cut(event, ":")
	if !found || verb != "failed" {
		t.Fatalf("status event = %q, want one failed event", status[0])
	}
	if strings.ContainsAny(event, "\r\n") || strings.HasPrefix(event, "done:") {
		t.Fatalf("status event permits forged status line: %q", status[0])
	}
}

func TestSpawnRejectsMetadataBearingControlsBeforeAnyMutation(t *testing.T) {
	tests := []struct {
		name string
		set  func(*Request)
	}{
		{"project", func(req *Request) { req.Project = "C:\\project\nbad" }},
		{"brief", func(req *Request) { req.BriefPath = "C:\\brief\nbad" }},
		{"kind", func(req *Request) { req.Kind = "ship\nbad" }},
		{"mode", func(req *Request) { req.Mode = "no-mistakes\nbad" }},
		{"harness", func(req *Request) { req.Harness = harness.Kind("claude\nbad") }},
		{"model", func(req *Request) { req.Model = "x\nwindow=other" }},
		{"effort", func(req *Request) { req.Effort = "high\rmalformed" }},
		{"title", func(req *Request) { req.Title = "a title\nbackend=other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			test.set(&f.request)

			_, err := f.service.Spawn(context.Background(), f.request)
			if err == nil || !strings.Contains(err.Error(), "control character") {
				t.Fatalf("Spawn control injection error = %v, want control character refusal", err)
			}
			if f.runner.calls != 0 || len(f.events) != 0 {
				t.Fatalf("control injection ran work: calls=%d events=%v", f.runner.calls, f.events)
			}
			if _, statErr := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("Spawn wrote injected metadata: stat error = %v", statErr)
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
			name: "validate",
			set:  func(f *fixture) { f.git.topErr = errors.New("worktree: not a git worktree") },
			want: "not a git worktree",
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
			f := newFixture(t)
			test.set(f)

			result, err := f.service.Spawn(context.Background(), f.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Spawn error = %v, want %q", err, test.want)
			}
			if result.Meta.Worktree != f.worktree || result.Meta.Project != f.project || result.Meta.Backend != "native" {
				t.Fatalf("partial result = %+v, want the native task's worktree and project", result.Meta)
			}
			status, statusErr := state.TailStatus(f.stateDir, f.request.ID, 2)
			if statusErr != nil || len(status) != 1 {
				t.Fatalf("status = %v, %v; want one failed event containing %q", status, statusErr, test.want)
			}
			if _, event := state.SplitStatus(status[0]); !strings.HasPrefix(event, "failed: ") || !strings.Contains(event, test.want) {
				t.Fatalf("status = %v, %v; want one failed event containing %q", status, statusErr, test.want)
			}
			if _, metaErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(metaErr, os.ErrNotExist) {
				t.Fatalf("post-acquisition failure wrote success metadata: %v", metaErr)
			}
			if hasTerminal(f.stateDir, f.request.ID) {
				t.Fatal("post-acquisition failure started a native terminal")
			}
			if f.git.returned != 1 {
				t.Fatalf("post-acquisition failure returned the lease %d times, want 1", f.git.returned)
			}
		})
	}
}

func TestSpawnPostAcquireFailureSurfacesReturnError(t *testing.T) {
	f := newFixture(t)
	f.git.topErr = errors.New("worktree: not a git worktree")
	f.git.returnErr = errors.New("worktree: return refused")

	_, err := f.service.Spawn(context.Background(), f.request)
	if err == nil || !strings.Contains(err.Error(), "not a git worktree") || !strings.Contains(err.Error(), "return refused") {
		t.Fatalf("Spawn error = %v, want joined launch and return failures", err)
	}
	if f.git.returned != 1 {
		t.Fatalf("post-acquire failure returned the lease %d times, want 1", f.git.returned)
	}
}

func TestSpawnSurfacesTaskLockReleaseFailure(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		f := newQuickFixture(t)
		f.service.ReleaseLock = func(string, string) error {
			return errors.New("lock is still shared")
		}

		result, err := f.service.Spawn(context.Background(), f.request)
		if err == nil || !strings.Contains(err.Error(), "release spawn lock") || !strings.Contains(err.Error(), "lock is still shared") {
			t.Fatalf("Spawn release error = %v, want surfaced cleanup failure", err)
		}
		if result.Meta.Worktree != f.worktree || !strings.HasPrefix(result.Output, "spawned task-7") {
			t.Fatalf("success result was discarded by cleanup error: %+v output=%q", result.Meta, result.Output)
		}
		if err := lock.ReleaseNamed(f.stateDir, spawnLockName); err != nil {
			t.Fatalf("ReleaseNamed cleanup: %v", err)
		}
	})

	t.Run("primary failure", func(t *testing.T) {
		f := newFixture(t)
		f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("harness build refused")}
		f.service.ReleaseLock = func(string, string) error {
			return errors.New("lock is still shared")
		}

		result, err := f.service.Spawn(context.Background(), f.request)
		if err == nil || !strings.Contains(err.Error(), "harness build refused") || !strings.Contains(err.Error(), "release spawn lock") {
			t.Fatalf("Spawn release error = %v, want joined primary and cleanup failures", err)
		}
		if result.Meta.Worktree != f.worktree {
			t.Fatalf("primary failure discarded recovery result: %+v", result.Meta)
		}
		if err := lock.ReleaseNamed(f.stateDir, spawnLockName); err != nil {
			t.Fatalf("ReleaseNamed cleanup: %v", err)
		}
	})
}

func TestSpawnRefusesAContendedLockAndSpawnsOnceItIsReleased(t *testing.T) {
	f := newQuickFixture(t)
	if _, err := lock.AcquireExclusiveNamed(f.stateDir, spawnLockName); err != nil {
		t.Fatalf("AcquireExclusiveNamed setup: %v", err)
	}
	_, err := f.service.Spawn(context.Background(), f.request)
	if !errors.Is(err, lock.ErrHeld) {
		t.Fatalf("Spawn contention error = %v, want ErrHeld", err)
	}
	if err := lock.ReleaseNamed(f.stateDir, spawnLockName); err != nil {
		t.Fatalf("ReleaseNamed setup lock: %v", err)
	}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn after lock release: %v", err)
	}
}

func TestSpawnRejectsCaseAliasBeforeTerminalOrWorktreeMutation(t *testing.T) {
	f := newQuickFixture(t)
	f.request.ID = "Foo"
	closeTerminalAtEnd(t, f.stateDir, "Foo")
	first, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("first Spawn: %v", err)
	}
	metaPath := filepath.Join(f.stateDir, "Foo.meta")
	before, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	calls, returned := f.runner.calls, f.git.returned

	second := f.request
	second.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), second); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("case-alias Spawn error = %v, want case-insensitive collision refusal", err)
	}
	if f.runner.calls != calls || f.git.returned != returned {
		t.Errorf("commands = %d and worktree returns = %d after alias rejection, want %d and %d before any second task mutation", f.runner.calls, f.git.returned, calls, returned)
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

func TestSpawnRejectsCaseInsensitiveMetadataExtensionBeforeTerminalOrWorktreeMutation(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.stateDir, "Foo.META"), "window=native\n")

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("case-insensitive extension Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(f.events) != 0 {
		t.Errorf("commands = %d and events = %v, want none before the metadata-alias refusal", f.runner.calls, f.events)
	}
}

// A spawn that fails after the Go temporary directory exists must not orphan
// it. teardownLaunch removes <id>.meta, and cleanup reads that file to find a
// task at all, so a directory left behind here can never be removed by
// anything afterwards - and it sits under the user cache directory, out of
// sight of the state tree that would otherwise show it.
func TestSpawnFailureLeavesNoGoTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	f.service.Harness.Adapters[harness.Claude] = fixtureAdapter{events: &f.events, buildErr: errors.New("harness build refused")}

	if _, err := f.service.Spawn(context.Background(), f.request); err == nil {
		t.Fatal("Spawn succeeded, want the injected build failure")
	}
	goTmp := goTmpDir(t, f.stateDir, f.request.ID)
	if _, err := os.Stat(goTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("go temporary directory %q survived a failed spawn: %v", goTmp, err)
	}
	// The metadata is gone, which is what makes the leak permanent: prove the
	// removal happened before it rather than depending on a later cleanup.
	if _, err := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed spawn kept metadata: %v", err)
	}
}

// A task temporary directory left behind by something other than a clean
// teardown - a killed process, a crashed host - still claims its id. On a
// case-insensitive filesystem "Foo" and "foo" are the same directory, so
// letting a new task take the alias would hand it a live task's credential
// script directory. The guard has to fire before any terminal or worktree
// mutation, or the collision is discovered after the damage.
//
// A failed spawn no longer feeds this guard: it removes its own tasktmp, and
// a .status without live metadata is deliberately not a claim on the id (that
// is what forced a cleaned-up task to be respawned under an invented suffix).
// The leftover directory below is therefore created directly, which is the
// only way this state still arises.
func TestSpawnRejectsCaseAliasOfARetainedTaskTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	leftover := filepath.Join(f.stateDir, "tasktmp", "Foo")
	if err := os.MkdirAll(leftover, 0o700); err != nil {
		t.Fatal(err)
	}
	// Premise: the leftover really is present and really does alias the
	// requested id, so a refusal means the guard fired rather than the spawn
	// failing for some unrelated reason.
	if info, err := os.Stat(leftover); err != nil || !info.IsDir() {
		t.Fatalf("leftover tasktmp: info=%v err=%v, want a directory to collide with", info, err)
	}

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("alias Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(f.events) != 0 {
		t.Errorf("commands = %d and events = %v after alias rejection, want none before a second task mutation", f.runner.calls, f.events)
	}
}

// A live task still claims its id through its metadata, which is the durable
// claim a failed spawn's removed tasktmp no longer has to stand in for.
func TestSpawnRejectsCaseAliasOfALiveTask(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.stateDir, "Foo.meta"), "id: Foo\n")

	request := f.request
	request.ID = "foo"
	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("alias Spawn error = %v, want collision refusal", err)
	}
	if f.runner.calls != 0 || len(f.events) != 0 {
		t.Errorf("commands = %d and events = %v after alias rejection, want none before a second task mutation", f.runner.calls, f.events)
	}
}

// refusingPreflight stops a dispatch the way a red blocking service does.
type refusingPreflight struct{}

func (refusingPreflight) Preflight(context.Context, string) (auth.Result, error) {
	return auth.Result{Refusal: "a blocking service is red"}, nil
}

// The capsule is written into the id's own task temporary directory only after
// the alias check, and a spawn that fails before its task is published takes
// the capsule with it, so the retry is not refused by the failed attempt.
func TestSpawnRemovesTheCapsuleOfASpawnThatFailsBeforePublishing(t *testing.T) {
	f := newQuickFixture(t)
	f.request.Yolo = false
	taskTmp := filepath.Join(f.stateDir, "tasktmp", f.request.ID)
	request := f.request
	request.Capsule = func(dir string) (string, error) {
		if dir != taskTmp {
			t.Errorf("capsule dir = %q, want %q", dir, taskTmp)
		}
		brief := filepath.Join(dir, "brief.md")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return brief, os.WriteFile(brief, []byte("Delivery contract: mode=no-mistakes\nDo the work.\n\n## CFO durable task capsule\n"), 0o600)
	}
	credentials := f.service.Auth
	f.service.Auth = refusingPreflight{}

	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "a blocking service is red") {
		t.Fatalf("Spawn error = %v, want the preflight refusal", err)
	}
	if _, err := os.Stat(taskTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capsule directory %q survived the failed spawn: %v", taskTmp, err)
	}

	f.service.Auth = credentials
	result, err := f.service.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("retry Spawn error = %v, want the same id spawned with its capsule", err)
	}
	if want := filepath.Join(taskTmp, "brief.md"); result.Meta.Brief != want {
		t.Errorf("meta brief = %q, want the capsule brief %q", result.Meta.Brief, want)
	}
}

// taskTmpProbe refuses to build the launch, after noting whether the task's
// temporary directory existed by then.
type taskTmpProbe struct {
	fixtureAdapter
	existed *bool
}

func (a taskTmpProbe) Build(spec harness.LaunchSpec) (harness.Launch, error) {
	info, err := os.Stat(spec.TaskTmp)
	*a.existed = err == nil && info.IsDir()
	return harness.Launch{}, errors.New("harness build refused")
}

// A failed launch must leave no task temporary directory. cleanup finds a task
// through the task metadata, so once that is retired nothing can remove this
// directory again - and while it survives it refuses the retry of the very
// spawn that just failed. It also holds the rendered credential script.
func TestSpawnFailureLeavesNoTaskTemporaryDirectory(t *testing.T) {
	f := newFixture(t)
	existed := false
	f.service.Harness.Adapters[harness.Claude] = taskTmpProbe{fixtureAdapter: fixtureAdapter{events: &f.events}, existed: &existed}
	taskTmp := filepath.Join(f.stateDir, "tasktmp", f.request.ID)

	if _, err := f.service.Spawn(context.Background(), f.request); err == nil {
		t.Fatal("Spawn succeeded, want the build failure")
	}
	// Premise: the directory really was created, so its absence is a removal
	// rather than a launch that never got far enough to make one.
	if !existed {
		t.Fatal("tasktmp was never created, so its absence proves nothing")
	}
	if _, err := os.Stat(taskTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("task temporary directory %q survived a failed spawn: %v", taskTmp, err)
	}
	if _, err := os.Stat(filepath.Join(f.stateDir, f.request.ID+".meta")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed spawn kept metadata: %v", err)
	}
}

func TestNotifyInstructionTeachesWorkingAndWaitingReports(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	instruction := notifyInstruction("task-7")

	for _, want := range []string{
		exe + " notify task-7 --working \"<what>\"",
		exe + " notify task-7 --waiting-on <task-id|overlord|ci|deploy|memory> \"<why>\"",
		exe + " notify task-7 --waiting-on overlord \"<why>\" --lavish <html-file>",
		exe + " notify task-7 --waiting-on overlord \"<why>\" --run <command.ps1>",
		"runs it with one click in a window he can use",
		"never run lavish-axi poll yourself",
		"lead with one short sentence that is the actual question",
		"lines of their own that start with \"- \"",
		"mark with **two asterisks** only the verdict or the blocking item",
		"the Overlord personally (his sign-in, his click, his page)",
		"a choice the CFO can make, such as whether to start something now or later, is a question, not a wait on the Overlord: ask it with --blocked and options",
		"Never wait on the CFO for a choice you can undo: take the better option, say which with --working, and keep going; a choice you cannot undo or make yourself is a question for --blocked, never one asked in your reply.",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
}

// The Overlord named the review page Scrawl (2026-10-01), so a goblin calls
// it that; the lavish-axi command and the --lavish flag keep their names.
func TestNotifyInstructionCallsTheReviewPageScrawl(t *testing.T) {
	// Act
	instruction := notifyInstruction("task-7")

	// Assert
	for _, want := range []string{
		"must answer on a Scrawl page",
		"call it Scrawl when you name it to him",
		"lavish-axi <html-file> --no-open",
		"--lavish <html-file>",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
	if strings.Contains(instruction, "Lavish") {
		t.Errorf("instruction = %q, want the page named Scrawl, never Lavish", instruction)
	}
}

// The Overlord, 2026-10-02: "in Scrawl the actual radio choice selection at
// the bottom doesn't exist anymore". A Scrawl page draws its choices only
// when it declares them, so the brief says how, and that his pick comes back
// as the option's exact text.
func TestNotifyInstructionTellsAPageThatAsksHimToPickToDeclareItsChoices(t *testing.T) {
	// Act
	instruction := notifyInstruction("task-7")

	// Assert
	for _, want := range []string{
		`<script type="application/json" data-lavish-choices>`,
		"radio list",
		"the option's exact text",
		"a page without it shows him no choices",
	} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q", instruction, want)
		}
	}
}

// Every Claude goblin runs Opus 5.5 unless a model is named (the Supreme
// Overlord's directive of 2026-09-23), whether --harness claude came alone or
// a lane named claude without a model. A named model still wins, and another
// harness keeps its own default. The model the task records is the one its
// launch is built with; the build is refused here so nothing starts.
func TestSpawnRunsAClaudeGoblinWithNoNamedModelOnOpus55(t *testing.T) {
	for _, c := range []struct {
		name  string
		kind  harness.Kind
		model string
		want  string
	}{
		{"claude with no model", harness.Claude, "", "claude-opus-5-5"},
		{"claude with a named model", harness.Claude, "claude-sonnet-5", "claude-sonnet-5"},
		{"another harness with no model", harness.Codex, "", "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.service.Harness.Adapters[c.kind] = fixtureAdapter{events: &f.events, specs: &f.specs, buildErr: errors.New("nothing starts in this test")}
			f.request.Harness, f.request.Model = c.kind, c.model

			result, err := f.service.Spawn(context.Background(), f.request)

			if err == nil || !strings.Contains(err.Error(), "nothing starts in this test") {
				t.Fatalf("Spawn = %v, want the build refusal", err)
			}
			launched := f.specs[len(f.specs)-1].Model
			if result.Meta.Model != c.want || valueOrDefault(launched) != c.want {
				t.Fatalf("recorded model %q, launched %q; want %q", result.Meta.Model, launched, c.want)
			}
		})
	}
}

type fixture struct {
	service  Service
	request  Request
	stateDir string
	dataDir  string
	project  string
	worktree string
	brief    string
	events   []string
	specs    []harness.LaunchSpec
	runner   *commandRunner
	git      *worktreeGit
}

// taskScratch is the scratch folder a spawn makes for task id in the
// fixture's home, beside its state folder.
func taskScratch(stateDir, id string) string {
	return filepath.Join(filepath.Dir(stateDir), "scratch", id)
}

// goTmpDir returns the Go temporary directory an older build gave a task,
// under the isolated user cache directory, which a task recorded with no
// scratch folder still uses.
func goTmpDir(t *testing.T, stateDir, id string) string {
	t.Helper()
	dir, err := state.GoTmpDir(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// isolateUserCacheDir points os.UserCacheDir at a directory of the test's own,
// by setting the variables it reads. Without the isolation a spawn test writes
// into the operator's own cache directory and leaves the per-task Go temporary
// directory behind, which is the same class of leak as a test resolving the
// live fleet home.
// HOME is in the set because os.UserCacheDir reads it on darwin and on Linux
// whenever XDG_CACHE_HOME is unset; without it the isolation is vacuous there.
// The resolve afterwards is the premise assertion: an isolation helper that
// silently stops isolating on a platform nobody runs it on is how a test comes
// to write into the operator's own cache.
func isolateUserCacheDir(t *testing.T) {
	t.Helper()
	cache := t.TempDir()
	for _, name := range []string{"LOCALAPPDATA", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(name, cache)
	}
	resolved, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("UserCacheDir = %v, want the isolated cache directory", err)
	}
	if rel, relErr := filepath.Rel(cache, resolved); relErr != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("UserCacheDir = %q, want it under the test's own directory %q", resolved, cache)
	}
}

// newFixture readies a spawn of task-7 that stops before any terminal starts:
// its claude adapter's screens are never drawn by anything here, so a test
// that needs the harness running uses newNativeFixture instead.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	isolateUserCacheDir(t)
	// A Codex spawn or switch reads the MCP servers of CODEX_HOME's
	// config.toml, which is never this machine's own.
	t.Setenv("CODEX_HOME", t.TempDir())
	// Canonical, as every folder made under it is, so the scratch and
	// worktrees folders are spelled as the state folder is, short names in a
	// runner's temp path included.
	root := makeDir(t, t.TempDir())
	stateDir := makeDir(t, filepath.Join(root, "state"))
	dataDir := makeDir(t, filepath.Join(root, "data"))
	project := makeDir(t, filepath.Join(root, "primary"))
	worktreeDir := makeDir(t, filepath.Join(root, "worktree"))
	brief := filepath.Join(root, "brief.md")
	writeFile(t, brief, "Delivery contract: mode=no-mistakes\nDo the work.\n")
	writeFile(t, filepath.Join(project, "primary-marker.txt"), "unchanged")

	fixture := &fixture{stateDir: stateDir, dataDir: dataDir, project: project, worktree: worktreeDir, brief: brief}
	fixture.runner = &commandRunner{events: &fixture.events}
	fixture.git = &worktreeGit{events: &fixture.events, top: worktreeDir}
	fixture.service = Service{
		Worktrees: worktree.Service{
			Commands: fixture.runner,
			Git:      fixture.git,
			Root:     filepath.Join(root, "worktrees"),
			DataDir:  dataDir,
			Sleep:    func(context.Context, time.Duration) error { return nil },
		},
		ScratchRoot: filepath.Join(root, "scratch"),
		Harness: harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
			harness.Claude: fixtureAdapter{events: &fixture.events, specs: &fixture.specs},
		}},
		StateDir:        stateDir,
		Project:         project,
		UserEnvironment: func() ([]string, error) { return os.Environ(), nil },
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
	}
	return fixture
}

// hasTerminal reports whether task id has a native terminal record.
func hasTerminal(stateDir, id string) bool {
	_, err := host.ReadRecord(stateDir, id)
	return !errors.Is(err, fs.ErrNotExist)
}

// closeTerminalAtEnd closes task id's native terminal, whichever host it is
// by then, when the test ends.
func closeTerminalAtEnd(t *testing.T, stateDir, id string) {
	t.Helper()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(stateDir, id); err == nil {
			if err := host.Close(stateDir, record, nativeCloseWait); err != nil {
				t.Errorf("close native terminal %s: %v", id, err)
			}
		}
	})
}

type fixtureAdapter struct {
	events   *[]string
	specs    *[]harness.LaunchSpec
	buildErr error
	// control replaces the fixture's stop and resume control, for a test
	// that needs a real harness's.
	control *harness.Control
}

func (a fixtureAdapter) Control() harness.Control {
	if a.control != nil {
		return *a.control
	}
	return harness.Control{
		StopKeys:    []string{"escape"},
		StopCommand: "/exit",
		ResumeArgs:  []string{"--continue"},
	}
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
	if a.specs != nil {
		*a.specs = append(*a.specs, spec)
	}
	if a.buildErr != nil {
		return harness.Launch{}, a.buildErr
	}
	return harness.Launch{
		Args:       []string{"--dangerously-skip-permissions"},
		Env:        map[string]string{"GOTMPDIR": spec.Scratch, "TEMP": spec.Scratch, "TMP": spec.Scratch},
		PromptFile: spec.BriefPath,
	}, nil
}

type worktreeGit struct {
	events    *[]string
	acquired  string
	top       string
	topErr    error
	returnErr error
	returned  int
}

func (g *worktreeGit) Acquire(_ context.Context, project, path, ref string) (string, error) {
	*g.events = append(*g.events, "worktree-acquire")
	g.acquired = path
	if !filepath.IsAbs(path) || ref != "" {
		return "", fmt.Errorf("unexpected worktree %q on %q", path, ref)
	}
	if project == "" {
		return "", fmt.Errorf("project is required")
	}
	return g.top, nil
}

func (g *worktreeGit) Landing(context.Context, string) (worktree.Landing, error) {
	return worktree.Landing{Landed: true}, nil
}

func (g *worktreeGit) ArchiveTag(context.Context, string, worktree.Landing, string) (string, error) {
	return "", fmt.Errorf("spawn never archives")
}

func (g *worktreeGit) WorktreeTop(context.Context, string) (string, error) {
	*g.events = append(*g.events, "validate-worktree")
	if g.topErr != nil {
		return "", g.topErr
	}
	return g.top, nil
}

func (g *worktreeGit) Return(context.Context, string, string) error {
	g.returned++
	return g.returnErr
}

func (g *worktreeGit) EnsureSeeded(context.Context, string) (bool, error) {
	return false, nil
}

// commandRunner answers the commands a spawn runs besides its terminal:
// provisioning's git questions and the project's own installer.
type commandRunner struct {
	events          *[]string
	calls           int
	installer       string
	installerStderr string
	mcpTracked      bool
}

func (r *commandRunner) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	r.calls++
	if req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "check-ignore" {
		return execx.Result{}, nil
	}
	if req.Name == "git" && len(req.Args) > 0 && req.Args[0] == "ls-files" {
		if r.mcpTracked {
			return execx.Result{Stdout: []byte(".mcp.json\n")}, nil
		}
		return execx.Result{ExitCode: 1}, nil
	}
	if r.installer != "" && req.Name == r.installer {
		*r.events = append(*r.events, "install")
		return execx.Result{ExitCode: 1, Stderr: []byte(r.installerStderr)}, nil
	}
	return execx.Result{}, fmt.Errorf("unexpected command: %#v", req)
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
