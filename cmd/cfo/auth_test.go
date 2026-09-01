package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// useFileStore points the credential store at a temporary directory, so a
// test never reads or writes the operator's real vault.
func useFileStore(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "credentials")
	t.Setenv(auth.StoreDirEnv, root)
	return root
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runCLIWithRuntime(t, commandRuntime{}, args...)
}

func runCLIWithRuntime(t *testing.T, runtime commandRuntime, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := runAuth(args, &stdout, &stderr, runtime)
	return code, stdout.String(), stderr.String()
}

func TestAuthStoreAndListKeepTwoProjectsApart(t *testing.T) {
	useFileStore(t)

	for _, args := range [][]string{
		{"store", "--project", "precisiondocs", "DATABASE_URL", "postgres://precisiondocs"},
		{"store", "--project", "clock-in", "DATABASE_URL", "postgres://clock-in"},
		{"store", "GITHUB_TOKEN", "gho_shared_value"},
	} {
		if code, _, stderr := runCLI(t, args...); code != 0 {
			t.Fatalf("cfo auth %v = %d: %s", args, code, stderr)
		}
	}

	code, stdout, stderr := runCLI(t, "list")
	if code != 0 {
		t.Fatalf("cfo auth list = %d: %s", code, stderr)
	}
	for _, want := range []string{"GITHUB_TOKEN", "clock-in/DATABASE_URL", "precisiondocs/DATABASE_URL"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("listing lacks %q:\n%s", want, stdout)
		}
	}
	// A listing is names only; a value in it would defeat the whole store.
	if strings.Contains(stdout, "postgres://") || strings.Contains(stdout, "gho_shared_value") {
		t.Fatalf("listing disclosed a credential:\n%s", stdout)
	}

	code, stdout, stderr = runCLI(t, "list", "--project", "clock-in")
	if code != 0 {
		t.Fatalf("cfo auth list --project = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "clock-in/DATABASE_URL") || strings.Contains(stdout, "precisiondocs") {
		t.Errorf("scoped listing = %q, want only clock-in", stdout)
	}
}

func TestAuthStoreRedactsWhatItConfirms(t *testing.T) {
	useFileStore(t)
	code, stdout, stderr := runCLI(t, "store", "--project", "precisiondocs", "STRIPE_SECRET_KEY", "sk_live_0123456789abcdef")
	if code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "sk_live_0123456789abcdef") {
		t.Fatalf("store confirmation disclosed the credential:\n%s", stdout)
	}
	if !strings.Contains(stdout, "precisiondocs/STRIPE_SECRET_KEY") {
		t.Errorf("confirmation = %q, want the scoped key named", stdout)
	}
}

func TestAuthCopyMovesASharedValueIntoAProjectScopeWithoutReEnteringIt(t *testing.T) {
	useFileStore(t)
	if code, _, stderr := runCLI(t, "store", "DATABASE_URL", "postgres://stored-before-namespacing"); code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}

	code, stdout, stderr := runCLI(t, "copy", "DATABASE_URL", "--to", "precisiondocs")
	if code != 0 {
		t.Fatalf("cfo auth copy = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "postgres://stored-before-namespacing") {
		t.Fatalf("copy disclosed the credential:\n%s", stdout)
	}

	store, err := auth.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	value, found, err := store.Get(auth.Scoped("precisiondocs", "DATABASE_URL"))
	if err != nil || !found || value != "postgres://stored-before-namespacing" {
		t.Fatalf("scoped value = (%q, %v, %v), want the shared value carried over", value, found, err)
	}
	// The shared entry stays: which project owns it is exactly the question
	// this command must not answer on the operator's behalf.
	if _, found, _ := store.Get(auth.Shared("DATABASE_URL")); !found {
		t.Error("copy removed the shared value it was only asked to copy")
	}
}

func TestAuthCopyRefusesWhatIsNotStored(t *testing.T) {
	useFileStore(t)
	code, _, stderr := runCLI(t, "copy", "ABSENT_TOKEN", "--to", "precisiondocs")
	if code == 0 {
		t.Fatal("cfo auth copy = 0 for a credential that is not stored")
	}
	if !strings.Contains(stderr, "ABSENT_TOKEN") {
		t.Errorf("stderr = %q, want the missing name reported", stderr)
	}
}

func TestAuthStoreRefusesAScopeThatCouldEscapeTheStore(t *testing.T) {
	useFileStore(t)
	if code, _, _ := runCLI(t, "store", "--project", "../escaped", "TOKEN_VALUE", "value"); code == 0 {
		t.Error("cfo auth store accepted a traversing project scope")
	}
	if code, _, _ := runCLI(t, "store", "--project", "precisiondocs", "bad name", "value"); code == 0 {
		t.Error("cfo auth store accepted a name that is not an environment variable")
	}
	// A checkout whose name contains dots or a space is not a traversal, and
	// the spawn path already reads and writes credentials under that scope.
	// Refusing it here would leave the operator unable to store the very
	// credential a preflight refusal tells them to store.
	for _, scope := range []string{"docs..example", "Retire 91", "my app (2)", "código"} {
		if code, _, stderr := runCLI(t, "store", "--project", scope, "TOKEN_VALUE", "value"); code != 0 {
			t.Fatalf("cfo auth store --project %q = %d: %s", scope, code, stderr)
		}
		code, stdout, stderr := runCLI(t, "list", "--project", scope)
		if code != 0 {
			t.Fatalf("cfo auth list --project %q = %d: %s", scope, code, stderr)
		}
		if !strings.Contains(stdout, scope+"/TOKEN_VALUE") {
			t.Errorf("listing = %q, want the credential stored under scope %q", stdout, scope)
		}
	}
}

// cmdPanes answers pane liveness from a set of pane ids, so an auth command
// test never needs a Herdr server.
type cmdPanes struct {
	live map[string]bool
}

func (p cmdPanes) Live(_ context.Context, meta state.TaskMeta) bool {
	return p.live[meta.HerdrPaneID]
}

// writeCmdTaskMeta lays down one task record with the directories a live task
// has. Pass wantWorktree false to model a task whose worktree is gone.
func writeCmdTaskMeta(t *testing.T, stateDir, id, project, paneID string, wantWorktree bool) state.TaskMeta {
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

// refreshTestRuntime builds a command runtime whose home, refresher, and
// sender are all test-local. The sent notices are returned through the
// captured pointers so the assertion names exactly what was typed where.
func refreshTestRuntime(t *testing.T, stateDir string, panes cmdPanes, sent *[]string) commandRuntime {
	t.Helper()
	return commandRuntime{
		resolveHome: func() (home.Home, error) {
			return home.Home{Root: t.TempDir(), State: stateDir, Data: filepath.Join(stateDir, "..", "data")}, nil
		},
		authRefresher: func(h home.Home) spawn.AuthRefresher {
			return spawn.AuthRefresher{StateDir: h.State, DataDir: h.Data, Panes: panes}
		},
		sendText: func(_ context.Context, _ home.Home, target, text string, _ bool) error {
			*sent = append(*sent, target+" "+text)
			return nil
		},
	}
}

func TestAuthStoreRefreshesLiveTasksAndSendsTheReSourceNotice(t *testing.T) {
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	// Same project, pane gone: its script must not be reported as refreshed.
	writeCmdTaskMeta(t, stateDir, "parked-1", project, "pane-parked", true)

	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "store", "--project", "precisiondocs", "FLY_API_TOKEN", "fly_new_token")
	if code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "refreshed live-1 auth.ps1 (1 vars)") {
		t.Errorf("stdout = %q, want the live task refreshed by name", stdout)
	}
	if strings.Contains(stdout, "parked-1") {
		t.Errorf("stdout = %q, want the paneless task left alone", stdout)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "gb-live-1 credentials refreshed: re-source ") {
		t.Fatalf("notices = %v, want exactly one re-source notice to gb-live-1", sent)
	}
	if !strings.Contains(sent[0], filepath.Join(live.TaskTmp, "auth.ps1")) {
		t.Errorf("notice = %q, want the script path named", sent[0])
	}
	script, err := os.ReadFile(filepath.Join(live.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fly_new_token'") {
		t.Errorf("script lacks the credential stored after spawn:\n%s", script)
	}
}

func TestAuthCopyIntoAProjectScopeRefreshesItsLiveTasks(t *testing.T) {
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)

	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	if code, _, stderr := runCLIWithRuntime(t, runtime, "store", "DATABASE_URL", "postgres://shared-value"); code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	sent = nil
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "copy", "DATABASE_URL", "--to", "precisiondocs")
	if code != 0 {
		t.Fatalf("cfo auth copy = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "refreshed live-1 auth.ps1 (1 vars)") {
		t.Errorf("stdout = %q, want the live task refreshed", stdout)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "gb-live-1 credentials refreshed") {
		t.Fatalf("notices = %v, want one re-source notice", sent)
	}
}

func TestAuthStoreWithNoReachablePaneSaysTheSnapshotMayBeStale(t *testing.T) {
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	// A Herdr outage: the record and its directories remain, no pane answers.
	writeCmdTaskMeta(t, stateDir, "out-1", project, "pane-out", true)

	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{}}, &sent)
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "store", "--project", "precisiondocs", "FLY_API_TOKEN", "fly_new_token")
	if code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	if strings.Contains(stdout, "refreshed") {
		t.Errorf("stdout = %q, want nothing reported as refreshed", stdout)
	}
	if !strings.Contains(stderr, "no pane could be confirmed reachable") || !strings.Contains(stderr, "precisiondocs") {
		t.Errorf("stderr = %q, want the outage note naming the scope", stderr)
	}
	if len(sent) != 0 {
		t.Errorf("notices = %v, want none delivered to an unreachable fleet", sent)
	}
}

func TestAuthRefreshCommandRegeneratesOneTaskAndRefusesTheRest(t *testing.T) {
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	// A cleaned-up task: metadata retired, scratch state archived.
	if err := os.MkdirAll(filepath.Join(stateDir, "archive", "old-1.20260102T150405Z"), 0o755); err != nil {
		t.Fatal(err)
	}

	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	// Unknown and archived ids are refused with exit 1.
	if code, _, stderr := runCLIWithRuntime(t, runtime, "refresh", "ghost-1"); code != 1 || !strings.Contains(stderr, "unknown task") {
		t.Fatalf("refresh ghost-1 = (%d, %q), want exit 1 naming the unknown task", code, stderr)
	}
	if code, _, stderr := runCLIWithRuntime(t, runtime, "refresh", "old-1"); code != 1 || !strings.Contains(stderr, "archived") {
		t.Fatalf("refresh old-1 = (%d, %q), want exit 1 naming the archive", code, stderr)
	}
	// A live id regenerates from the store and delivers the notice.
	if code, _, stderr := runCLIWithRuntime(t, runtime, "store", "--project", "precisiondocs", "FLY_API_TOKEN", "fly_new_token"); code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	sent = nil
	code, stdout, _ := runCLIWithRuntime(t, runtime, "refresh", "live-1")
	if code != 0 {
		t.Fatalf("cfo auth refresh = %d", code)
	}
	if !strings.Contains(stdout, "refreshed live-1 auth.ps1 (1 vars)") {
		t.Errorf("stdout = %q, want the refresh line", stdout)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "gb-live-1 credentials refreshed: re-source ") {
		t.Fatalf("notices = %v, want exactly one notice naming the script", sent)
	}
	script, err := os.ReadFile(filepath.Join(live.TaskTmp, "auth.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "$env:FLY_API_TOKEN = 'fly_new_token'") {
		t.Errorf("script lacks the stored credential:\n%s", script)
	}
}
