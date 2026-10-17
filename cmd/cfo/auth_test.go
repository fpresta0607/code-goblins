package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/harness"
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
		{"store", "GITHUB_TOKEN", "gho_shared_value_0123456789"},
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

// A value that cannot be the credential its name names is refused before
// anything is stored, by an argument, a pipe or the console alike, saying why
// and never repeating it: the Overlord's STRIPE_TEST_SECRET_KEY once arrived
// as the single Ctrl+V a paste with nothing to paste types.
func TestAuthStoreRefusesAValueOfTheWrongShapeWithoutRepeatingIt(t *testing.T) {
	random := "q7Lm2Xv9Rt4Kp8Zw3Nb6Hd1Yc5Fg0Js"
	for _, test := range []struct {
		name, value, stdin, says string
	}{
		{"one character as an argument", "s", "", "STRIPE_TEST_SECRET_KEY does not start with sk_test_ or rk_test_"},
		{"a publishable key as an argument", "pk" + "_test_" + random, "", "STRIPE_TEST_SECRET_KEY does not start with sk_test_ or rk_test_"},
		{"a Ctrl+V on stdin", "", "\x16\r\n", "STRIPE_TEST_SECRET_KEY holds a control character"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			storeDir := useFileStore(t)
			args := []string{"store", "--project", "precisiondocs", "STRIPE_TEST_SECRET_KEY"}
			if test.value != "" {
				args = append(args, test.value)
			} else {
				input, err := os.CreateTemp(t.TempDir(), "stdin")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { input.Close() })
				if _, err := input.WriteString(test.stdin); err != nil {
					t.Fatal(err)
				}
				if _, err := input.Seek(0, 0); err != nil {
					t.Fatal(err)
				}
				originalStdin := os.Stdin
				os.Stdin = input
				t.Cleanup(func() { os.Stdin = originalStdin })
			}

			// Act
			code, stdout, stderr := runCLI(t, args...)

			// Assert
			if code != 2 || !strings.Contains(stderr, test.says) || !strings.Contains(stderr, "Nothing was stored") {
				t.Fatalf("cfo auth store = %d %q, want 2 saying %q", code, stderr, test.says)
			}
			if len(test.value) > 1 && strings.Contains(stdout+stderr, test.value) {
				t.Fatal("the refusal repeats the value")
			}
			if _, err := os.Stat(filepath.Join(storeDir, "precisiondocs", "STRIPE_TEST_SECRET_KEY")); !os.IsNotExist(err) {
				t.Fatal("a value of the wrong shape was stored")
			}
		})
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
		Credentials:      []string{"stored"},
		HasCredentials:   true,
	}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// declareCmdStored writes project's manifest, in the data folder beside
// stateDir that refreshTestRuntime's home names, with one service, stored,
// that declares names. The tasks writeCmdTaskMeta records carry that service,
// so what a test stores under those names is what their scripts are given.
func declareCmdStored(t *testing.T, stateDir, project string, names ...string) {
	t.Helper()
	path := auth.ManifestPath(filepath.Join(stateDir, "..", "data"), project)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"project": "` + auth.ProjectName(project) + `", "services": [{"name": "stored", "method": "env", "env": ["` + strings.Join(names, `", "`) + `"]}, {"name": "extra", "method": "env", "env": ["EXTRA_STANDIN_KEY"]}]}`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
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
		sendText: func(_ context.Context, _ home.Home, target, text string) error {
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
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
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
	declareCmdStored(t, stateDir, project, "DATABASE_URL")
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
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
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

// scriptSets reports whether a credential script sets the variable name.
func scriptSets(t *testing.T, path, name string) bool {
	t.Helper()
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(script), "$env:"+name+" = ")
}

// Granting a service is one command: the task's record names it, so every
// later terminal carries it, and the running goblin is told to load it. Its
// output and the notice name services and a path, never a value.
func TestAuthGrantGivesARunningTaskOneMoreServiceByName(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	other := writeCmdTaskMeta(t, stateDir, "other-1", project, "pane-other", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true, "pane-other": true}}, &sent)
	for name, value := range map[string]string{"FLY_API_TOKEN": "stand-in-fly", "EXTRA_STANDIN_KEY": "stand-in-extra"} {
		if code, _, stderr := runCLI(t, "store", "--project", "precisiondocs", name, value); code != 0 {
			t.Fatalf("cfo auth store = %d: %s", code, stderr)
		}
	}

	// Act
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "grant", "live-1", "extra")

	// Assert
	if code != 0 {
		t.Fatalf("cfo auth grant = %d: %s", code, stderr)
	}
	after, err := state.ReadTaskMeta(stateDir, "live-1")
	if err != nil || strings.Join(after.Credentials, ",") != "extra,stored" {
		t.Errorf("after the grant the record names %v, %v, want extra beside stored", after.Credentials, err)
	}
	script := filepath.Join(live.TaskTmp, "auth.ps1")
	if !scriptSets(t, script, "EXTRA_STANDIN_KEY") || !scriptSets(t, script, "FLY_API_TOKEN") {
		t.Error("the task's script lacks a name its services declare")
	}
	if !strings.Contains(stdout, "granted live-1 extra. It carries extra, stored") {
		t.Errorf("stdout = %q, want the grant and what the task carries, by service name", stdout)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "gb-live-1 credentials refreshed: re-source ") {
		t.Errorf("notices = %v, want the one goblin told to load its script", sent)
	}
	untouched, err := state.ReadTaskMeta(stateDir, "other-1")
	if err != nil || strings.Join(untouched.Credentials, ",") != "stored" {
		t.Errorf("another task of the project names %v, %v, want it as it was", untouched.Credentials, err)
	}
	if _, err := os.Stat(filepath.Join(other.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
		t.Errorf("another task's script was written by the grant: %v", err)
	}
	for _, text := range append([]string{stdout, stderr}, sent...) {
		if strings.Contains(text, "stand-in-fly") || strings.Contains(text, "stand-in-extra") {
			t.Errorf("the grant's words hold a credential value: %q", text)
		}
	}
}

func TestAuthGrantRefusesWhatItCannotGrant(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"a service the manifest does not declare", []string{"grant", "live-1", "billing"}, 1, "It declares extra, stored"},
		{"a task that is not running", []string{"grant", "ghost-1", "extra"}, 1, "not a running task"},
		{"no service named", []string{"grant", "live-1"}, 2, "usage"},
		{"an MCP server the project does not define", []string{"grant", "live-1", "--mcp", "unheard-of"}, 1, "defines no server named unheard-of"},
		{"no MCP server named", []string{"grant", "live-1", "--mcp"}, 2, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			code, _, stderr := runCLIWithRuntime(t, runtime, tc.args...)

			// Assert
			if code != tc.code || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit = %d, stderr = %q, want exit %d naming %q", code, stderr, tc.code, tc.want)
			}
			after, err := state.ReadTaskMeta(stateDir, "live-1")
			if err != nil || strings.Join(after.Credentials, ",") != "stored" || len(after.MCPServers) != 0 {
				t.Errorf("the record names %v and the MCP servers %v, %v, want it unchanged", after.Credentials, after.MCPServers, err)
			}
		})
	}
}

// A goblin that needs a service says so in a blocked report. It never grants
// itself one or revokes one, and neither does a gate agent.
func TestAuthGrantAndRevokeAreRefusedInAGoblinsOrAGateAgentsTerminal(t *testing.T) {
	commands := []struct {
		args []string
		want string
	}{
		{[]string{"grant", "live-1", "extra"}, "blocked report"},
		{[]string{"grant", "live-1", "--mcp", "literal-env"}, "blocked report"},
		{[]string{"revoke", "live-1", "stored"}, "the CFO revokes it"},
		{[]string{"revoke", "live-1", "--mcp", "granted-server"}, "the CFO revokes it"},
		{[]string{"grant", "live-1", "--codex-credits"}, "blocked report"},
		{[]string{"revoke", "live-1", "--codex-credits"}, "blocked report"},
	}
	for _, variable := range []string{harness.RoleVariable, gateAgentVariable} {
		for _, command := range commands {
			t.Run(variable+" "+strings.Join(command.args, " "), func(t *testing.T) {
				// Arrange
				t.Setenv(harness.RoleVariable, "")
				t.Setenv(gateAgentVariable, "")
				value := "1"
				if variable == harness.RoleVariable {
					value = harness.RoleGoblin
				}
				t.Setenv(variable, value)
				useFileStore(t)
				root := t.TempDir()
				stateDir := filepath.Join(root, "state")
				project := filepath.Join(root, "precisiondocs")
				declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
				writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
				if err := state.WriteTaskMCPServers(stateDir, "live-1", []string{"granted-server"}); err != nil {
					t.Fatal(err)
				}
				isAuthorized := command.args[0] == "revoke"
				if err := state.WriteTaskCodexCredits(stateDir, "live-1", isAuthorized); err != nil {
					t.Fatal(err)
				}
				var sent []string
				runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)

				// Act
				code, _, stderr := runCLIWithRuntime(t, runtime, command.args...)

				// Assert
				if code != 2 || !strings.Contains(stderr, "the CFO's act") || !strings.Contains(stderr, command.want) {
					t.Errorf("exit = %d, stderr = %q, want it refused, naming whose act it is and %q", code, stderr, command.want)
				}
				after, err := state.ReadTaskMeta(stateDir, "live-1")
				if err != nil || strings.Join(after.Credentials, ",") != "stored" || strings.Join(after.MCPServers, ",") != "granted-server" || after.CodexCredits != isAuthorized {
					t.Errorf("the record names %v and the MCP servers %v with Codex credits %v, %v, want it unchanged", after.Credentials, after.MCPServers, after.CodexCredits, err)
				}
			})
		}
	}
}

// Revoking a service is one command: the task's record no longer names it, so
// no later terminal carries it, and its credential script is rewritten
// without it. The terminal running now keeps what it already holds, which
// the output says along with the restart that ends it, and it is told nothing
// to load, since loading the script takes no service's variable away. It
// names services and a path, never a value.
func TestAuthRevokeTakesAServiceOutOfARunningTasksRecord(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	other := writeCmdTaskMeta(t, stateDir, "other-1", project, "pane-other", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true, "pane-other": true}}, &sent)
	for name, value := range map[string]string{"FLY_API_TOKEN": "stand-in-fly", "EXTRA_STANDIN_KEY": "stand-in-extra"} {
		if code, _, stderr := runCLI(t, "store", "--project", "precisiondocs", name, value); code != 0 {
			t.Fatalf("cfo auth store = %d: %s", code, stderr)
		}
	}
	if code, _, stderr := runCLIWithRuntime(t, runtime, "grant", "live-1", "extra"); code != 0 {
		t.Fatalf("cfo auth grant = %d: %s", code, stderr)
	}
	script := filepath.Join(live.TaskTmp, "auth.ps1")
	if !scriptSets(t, script, "EXTRA_STANDIN_KEY") {
		t.Fatal("the premise is a script the grant gave EXTRA_STANDIN_KEY")
	}
	sent = nil

	// Act
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "revoke", "live-1", "extra")

	// Assert
	if code != 0 {
		t.Fatalf("cfo auth revoke = %d: %s", code, stderr)
	}
	after, err := state.ReadTaskMeta(stateDir, "live-1")
	if err != nil || strings.Join(after.Credentials, ",") != "stored" {
		t.Errorf("after the revoke the record names %v, %v, want stored alone", after.Credentials, err)
	}
	if scriptSets(t, script, "EXTRA_STANDIN_KEY") || !scriptSets(t, script, "FLY_API_TOKEN") {
		t.Error("the task's script still sets the revoked service's name, or lost the name of the one it keeps")
	}
	for _, want := range []string{"revoked extra from live-1. Its record names stored", "running now keeps", "until it ends", "`cfo switch live-1 --restart`"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	if len(sent) != 0 {
		t.Errorf("notices = %v, want none: loading the script takes nothing back", sent)
	}
	log, err := state.TailStatus(stateDir, "live-1", 10)
	if err != nil || !strings.Contains(strings.Join(log, "\n"), "credentials: the CFO revoked extra") {
		t.Errorf("status log = %q, %v, want a record of the revoke", log, err)
	}
	untouched, err := state.ReadTaskMeta(stateDir, "other-1")
	if err != nil || strings.Join(untouched.Credentials, ",") != "stored" {
		t.Errorf("another task of the project names %v, %v, want it as it was", untouched.Credentials, err)
	}
	if _, err := os.Stat(filepath.Join(other.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
		t.Errorf("another task's script was written by the revoke: %v", err)
	}
	for _, text := range append([]string{stdout, stderr, strings.Join(log, "\n")}, sent...) {
		if strings.Contains(text, "stand-in-fly") || strings.Contains(text, "stand-in-extra") {
			t.Errorf("the revoke's words hold a credential value: %q", text)
		}
	}
}

// Revoking what the task does not hold says so and changes nothing, and a
// command missing its names is a usage error.
func TestAuthRevokeRefusesWhatTheTaskDoesNotHold(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"a service the manifest declares that the task does not carry", []string{"revoke", "live-1", "extra"}, 1, "live-1 does not carry extra, so nothing was revoked. It carries stored"},
		{"a service the manifest does not declare", []string{"revoke", "live-1", "billing"}, 1, "live-1 does not carry billing, so nothing was revoked"},
		{"an MCP server its record does not name", []string{"revoke", "live-1", "--mcp", "literal-env"}, 1, "live-1's record names no MCP server literal-env, so nothing was revoked. It names none"},
		{"a task that is not running", []string{"revoke", "ghost-1", "stored"}, 1, "not a running task"},
		{"no service named", []string{"revoke", "live-1"}, 2, "usage"},
		{"no MCP server named", []string{"revoke", "live-1", "--mcp"}, 2, "usage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			code, stdout, stderr := runCLIWithRuntime(t, runtime, tc.args...)

			// Assert
			if code != tc.code || !strings.Contains(stderr, tc.want) || stdout != "" {
				t.Errorf("exit = %d, stdout = %q, stderr = %q, want exit %d naming %q and nothing done", code, stdout, stderr, tc.code, tc.want)
			}
			after, err := state.ReadTaskMeta(stateDir, "live-1")
			if err != nil || strings.Join(after.Credentials, ",") != "stored" || len(after.MCPServers) != 0 {
				t.Errorf("the record names %v and the MCP servers %v, %v, want it unchanged", after.Credentials, after.MCPServers, err)
			}
			if _, err := os.Stat(filepath.Join(live.TaskTmp, "auth.ps1")); !os.IsNotExist(err) {
				t.Errorf("the refused revoke wrote the task's script: %v", err)
			}
			if log, _ := state.TailStatus(stateDir, "live-1", 10); len(log) != 0 || len(sent) != 0 {
				t.Errorf("status log = %q, notices = %v, want nothing recorded or sent", log, sent)
			}
		})
	}
}

// Revoking an MCP server is the same command with --mcp: the task's record no
// longer names the server, so its next terminal is not given it. A harness
// reads its servers when it starts, so the one running now keeps it, and the
// output names the restart that ends it.
func TestAuthRevokeMCPTakesAServerOutOfARunningTasksRecord(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(standInCmdMCP), 0o644); err != nil {
		t.Fatal(err)
	}
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)
	if code, _, stderr := runCLIWithRuntime(t, runtime, "grant", "live-1", "--mcp", "literal-env", "plain"); code != 0 {
		t.Fatalf("cfo auth grant --mcp = %d: %s", code, stderr)
	}

	// Act
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "revoke", "live-1", "--mcp", "literal-env")

	// Assert
	if code != 0 {
		t.Fatalf("cfo auth revoke --mcp = %d: %s", code, stderr)
	}
	after, err := state.ReadTaskMeta(stateDir, "live-1")
	if err != nil || strings.Join(after.MCPServers, ",") != "plain" || strings.Join(after.Credentials, ",") != "stored" {
		t.Errorf("after the revoke the record names the MCP servers %v and the services %v, %v, want plain alone and nothing else changed", after.MCPServers, after.Credentials, err)
	}
	for _, want := range []string{"revoked the MCP servers literal-env from live-1. Its record names plain", "running now keeps", "`cfo switch live-1 --restart`"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	log, err := state.TailStatus(stateDir, "live-1", 10)
	if err != nil || !strings.Contains(strings.Join(log, "\n"), "credentials: the CFO revoked the MCP servers literal-env") {
		t.Errorf("status log = %q, %v, want a record of the revoke", log, err)
	}
	if len(sent) != 0 {
		t.Errorf("notices = %v, want none: a running harness drops no MCP server", sent)
	}
	for _, text := range []string{stdout, stderr, strings.Join(log, "\n")} {
		if strings.Contains(text, "stand-in-mcp-env-value") {
			t.Errorf("the revoke's words hold a value of the server's entry: %q", text)
		}
	}
}

// Storing a name no service declares used to put it into every terminal of
// the project. It reaches none now, and the store says so instead of leaving
// it to read as a credential every goblin has.
func TestAuthStoreSaysWhenNoServiceDeclaresTheName(t *testing.T) {
	// Arrange
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	live := writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true}}, &sent)

	// Act
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "store", "--project", "precisiondocs", "STRAY_STANDIN_KEY", "stand-in-stray")
	_, declared, _ := runCLIWithRuntime(t, runtime, "store", "--project", "precisiondocs", "FLY_API_TOKEN", "stand-in-fly")

	// Assert
	if code != 0 {
		t.Fatalf("cfo auth store = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "declares STRAY_STANDIN_KEY, so it reaches no task yet") || !strings.Contains(stdout, filepath.Join("data", "projects", "precisiondocs", "auth.json")) {
		t.Errorf("stdout = %q, want it said that the name reaches no task, naming the manifest", stdout)
	}
	if strings.Contains(declared, "reaches no task") {
		t.Errorf("stdout = %q, want nothing said about a name a service declares", declared)
	}
	if scriptSets(t, filepath.Join(live.TaskTmp, "auth.ps1"), "STRAY_STANDIN_KEY") {
		t.Error("the task's script holds a name no service declares")
	}
}

// standInCmdMCP is a project's MCP configuration under made-up names and
// values: one server whose entry holds a value and one that holds none.
const standInCmdMCP = `{"mcpServers": {
	"literal-env": {"command": "standin-server", "env": {"STANDIN_MCP_KEY": "stand-in-mcp-env-value"}},
	"plain": {"command": "standin-server"}
}}`

// Authorizing a task to spend paid Codex credits is the grant with
// --codex-credits, and withdrawing it the revoke: the task's record says so,
// its status log says who changed it, the output says when its codex guard
// follows, and no other task changes. A task that is not running has no guard
// to change.
func TestAuthGrantCodexCreditsAuthorizesARunningTaskAndRevokeWithdrawsIt(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	writeCmdTaskMeta(t, stateDir, "other-1", project, "pane-other", true)
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true, "pane-other": true}}, &sent)

	// Act
	grantCode, grantOut, grantErr := runCLIWithRuntime(t, runtime, "grant", "live-1", "--codex-credits")
	granted, grantedErr := state.ReadTaskMeta(stateDir, "live-1")
	revokeCode, revokeOut, revokeErr := runCLIWithRuntime(t, runtime, "revoke", "live-1", "--codex-credits")
	revoked, revokedErr := state.ReadTaskMeta(stateDir, "live-1")
	ghostCode, _, ghostErr := runCLIWithRuntime(t, runtime, "grant", "ghost-1", "--codex-credits")

	// Assert
	if grantCode != 0 || grantedErr != nil || !granted.CodexCredits || !strings.Contains(grantOut, "authorized live-1 to spend paid Codex credits") {
		t.Errorf("grant = %d, %q, %q; record credits %v, %v; want live-1 authorized", grantCode, grantOut, grantErr, granted.CodexCredits, grantedErr)
	}
	if revokeCode != 0 || revokedErr != nil || revoked.CodexCredits || !strings.Contains(revokeOut, "withdrew the authorization of live-1") {
		t.Errorf("revoke = %d, %q, %q; record credits %v, %v; want the authorization withdrawn", revokeCode, revokeOut, revokeErr, revoked.CodexCredits, revokedErr)
	}
	if strings.Join(granted.Credentials, ",") != "stored" || strings.Join(revoked.Credentials, ",") != "stored" {
		t.Errorf("the record names the services %v, then %v, want them unchanged", granted.Credentials, revoked.Credentials)
	}
	if ghostCode != 1 || !strings.Contains(ghostErr, "ghost-1 is not a running task") {
		t.Errorf("grant to a task that is not running = %d, %q, want it refused", ghostCode, ghostErr)
	}
	log, err := state.TailStatus(stateDir, "live-1", 10)
	if joined := strings.Join(log, "\n"); err != nil || !strings.Contains(joined, "credentials: the CFO authorized the task to spend paid Codex credits") || !strings.Contains(joined, "credentials: the CFO withdrew the task's authorization") {
		t.Errorf("status log = %q, %v, want a record of the grant and of the revoke", log, err)
	}
	if untouched, err := state.ReadTaskMeta(stateDir, "other-1"); err != nil || untouched.CodexCredits {
		t.Errorf("another task's record has Codex credits %v, %v, want none", untouched.CodexCredits, err)
	}
	if len(sent) != 0 {
		t.Errorf("notices = %v, want none: the guard follows the record, and the terminal loads nothing", sent)
	}
}

// Granting an MCP server is the same command with --mcp: the task's record
// names the server, its status log says who granted it, and the output says
// when the task is given it. A harness reads its servers when it starts, so
// no running terminal is told to load anything. It names servers, never a
// value.
func TestAuthGrantMCPNamesAServerInARunningTasksRecord(t *testing.T) {
	// Arrange
	t.Setenv(harness.RoleVariable, "")
	t.Setenv(gateAgentVariable, "")
	useFileStore(t)
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	project := filepath.Join(root, "precisiondocs")
	declareCmdStored(t, stateDir, project, "FLY_API_TOKEN")
	writeCmdTaskMeta(t, stateDir, "live-1", project, "pane-live", true)
	writeCmdTaskMeta(t, stateDir, "other-1", project, "pane-other", true)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".mcp.json"), []byte(standInCmdMCP), 0o644); err != nil {
		t.Fatal(err)
	}
	var sent []string
	runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{"pane-live": true, "pane-other": true}}, &sent)

	// Act
	code, stdout, stderr := runCLIWithRuntime(t, runtime, "grant", "live-1", "--mcp", "literal-env")

	// Assert
	if code != 0 {
		t.Fatalf("cfo auth grant --mcp = %d: %s", code, stderr)
	}
	after, err := state.ReadTaskMeta(stateDir, "live-1")
	if err != nil || strings.Join(after.MCPServers, ",") != "literal-env" || strings.Join(after.Credentials, ",") != "stored" {
		t.Errorf("after the grant the record names the MCP servers %v and the services %v, %v, want literal-env added and nothing else changed", after.MCPServers, after.Credentials, err)
	}
	for _, want := range []string{"granted live-1 the MCP servers literal-env", "cfo switch live-1 --restart"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
	log, err := state.TailStatus(stateDir, "live-1", 10)
	if err != nil || !strings.Contains(strings.Join(log, "\n"), "credentials: the CFO granted the MCP servers literal-env") {
		t.Errorf("status log = %q, %v, want a record of the grant", log, err)
	}
	untouched, err := state.ReadTaskMeta(stateDir, "other-1")
	if err != nil || len(untouched.MCPServers) != 0 {
		t.Errorf("another task of the project names the MCP servers %v, %v, want none", untouched.MCPServers, err)
	}
	if len(sent) != 0 {
		t.Errorf("notices = %v, want none: a running harness loads no MCP server", sent)
	}
	for _, text := range []string{stdout, stderr, strings.Join(log, "\n")} {
		if strings.Contains(text, "stand-in-mcp-env-value") {
			t.Errorf("the grant's words hold a value of the server's entry: %q", text)
		}
	}
}

// A credential pasted where a task id goes passes as an id, since an id may
// hold letters, digits and underscores. The commands that name a task's
// services refuse it without repeating it, as they refuse a service name
// shaped like a value.
func TestAuthTaskCommandsNeverRepeatATaskIDShapedLikeACredentialValue(t *testing.T) {
	valueShaped := "sk_live_" + strings.Repeat("a1B2", 8)
	for _, args := range [][]string{
		{"grant", valueShaped, "stored"},
		{"grant", valueShaped, "--mcp", "literal-env"},
		{"revoke", valueShaped, "stored"},
		{"revoke", valueShaped, "--mcp", "literal-env"},
		{"refresh", valueShaped},
	} {
		t.Run(strings.TrimSpace(args[0]+" "+strings.Join(args[2:], " ")), func(t *testing.T) {
			// Arrange
			t.Setenv(harness.RoleVariable, "")
			t.Setenv(gateAgentVariable, "")
			useFileStore(t)
			stateDir := filepath.Join(t.TempDir(), "state")
			var sent []string
			runtime := refreshTestRuntime(t, stateDir, cmdPanes{live: map[string]bool{}}, &sent)

			// Act
			code, stdout, stderr := runCLIWithRuntime(t, runtime, args...)

			// Assert
			if code != 2 || !strings.Contains(stderr, "the task id looks like a credential value") {
				t.Errorf("exit = %d, stderr = %q, want it refused as shaped like a value", code, stderr)
			}
			if strings.Contains(stdout+stderr, valueShaped) {
				t.Error("the refusal repeats a task id shaped like a credential value")
			}
		})
	}
}
