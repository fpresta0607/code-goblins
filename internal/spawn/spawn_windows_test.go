package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/goblinname"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
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

// Under a policy whose gate runs on its task's own harness, a goblin on a
// harness no gate can run on could never gate its work, so its spawn is
// refused before anything starts.
func TestSpawnRefusesAGatedTaskOnAHarnessNoGateRunsOn(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "pipeline.json")
	const followsHarness = `{"version": 6, "auto_fix": {"review": 0, "test": 1, "lint": 1, "rebase": 1, "ci": 1}, "classes": {"ordinary": {"review_cycles": 2}, "high-risk": {"review_cycles": 3}, "mechanical": {"review_cycles": 2}}}`
	if err := os.WriteFile(policy, []byte(followsHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("grok", func(t *testing.T) {
		f := newQuickFixture(t)
		f.service.PolicyPath = policy
		f.request.Harness = harness.Kind("grok")

		_, err := f.service.Spawn(context.Background(), f.request)

		if err == nil || !strings.Contains(err.Error(), `"grok"`) || !strings.Contains(err.Error(), "delivery assessment") || !strings.Contains(err.Error(), "--delivery-exception") || strings.Contains(err.Error(), "--mode direct-PR") {
			t.Fatalf("error = %v, want the grok task refused with the assessment or exception route to a mode that needs no gate", err)
		}
		if _, readErr := state.ReadTaskMeta(f.stateDir, f.request.ID); !errors.Is(readErr, os.ErrNotExist) {
			t.Fatalf("the refused spawn left a task record: %v", readErr)
		}
	})
	t.Run("codex", func(t *testing.T) {
		f := newQuickFixture(t)
		f.service.PolicyPath = policy

		result, err := f.service.Spawn(context.Background(), f.request)

		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := pipeline.LoadSelection(filepath.Join(result.Meta.TaskTmp, "pipeline.json"))
		if err != nil || snapshot.Policy.Version != 6 || snapshot.Policy.Primary != (pipeline.Reviewer{}) {
			t.Fatalf("snapshot: %+v %v, want version 6 with no harness named", snapshot, err)
		}
	})
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

// A goblin gets its fun name and title at spawn, published with its record
// so the board names it from the moment it exists, and the title names the
// subject of the work its brief describes.
func TestSpawnGivesTheGoblinANameAndATitleThatNamesItsWork(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	writeFile(t, f.request.BriefPath, strings.ReplaceAll(completedSpawnBrief, "Do the work.", "Keep the Fly token green until it expires."))

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || meta.GoblinName == "" || meta.GoblinTitle != "Token Tamer" {
		t.Fatalf("metadata: %+v %v, want a name and the title Token Tamer", meta, err)
	}
	if want := "goblin: " + meta.GoblinName + " - " + meta.GoblinTitle; !strings.Contains(result.Output, "\n"+want+"\n") && !strings.HasSuffix(result.Output, "\n"+want) {
		t.Fatalf("output = %q, want the line %q", result.Output, want)
	}
}

// A brief that gives its goblin's title gives it as written.
func TestSpawnGivesTheGoblinTheTitleItsBriefGives(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	writeFile(t, f.request.BriefPath, strings.ReplaceAll(completedSpawnBrief, "ready: true", "goblin title: Expiry Elf\nready: true"))

	// Act
	_, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || meta.GoblinName == "" || meta.GoblinTitle != "Expiry Elf" {
		t.Fatalf("metadata: %+v %v, want a name and the title Expiry Elf", meta, err)
	}
}

// A queued task already has its goblin's name and title on its card, and its
// spawn keeps them, so the goblin is called the same in Tasks and In
// progress.
func TestSpawnKeepsTheNameAndTitleTheTaskWasQueuedWith(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	queued, err := goblinname.Reserve(f.stateDir, []goblinname.Work{{ID: f.request.ID, Title: "Smooth resumes"}})
	if err != nil {
		t.Fatal(err)
	}
	want := queued[f.request.ID]
	f.request.Title = "Keep the Fly token green"

	// Act
	_, err = f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, f.request.ID)
	if err != nil || want.Name == "" || meta.GoblinName != want.Name || meta.GoblinTitle != want.Title {
		t.Fatalf("metadata: %+v %v, want the queued pair %+v", meta, err, want)
	}
}

// A ship task is published as native, with its terminal the host named by its
// id and no Herdr pane, and its harness starts there with the task's identity
// and its brief.
func TestSpawnShipPublishesANativeTaskAndBriefsItsHarness(t *testing.T) {
	for _, key := range []string{"CODEX_THREAD_ID", "CLAUDE_CODE_SESSION_ID", "PI_SESSION_ID", "GOBLINS_SESSION_ID", "GOBLINS_SESSION_HARNESS", "GOBLINS_ROOT_SESSION_ID"} {
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
	if got, want := sortedKeys(t, f.stateDir, f.request.ID), []string{"backend", "brief", "credentials", "effort", "endpoint_task_id", "first_gen", "goblin_name", "goblin_title", "harness", "kind", "mode", "model", "project", "scratch", "spawn_gen", "tasktmp", "window", "worktree", "yolo"}; !reflect.DeepEqual(got, want) {
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
	// TMP alone names the folder every goblin shares: Git Bash makes the TMP
	// of its first shell /tmp for every shell of the user, so it can be no
	// task's own folder, which goes with the task.
	sharedTemp := filepath.Join(filepath.Dir(scratch), home.SharedTempDir)
	for name, want := range map[string]string{"GOBLINS_TASK_ID": "task-7", "GOBLINS_ROLE": harness.RoleGoblin, "GOTMPDIR": scratch, "TEMP": scratch, "TMPDIR": scratch, "TMP": sharedTemp, "GOBLINS_STATE_OVERRIDE": f.stateDir} {
		if got := env[name]; got == nil || *got != want {
			t.Errorf("the harness started with %s = %v, want %q", name, got, want)
		}
	}
	if info, statErr := os.Stat(sharedTemp); statErr != nil || !info.IsDir() {
		t.Errorf("stat %q = %v, want the spawn to have made the folder the harness's TMP names", sharedTemp, statErr)
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
	if got, want := sortedKeys(t, f.stateDir, f.request.ID), []string{"backend", "brief", "credentials", "effort", "endpoint_task_id", "first_gen", "goblin_name", "goblin_title", "harness", "kind", "model", "project", "scratch", "spawn_gen", "tasktmp", "window", "worktree"}; !reflect.DeepEqual(got, want) {
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

func TestSpawnLeavesAnEnvFileTheRepositoryTracksAsCheckedOut(t *testing.T) {
	f := newQuickFixture(t)
	// The project commits .env, so git worktree add checks it out. Nothing is
	// shared that no manifest names, so the worktree's file is the
	// repository's own and the spawn has nothing to say about it.
	writeFile(t, filepath.Join(f.project, ".env"), "K=primary\n")
	writeFile(t, filepath.Join(f.worktree, ".env"), "K=checked-out\n")

	result, err := f.service.Spawn(context.Background(), f.request)
	if err != nil {
		t.Fatalf("Spawn: %v, want the goblin dispatched with its checked-out .env", err)
	}
	if strings.Contains(result.Output, "env: the checkout holds") {
		t.Errorf("output = %q, want nothing said about an env file the worktree holds", result.Output)
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
	// GOBLINS_STATE_OVERRIDE is only written at harness start, after the manifest
	// is merged, so it proves the reserved set does not depend on what the
	// launch map happens to hold at merge time. LOCALAPPDATA, XDG_CACHE_HOME
	// and HOME are reserved for a different reason: the launch never writes
	// them, but os.UserCacheDir reads them to derive the task's Go temporary
	// directory, so a redirect would leave any goblins command the goblin runs
	// computing a different directory than spawn created.
	writeWorktreeManifest(t, f.dataDir, f.project, worktree.Manifest{
		Project: "primary",
		Env: map[string]string{
			"gotmpdir":                 `C:\hijacked-gotmpdir`,
			"goblins_state_override":   `C:\hijacked-state`,
			"Goblins_Role":             "overlord",
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
		"GOBLINS_STATE_OVERRIDE":   f.stateDir,
		"GOBLINS_ROLE":             harness.RoleGoblin,
		"PLAYWRIGHT_BROWSERS_PATH": `C:\cache\ms-playwright`,
	} {
		if got := env[name]; got == nil || *got != want {
			t.Errorf("the harness started with %s = %v, want %q", name, got, want)
		}
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

// The capsule is written into the id's own task temporary directory only after
// the alias check, and a spawn that fails before its task is published takes
// the capsule with it, so the retry is not refused by the failed attempt.
func TestSpawnRemovesTheCapsuleOfASpawnThatFailsBeforePublishing(t *testing.T) {
	f := newQuickFixture(t)
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
		return brief, os.WriteFile(brief, []byte(completedSpawnBrief+"\n## CEO durable task capsule\n"), 0o600)
	}
	f.git.acquireErr = errors.New("worktree: add worktree refused")

	if _, err := f.service.Spawn(context.Background(), request); err == nil || !strings.Contains(err.Error(), "add worktree refused") {
		t.Fatalf("Spawn error = %v, want the worktree refusal", err)
	}
	if _, err := os.Stat(taskTmp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("capsule directory %q survived the failed spawn: %v", taskTmp, err)
	}

	f.git.acquireErr = nil
	result, err := f.service.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("retry Spawn error = %v, want the same id spawned with its capsule", err)
	}
	if want := filepath.Join(taskTmp, "brief.md"); result.Meta.Brief != want {
		t.Errorf("meta brief = %q, want the capsule brief %q", result.Meta.Brief, want)
	}
}

// A spawn that fails with its terminal still running keeps the task it
// published, so the capsule's brief stays in the task's temporary directory
// for the kept session and for a resume of it.
func TestSpawnKeepsTheCapsuleOfALaunchItKeeps(t *testing.T) {
	f := newQuickFixture(t)
	taskTmp := filepath.Join(f.stateDir, "tasktmp", f.request.ID)
	request := f.request
	request.Capsule = func(dir string) (string, error) {
		brief := filepath.Join(dir, "brief.md")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return brief, os.WriteFile(brief, []byte(completedSpawnBrief+"\n## CEO durable task capsule\n"), 0o600)
	}
	f.userEnv = append(f.userEnv, fakeCodexMode+"=daybreak-inline", fakeCodexDraft+"=An unfinished user draft")

	_, err := f.service.Spawn(context.Background(), request)

	if err == nil || !strings.Contains(err.Error(), "holds a draft") {
		t.Fatalf("Spawn = %v, want a refusal that keeps the running terminal", err)
	}
	if saved, err := os.ReadFile(filepath.Join(taskTmp, "brief.md")); err != nil || !strings.Contains(string(saved), "CEO durable task capsule") {
		t.Fatalf("capsule brief = %q, %v; want it kept with the session", saved, err)
	}
	terminal, err := host.ReadRecord(f.stateDir, f.request.ID)
	if err != nil || !host.Running(terminal) {
		t.Fatalf("terminal = %+v, %v; want the kept launch's terminal running", terminal, err)
	}
}

// taskScratch is the scratch folder a spawn makes for task id in the
// fixture's home, beside its state folder.
func taskScratch(stateDir, id string) string {
	return filepath.Join(filepath.Dir(stateDir), "scratch", id)
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

// A default spawn used to give the worktree the checkout's .env with no word
// from anyone. It gives none now, and says by name which env files the
// checkout holds that the worktree was not given, with the line that shares
// one, so nobody learns it from a dev server that will not start.
func TestASpawnSharesNoEnvFileTheManifestDoesNotName(t *testing.T) {
	// Arrange
	f := newQuickFixture(t)
	writeFile(t, filepath.Join(f.project, ".env"), "STANDIN_SETTING=the checkout's own line\n")
	writeFile(t, filepath.Join(f.project, ".env.local"), "STANDIN_SETTING=the checkout's own line\n")

	// Act
	result, err := f.service.Spawn(context.Background(), f.request)

	// Assert
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	for _, name := range []string{".env", ".env.local"} {
		if _, err := os.Stat(filepath.Join(f.worktree, name)); !os.IsNotExist(err) {
			t.Errorf("the worktree was given %s, which no manifest names (%v)", name, err)
		}
	}
	for _, want := range []string{"env: the checkout holds .env, .env.local", `"link": [".env", ".env.local"]`, "worktree.json"} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("output = %q, want %q: the spawn names what it did not share and the line that shares it", result.Output, want)
		}
	}
	if strings.Contains(result.Output, "the checkout's own line") {
		t.Error("the spawn's output holds a line of the env file")
	}
	instruction := delivered(t, submittedLines(t, f, 1)[0])
	for _, want := range []string{"holds .env, .env.local, which your worktree was not given", "blocked report", "never copy it yourself"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("instruction = %q, want %q: the goblin is told before its dev server fails", instruction, want)
		}
	}
}
