package connections

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type checkRunner func(context.Context, execx.Request) (execx.Result, error)

func (r checkRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	return r(ctx, request)
}

func inspectorFixture(t *testing.T) (*Inspector, state.TaskMeta) {
	t.Helper()
	root := t.TempDir()
	meta := state.TaskMeta{ID: "sample", SpawnGen: "first", Project: filepath.Join(root, "project"), Worktree: filepath.Join(root, "work"), TaskTmp: filepath.Join(root, "task"), Harness: "claude", Backend: "native"}
	for _, directory := range []string{meta.Project, meta.Worktree, meta.TaskTmp} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	inspector := NewInspector(filepath.Join(root, "data"), filepath.Join(root, "state"))
	store, err := auth.OpenFileStore(filepath.Join(root, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	inspector.OpenStore = func() (auth.Store, error) { return store, nil }
	inspector.Runtime = func(context.Context, string, state.TaskMeta) ([]string, []string, error) {
		return []string{"FIXTURE_SECRET=synthetic-value"}, []string{"--strict-mcp-config", "--mcp-config", filepath.Join(meta.TaskTmp, "mcp.json")}, nil
	}
	inspector.Runner = checkRunner(func(_ context.Context, request execx.Request) (execx.Result, error) {
		if request.Dir != meta.Worktree || !request.KillTree {
			t.Fatal("check escaped its worktree or time limit")
		}
		return execx.Result{}, nil
	})
	inspector.Claude = func(_ context.Context, dir string, env, args []string) ([]Entry, error) {
		if dir != meta.Worktree || !slices.Contains(args, "--strict-mcp-config") || !slices.Contains(env, "FIXTURE_SECRET=synthetic-value") {
			t.Fatal("lost launch configuration")
		}
		return []Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}}, nil
	}
	return inspector, meta
}

func writeConnectionFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorChecksServicesAndLaunchCredentialsWithoutExposingValues(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"cli","probe":["probe"]},{"name":"missing","method":"env","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"},"oauth":{"url":"https://example.invalid/mcp"}}}`)
	result := inspector.Check(context.Background(), meta)
	for id, status := range map[string]string{"service:repo": "connected", "service:missing": "missing", "mcp:tools": "connected", "withheld:oauth": "withheld", "credential:FIXTURE_SECRET": "provided", "credential:FIXTURE_MISSING_TOKEN": "missing"} {
		index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || result.Entries[index].Status != status {
			t.Fatalf("%s did not have status %s: %+v", id, status, result)
		}
	}
	data, _ := json.Marshal(result)
	for _, secret := range []string{"synthetic-value", "private-command", "https://"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("report exposed %s", secret)
		}
	}
}

func TestInspectorRetainsLoadedMCPWhenItsHealthCheckFails(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) {
		return nil, errors.New("private diagnostic")
	}
	result := inspector.Check(context.Background(), meta)
	index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "mcp:tools" })
	if result.Error == "" || index < 0 || result.Entries[index].Status != "unverified" {
		t.Fatalf("lost configured server: %+v", result)
	}
	if slices.ContainsFunc(result.Entries, func(entry Entry) bool { return entry.Status == "withheld" }) {
		t.Fatal("failed check invented a withholding decision")
	}
}

func TestCodexDisabledInventoryDoesNotStartMCPServers(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	meta.Harness = "codex"
	executable := "codex"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, executable), nil, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	inspector.Runtime = func(context.Context, string, state.TaskMeta) ([]string, []string, error) {
		return []string{"TEST_TOKEN=synthetic"}, []string{"-c", "mcp_servers.tools.enabled=false"}, nil
	}
	inspector.Runner = checkRunner(func(_ context.Context, request execx.Request) (execx.Result, error) {
		if !slices.Contains(request.Args, "mcp_servers.tools.enabled=false") {
			t.Fatal("lost launch restriction")
		}
		return execx.Result{Stdout: []byte(`[{"name":"tools","enabled":false,"transport":{"url":"private"},"auth_status":"OAuth"}]`)}, nil
	})
	inspector.Codex = func(context.Context, string, []string, []string) ([]Entry, error) {
		t.Fatal("started a disabled MCP")
		return nil, nil
	}
	result := inspector.Check(context.Background(), meta)
	index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "mcp:tools" })
	if index < 0 || result.Entries[index].Status != "disabled" || len(result.Entries[index].Actions) != 0 {
		t.Fatalf("wrong disabled state: %+v", result)
	}
}

func TestRepairsAreScopedAndNeverAcceptBrowserCommandsOrUnknownCredentials(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"cli","probe":["probe"],"login":["login"],"url":"https://example.invalid/login","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	snapshot := Snapshot{Entries: []Entry{{ID: "service:repo", Actions: []string{"login", "cli", "store:FIXTURE_MISSING_TOKEN"}}}}
	for _, test := range []struct {
		action string
		want   Repair
	}{
		{"login", Repair{URL: "https://example.invalid/login"}}, {"cli", Repair{Service: "repo"}}, {"store:FIXTURE_MISSING_TOKEN", Repair{Credential: "FIXTURE_MISSING_TOKEN"}},
	} {
		t.Run(test.action, func(t *testing.T) {
			got, err := inspector.RepairPlan(meta, snapshot, "service:repo", test.action)
			if err != nil || got != test.want {
				t.Fatalf("repair=%+v error=%v", got, err)
			}
		})
	}
	for _, action := range []string{"store:OTHER_TOKEN", "cli; evil", "store:OPENAI_API_KEY"} {
		if _, err := inspector.RepairPlan(meta, snapshot, "service:repo", action); err == nil {
			t.Fatal("accepted unoffered action")
		}
	}
	if _, err := inspector.RepairPlan(meta, snapshot, "service:other", "login"); err == nil {
		t.Fatal("accepted another service")
	}
	for _, url := range []string{"http://example.invalid", "https://user:secret@example.invalid", "https://example.invalid/?token=secret", "javascript:alert(1)"} {
		if safeLoginURL(url) {
			t.Fatal("accepted unsafe login URL")
		}
	}
}

func TestCacheTimeoutIsVisibleEvenIfCheckerIgnoresCancellation(t *testing.T) {
	release := make(chan struct{})
	cache := NewCache(time.Minute, 10*time.Millisecond, func(context.Context, string) Snapshot { <-release; return Snapshot{} })
	defer close(release)
	defer cache.Close()
	cache.Get("hung", false)
	result := waitChecked(t, cache, "hung")
	if result.Error != "Connection check timed out." {
		t.Fatalf("hung check result=%+v", result)
	}
}

func TestCLIRepairRunsOnlyChosenLoginAndRechecksItsResult(t *testing.T) {
	for _, isSuccessful := range []bool{true, false} {
		t.Run(map[bool]string{true: "succeeds", false: "fails"}[isSuccessful], func(t *testing.T) {
			inspector, meta := inspectorFixture(t)
			writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"cli","probe":["probe"],"login":["login"],"optional":true}]}`)
			isLoggedIn := false
			logins := 0
			inspector.Runner = checkRunner(func(_ context.Context, request execx.Request) (execx.Result, error) {
				if request.Dir != meta.Worktree || !request.KillTree {
					t.Fatal("login escaped its worktree")
				}
				if request.Name == "login" {
					logins++
					isLoggedIn = isSuccessful
					return execx.Result{Stdout: []byte("synthetic-secret")}, nil
				}
				if request.Name != "probe" {
					t.Fatal("unexpected command", request.Name)
				}
				if !isLoggedIn {
					return execx.Result{ExitCode: 1, Stderr: []byte("unauthorized synthetic-secret")}, nil
				}
				return execx.Result{}, nil
			})
			err := inspector.RepairConnection(context.Background(), meta, "service:repo", "cli")
			if logins != 1 || (err == nil) != isSuccessful {
				t.Fatalf("logins=%d error=%v", logins, err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("login diagnostic leaked")
			}
			result := inspector.Check(context.Background(), meta)
			index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
			if index < 0 || (result.Entries[index].Status == "connected") != isSuccessful {
				t.Fatalf("recheck=%+v", result)
			}
		})
	}
}

func TestLoadedAndWithheldTokenRepairsRequireAnExportedProjectCredential(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"url":"https://example.invalid","headers":{"Authorization":"Bearer ${FIXTURE_MISSING_TOKEN}"}}}}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"withheld":{"url":"https://example.invalid","bearerTokenEnvVar":"FIXTURE_MISSING_TOKEN"},"undeclared":{"url":"https://example.invalid","bearerTokenEnvVar":"UNDECLARED_FIXTURE_TOKEN"}}}`)
	inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "unauthorized"}}, nil
	}
	snapshot := inspector.Check(context.Background(), meta)
	for _, connection := range []string{"mcp:tools", "withheld:withheld", "credential:FIXTURE_MISSING_TOKEN"} {
		plan, err := inspector.RepairPlan(meta, snapshot, connection, "store:FIXTURE_MISSING_TOKEN")
		if err != nil || plan.Credential != "FIXTURE_MISSING_TOKEN" {
			t.Fatalf("%s repair=%+v error=%v", connection, plan, err)
		}
	}
	if _, err := inspector.RepairPlan(meta, snapshot, "withheld:undeclared", "store:UNDECLARED_FIXTURE_TOKEN"); err == nil {
		t.Fatal("offered a token the project never exports")
	}
}

func TestRuntimeFailureCannotReuseConnectedOrProvidedEvidence(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"tools":{"command":"fixture"}}}`)
	inspector.Runtime = func(context.Context, string, state.TaskMeta) ([]string, []string, error) {
		return nil, nil, errors.New("Goblin runtime unavailable.")
	}
	snapshot := inspector.Check(context.Background(), meta)
	if snapshot.Error == "" || len(snapshot.Entries) != 3 {
		t.Fatalf("missing unavailable evidence: %+v", snapshot)
	}
	for _, entry := range snapshot.Entries {
		if entry.Status == "connected" || entry.Status == "provided" {
			t.Fatal("invented runtime evidence")
		}
	}
}

func TestCLISignInIsOfferedOnlyWhenDeclaredCredentialsResolve(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"ready","method":"cli","probe":["probe"],"login":["login"]},{"name":"blocked","method":"cli","probe":["probe"],"login":["login"],"env":["FIXTURE_MISSING_TOKEN"]}]}`)
	inspector.Runner = checkRunner(func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{ExitCode: 1, Stderr: []byte("unauthorized")}, nil
	})
	snapshot := inspector.Check(context.Background(), meta)
	for id, want := range map[string][]string{"service:ready": {"cli"}, "service:blocked": {"store:FIXTURE_MISSING_TOKEN"}} {
		index := slices.IndexFunc(snapshot.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || !slices.Equal(snapshot.Entries[index].Actions, want) {
			t.Fatalf("%s actions differ from %v: %+v", id, want, snapshot)
		}
	}
	if _, err := inspector.RepairPlan(meta, snapshot, "service:blocked", "cli"); err == nil {
		t.Fatal("offered a CLI sign-in that cannot run while a credential is missing")
	}
}

func TestLaunchDisabledCodexServerNeverOffersSignIn(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	meta.Harness = "codex"
	executable := "codex"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, executable), nil, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	inspector.Runtime = func(context.Context, string, state.TaskMeta) ([]string, []string, error) {
		return nil, []string{"-c", "mcp_servers.oauth.enabled=false"}, nil
	}
	inspector.Runner = checkRunner(func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`[{"name":"tools","enabled":true},{"name":"oauth","enabled":false}]`)}, nil
	})
	inspector.Codex = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{
			codexEntry(codexStatus{Name: "tools", RuntimeStatus: "authenticationRequired", AuthStatus: "notLoggedIn"}),
			codexEntry(codexStatus{Name: "oauth", RuntimeStatus: "disabled", AuthStatus: "notLoggedIn"}),
		}, nil
	}
	snapshot := inspector.Check(context.Background(), meta)
	for id, want := range map[string][]string{"mcp:tools": {"login"}, "mcp:oauth": nil} {
		index := slices.IndexFunc(snapshot.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || !slices.Equal(snapshot.Entries[index].Actions, want) {
			t.Fatalf("%s actions differ from %v: %+v", id, want, snapshot)
		}
	}
	if _, err := inspector.RepairPlan(meta, snapshot, "mcp:oauth", "login"); err == nil {
		t.Fatal("offered sign-in for a launch-disabled server")
	}
}
