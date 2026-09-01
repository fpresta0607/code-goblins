package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// stubPanes answers pane liveness from a set of pane ids, so a refresh test
// never needs a Herdr server.
type stubPanes struct {
	live map[string]bool
}

func (p stubPanes) Live(_ context.Context, meta state.TaskMeta) bool {
	return p.live[meta.HerdrPaneID]
}

// writeTaskMeta lays down one task record with the directories a live task
// has: a worktree and a tasktmp. Pass wantWorktree false to model a task
// whose worktree is gone.
func writeTaskMeta(t *testing.T, stateDir, id, project, paneID string, wantWorktree bool) state.TaskMeta {
	t.Helper()
	worktreeDir := filepath.Join(stateDir, "worktrees", id)
	if wantWorktree {
		if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	taskTmp := filepath.Join(stateDir, "tasktmp", id)
	if err := os.MkdirAll(taskTmp, 0o700); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{
		ID:               id,
		Project:          project,
		Worktree:         worktreeDir,
		TaskTmp:          taskTmp,
		Kind:             "ship",
		Mode:             "direct-PR",
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws",
		HerdrTabID:       "tab-" + id,
		HerdrPaneID:      paneID,
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func useCredentialStore(t *testing.T) auth.Store {
	t.Helper()
	t.Setenv(auth.StoreDirEnv, filepath.Join(t.TempDir(), "credentials"))
	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestAuthRefreshRewritesOnlyLiveTasksOfTheProject(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	other := filepath.Join(root, "clock-in")
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "FLY_API_TOKEN"), "fly_new_token"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(other, "DATABASE_URL"), "postgres://other"); err != nil {
		t.Fatal(err)
	}

	live := writeTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	// Same project, but its worktree is gone: not live, not refreshed.
	dead := writeTaskMeta(t, stateDir, "dead-1", project, "pane-dead", true)
	if err := os.Remove(dead.Worktree); err != nil {
		t.Fatal(err)
	}
	// Another project's live task must never see this project's scope.
	foreign := writeTaskMeta(t, stateDir, "foreign-1", other, "pane-foreign", true)

	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{
		"pane-live":    true,
		"pane-dead":    true,
		"pane-foreign": true,
	}}}
	refreshed, err := refresher.RefreshProject(context.Background(), project)
	if err != nil {
		t.Fatalf("RefreshProject: %v", err)
	}
	if len(refreshed) != 1 || refreshed[0].ID != "live-1" {
		t.Fatalf("refreshed = %v, want exactly live-1", refreshed)
	}
	if !refreshed[0].Live {
		t.Errorf("live-1 = Live false, want the pane reported live")
	}
	script, err := os.ReadFile(filepath.Join(live.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatalf("read refreshed script: %v", err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fly_new_token'") {
		t.Errorf("script lacks the credential stored after spawn:\n%s", script)
	}
	if strings.Contains(string(script), "postgres://other") {
		t.Errorf("script leaked another project's scope:\n%s", script)
	}
	if _, err := os.Stat(filepath.Join(dead.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
		t.Errorf("dead task's script = %v, want it untouched", err)
	}
	if _, err := os.Stat(filepath.Join(foreign.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
		t.Errorf("foreign task's script = %v, want it untouched", err)
	}
}

func TestAuthRefreshIncludesStoreOnlyVariablesAbsentFromTheManifest(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	dataDir := filepath.Join(root, "data")
	project := filepath.Join(root, "precisiondocs")
	// The manifest declares exactly one variable; the operator stored two.
	manifest := `{"project": "precisiondocs", "services": [{"name": "fly", "method": "env", "env": ["DATABASE_URL"], "probe": ["flyctl", "status"]}]}`
	if err := os.MkdirAll(filepath.Join(dataDir, "projects", "precisiondocs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "projects", "precisiondocs", "auth.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "DATABASE_URL"), "postgres://declared"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(project, "OPENROUTER_API_KEY"), "sk_or_stored_midtask"); err != nil {
		t.Fatal(err)
	}

	live := writeTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{"pane-live": true}}}
	refreshed, err := refresher.RefreshProject(context.Background(), project)
	if err != nil {
		t.Fatalf("RefreshProject: %v", err)
	}
	if len(refreshed) != 1 || refreshed[0].Vars != 2 {
		t.Fatalf("refreshed = %v, want live-1 with both variables", refreshed)
	}
	script, err := os.ReadFile(filepath.Join(live.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	// A manifest is the probe contract, not a filter: OPENROUTER_API_KEY is
	// stored under this project's scope, so the pane gets it whether or not
	// any manifest declared it.
	if !strings.Contains(string(script), "$env:OPENROUTER_API_KEY = 'sk_or_stored_midtask'") {
		t.Errorf("script lacks the store-only variable:\n%s", script)
	}
	if !strings.Contains(string(script), "$env:DATABASE_URL = 'postgres://declared'") {
		t.Errorf("script lacks the declared variable:\n%s", script)
	}
}

func TestAuthRefreshWithNoLiveTasksTouchesNothing(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	store := useCredentialStore(t)
	// A task whose pane is gone: metadata and directories remain, but no
	// pane can re-source anything.
	writeTaskMeta(t, stateDir, "parked-1", project, "pane-parked", true)

	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{}}}
	refreshed, err := refresher.RefreshProject(context.Background(), project)
	if err != nil {
		t.Fatalf("RefreshProject: %v", err)
	}
	if len(refreshed) != 0 {
		t.Fatalf("refreshed = %v, want nothing", refreshed)
	}
	entries, err := os.ReadDir(filepath.Join(stateDir, "tasktmp"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, err := os.Stat(filepath.Join(stateDir, "tasktmp", entry.Name(), "auth.ps1")); !os.IsNotExist(err) {
			t.Errorf("tasktmp %s gained an auth.ps1 with no live task", entry.Name())
		}
	}
}

func TestAuthRefreshTaskRefusesArchivedAndUnknownIDs(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	store := useCredentialStore(t)
	// A cleaned-up task: metadata retired, scratch state archived.
	if err := os.MkdirAll(filepath.Join(stateDir, "archive", "old-1.20260102T150405Z"), 0o755); err != nil {
		t.Fatal(err)
	}

	refresher := AuthRefresher{StateDir: stateDir, Store: store}
	if _, err := refresher.RefreshTask(context.Background(), "old-1"); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("err = %v, want an archived refusal", err)
	}
	if _, err := refresher.RefreshTask(context.Background(), "ghost-1"); err == nil || !strings.Contains(err.Error(), "unknown task") {
		t.Fatalf("err = %v, want an unknown-task refusal", err)
	}
	if _, err := refresher.RefreshTask(context.Background(), "../escape"); err == nil {
		t.Fatal("RefreshTask accepted an invalid task id")
	}
}

func TestAuthRefreshTaskRegeneratesOneTaskWithoutRequiringAWorktree(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "FLY_API_TOKEN"), "fly_new_token"); err != nil {
		t.Fatal(err)
	}
	// An explicit refresh is the operator's call: the worktree may be gone
	// while the pane still runs, and the script is still the task's own.
	meta := writeTaskMeta(t, stateDir, "live-1", project, "pane-live", false)
	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{"pane-live": true}}}
	refreshed, err := refresher.RefreshTask(context.Background(), "live-1")
	if err != nil {
		t.Fatalf("RefreshTask: %v", err)
	}
	if refreshed.ID != "live-1" || refreshed.Vars != 1 || !refreshed.Live {
		t.Fatalf("refreshed = %+v, want live-1, 1 var, live pane", refreshed)
	}
	script, err := os.ReadFile(filepath.Join(meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fly_new_token'") {
		t.Errorf("script lacks the stored credential:\n%s", script)
	}
}

func TestAuthRefreshDropsReservedLaunchNamesFromTheStore(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "GOTMPDIR"), `C:\hijacked`); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(project, "SAFE_KEY"), "value"); err != nil {
		t.Fatal(err)
	}
	meta := writeTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{"pane-live": true}}}
	if _, err := refresher.RefreshProject(context.Background(), project); err != nil {
		t.Fatalf("RefreshProject: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(meta.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "hijacked") {
		t.Errorf("a stored credential redirected the launch contract:\n%s", script)
	}
	if !strings.Contains(string(script), "$env:SAFE_KEY = 'value'") {
		t.Errorf("script dropped an unrelated credential:\n%s", script)
	}
}

func TestAuthRefreshTaskAcceptsAnIDRespawnedAfterCleanup(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	store := useCredentialStore(t)
	if err := store.Set(auth.Scoped(project, "FLY_API_TOKEN"), "fly_new_token"); err != nil {
		t.Fatal(err)
	}
	// Cleanup archived the first run under a stamped name and freed the id;
	// the second run of the same id is live beside that archive.
	if err := os.MkdirAll(filepath.Join(stateDir, "archive", "reused-1.20260102T150405Z"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := writeTaskMeta(t, stateDir, "reused-1", project, "pane-live", true)
	refresher := AuthRefresher{StateDir: stateDir, Store: store, Panes: stubPanes{live: map[string]bool{"pane-live": true}}}
	refreshed, err := refresher.RefreshTask(context.Background(), "reused-1")
	if err != nil {
		t.Fatalf("RefreshTask refused a live respawned id: %v", err)
	}
	if refreshed.ID != "reused-1" || refreshed.Vars != 1 || !refreshed.Live {
		t.Fatalf("refreshed = %+v, want reused-1, 1 var, live pane", refreshed)
	}
	if _, err := os.Stat(filepath.Join(meta.TaskTmp, "auth.ps1")); err != nil {
		t.Fatalf("respawned task's script = %v, want it regenerated", err)
	}
}
