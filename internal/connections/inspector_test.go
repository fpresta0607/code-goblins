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
	"github.com/fpresta0607/code-goblins/internal/crawler"
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
	inspector := NewInspector(root, filepath.Join(root, "data"), filepath.Join(root, "state"))
	store, err := auth.OpenFileStore(filepath.Join(root, "credentials"))
	if err != nil {
		t.Fatal(err)
	}
	inspector.OpenStore = func() (auth.Store, error) { return store, nil }
	inspector.Runtime = func(context.Context, string, state.TaskMeta) (RuntimeObservation, error) {
		return RuntimeObservation{Identity: RuntimeIdentity{ProcessID: 42, StartedAt: time.Unix(100, 0), Workspace: meta.Worktree}, Env: []string{"FIXTURE_SECRET=synthetic-value"}, Args: []string{"--strict-mcp-config", "--mcp-config", filepath.Join(meta.TaskTmp, "mcp.json")}}, nil
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

func storeConnectionCredential(t *testing.T, inspector *Inspector, meta state.TaskMeta, name string) {
	t.Helper()
	store, err := inspector.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(auth.Scoped(meta.Project, name), "synthetic-stored-value"); err != nil {
		t.Fatal(err)
	}
}

func TestSuccessfulSeparateCodexProbeDoesNotVerifyLiveSession(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	meta.Harness = "codex"
	executable := "codex"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	directory := t.TempDir()
	writeConnectionFixture(t, filepath.Join(directory, executable), "")
	if err := os.Chmod(filepath.Join(directory, executable), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	inspector.Runner = checkRunner(func(context.Context, execx.Request) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`[{"name":"tools","enabled":true}]`)}, nil
	})
	inspector.Codex = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{codexEntry(codexStatus{Name: "tools", RuntimeStatus: "connected"})}, nil
	}

	result := inspector.Check(context.Background(), meta)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var response Snapshot
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(response.Entries, func(entry Entry) bool { return entry.ID == "mcp:tools" })
	if index < 0 {
		t.Fatalf("missing fake probe result: %s", data)
	}
	entry := response.Entries[index]
	if response.Generation != meta.SpawnGen || entry.Status != "probe_succeeded" || entry.Scope != "separate_probe" || entry.LiveStatus != "unknown" || entry.AccountIdentity != "unknown" || entry.TargetIdentity != "unknown" {
		t.Fatalf("separate probe implied live health or identity: %s", data)
	}
}

func TestInspectorRejectsRuntimeIdentityChangesDuringProbe(t *testing.T) {
	for _, change := range []string{"process", "start", "workspace", "unreadable"} {
		t.Run(change, func(t *testing.T) {
			inspector, meta := inspectorFixture(t)
			readRuntime := inspector.Runtime
			calls := 0
			inspector.Runtime = func(ctx context.Context, directory string, task state.TaskMeta) (RuntimeObservation, error) {
				observed, err := readRuntime(ctx, directory, task)
				calls++
				if calls == 2 {
					switch change {
					case "process":
						observed.Identity.ProcessID++
					case "start":
						observed.Identity.StartedAt = observed.Identity.StartedAt.Add(time.Second)
					case "workspace":
						observed.Identity.Workspace = meta.Project
					case "unreadable":
						return RuntimeObservation{}, errors.New("private diagnostic")
					}
				}
				return observed, err
			}

			result := inspector.Check(context.Background(), meta)

			if result.Error == "" || len(result.Entries) != 0 || result.Runtime != nil {
				t.Fatalf("published stale runtime evidence: %+v", result)
			}
		})
	}
}

func TestInspectorKeepsSameProviderWorkersEvidenceSeparate(t *testing.T) {
	inspector, first := inspectorFixture(t)
	first.ID, first.SpawnGen, first.HasCredentials, first.Credentials = "first", "g1", true, []string{"repo"}
	second := first
	second.ID, second.SpawnGen, second.Worktree, second.Credentials = "second", "g2", filepath.Join(t.TempDir(), "work"), nil
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, first.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET"]}]}`)
	inspector.Runtime = func(_ context.Context, _ string, task state.TaskMeta) (RuntimeObservation, error) {
		observed := RuntimeObservation{Identity: RuntimeIdentity{ProcessID: 42, StartedAt: time.Unix(100, 0), Workspace: task.Worktree}, Args: []string{"--strict-mcp-config"}}
		if task.ID == "first" {
			observed.Env = []string{"FIXTURE_SECRET=synthetic-value"}
		} else {
			observed.Identity.ProcessID = 43
		}
		return observed, nil
	}
	inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}}, nil
	}
	tasks := map[string]state.TaskMeta{"first\ng1": first, "second\ng2": second}
	cache := NewCache(time.Minute, time.Minute, func(ctx context.Context, key string) Snapshot { return inspector.Check(ctx, tasks[key]) })
	defer cache.Close()
	for key := range tasks {
		cache.Get(key, false)
	}

	for key, task := range tasks {
		result := waitChecked(t, cache, key)
		if result.Generation != task.SpawnGen || result.Runtime == nil || result.Runtime.Workspace != task.Worktree {
			t.Fatalf("mixed worker identity: %+v", result)
		}
		index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
		if index < 0 {
			t.Fatal("service missing")
		}
		if result.Entries[index].Grant != map[string]string{"first": "revoke", "second": "grant"}[task.ID] {
			t.Fatalf("mixed grants: %+v", result)
		}
		credentialIndex := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "credential:FIXTURE_SECRET" })
		if credentialIndex < 0 || result.Entries[credentialIndex].Status != map[string]string{"first": "provided", "second": "withheld"}[task.ID] {
			t.Fatalf("mixed environments: %+v", result)
		}
	}
}

func TestFailedSeparateProbeCannotRetainPartialSuccess(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}}, errors.New("private diagnostic")
	}

	result := inspector.Check(context.Background(), meta)

	for _, entry := range result.Entries {
		if entry.ID == "mcp:tools" {
			if entry.Status != "unverified" || entry.LiveStatus != "unknown" {
				t.Fatalf("failed probe kept success: %+v", entry)
			}
			return
		}
	}
	t.Fatal("lost probe entry")
}

func TestInspectorExplainsCredentialGrantDifferencesFromObservedProcess(t *testing.T) {
	for _, test := range []struct {
		name                                      string
		isGranted, isLoaded, isStored, isRejected bool
		wantReason                                string
	}{
		{"granted absent", true, false, true, false, "reach this goblin at its next terminal"},
		{"granted absent unstored", true, false, false, false, ""},
		{"granted absent rejected by probe", true, false, true, true, ""},
		{"revoked present", false, true, true, false, "leave this goblin at its next terminal"},
		{"granted present", true, true, true, false, ""},
		{"revoked absent", false, false, true, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector, meta := inspectorFixture(t)
			meta.HasCredentials = true
			if test.isGranted {
				meta.Credentials = []string{"repo"}
			}
			if test.isStored {
				storeConnectionCredential(t, inspector, meta, "FIXTURE_SECRET")
			}
			writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET"],"probe":["probe"]}]}`)
			if test.isRejected {
				inspector.Runner = checkRunner(func(context.Context, execx.Request) (execx.Result, error) {
					return execx.Result{ExitCode: 1, Stderr: []byte("401 Unauthorized")}, nil
				})
			}
			readRuntime := inspector.Runtime
			inspector.Runtime = func(ctx context.Context, directory string, task state.TaskMeta) (RuntimeObservation, error) {
				observed, err := readRuntime(ctx, directory, task)
				if !test.isLoaded {
					observed.Env = nil
				}
				return observed, err
			}
			inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) { return nil, nil }

			result := inspector.Check(context.Background(), meta)

			index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
			if index < 0 {
				t.Fatal("service missing")
			}
			entry := result.Entries[index]
			if entry.RestartRequired != (test.wantReason != "") || !strings.Contains(entry.RestartReason, test.wantReason) || entry.Grant != map[bool]string{true: "revoke", false: "grant"}[test.isGranted] {
				t.Fatalf("grant confused with loaded state: %+v", entry)
			}
		})
	}
}

func TestRestartIsNotClaimedForCredentialsLaunchWithholds(t *testing.T) {
	for _, test := range []struct {
		name, manifest, stored string
		grants                 []string
	}{
		{"harness billing key", `{"project":"project","services":[{"name":"repo","method":"env","env":["OPENAI_API_KEY"]}]}`, "OPENAI_API_KEY", []string{"repo"}},
		{"name refused by a wrong-target sibling", `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET"]},{"name":"elsewhere","method":"env","env":["FIXTURE_SECRET"],"identity":{"var":"FIXTURE_SECRET","expect":"another-instance","note":"n"}}]}`, "FIXTURE_SECRET", []string{"repo", "elsewhere"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector, meta := inspectorFixture(t)
			meta.HasCredentials, meta.Credentials = true, test.grants
			storeConnectionCredential(t, inspector, meta, test.stored)
			writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), test.manifest)
			readRuntime := inspector.Runtime
			inspector.Runtime = func(ctx context.Context, directory string, task state.TaskMeta) (RuntimeObservation, error) {
				observed, err := readRuntime(ctx, directory, task)
				observed.Env = nil
				return observed, err
			}
			inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) { return nil, nil }

			result := inspector.Check(context.Background(), meta)

			index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
			if index < 0 || result.Entries[index].Status != "provided" {
				t.Fatalf("fixture service is not one launch would load: %+v", result)
			}
			if result.Entries[index].RestartRequired || result.Entries[index].RestartReason != "" {
				t.Fatalf("restart claimed for a credential launch withholds: %+v", result.Entries[index])
			}
		})
	}
}

func TestSharedAndPartiallyLoadedCredentialsHaveAccurateRestartExplanations(t *testing.T) {
	for _, test := range []struct {
		name        string
		grants      []string
		wantRestart bool
		wantReason  string
	}{
		{"shared revoked service", []string{"other"}, false, "another account this goblin has uses them too"},
		{"partially loaded service", []string{"repo"}, true, "Its missing variables reach this goblin at its next terminal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inspector, meta := inspectorFixture(t)
			meta.HasCredentials, meta.Credentials = true, test.grants
			storeConnectionCredential(t, inspector, meta, "FIXTURE_SECRET")
			storeConnectionCredential(t, inspector, meta, "MISSING_TOKEN")
			writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET","MISSING_TOKEN"]},{"name":"other","method":"env","env":["FIXTURE_SECRET"]}]}`)

			result := inspector.Check(context.Background(), meta)

			index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
			if index < 0 || result.Entries[index].RestartRequired != test.wantRestart || !strings.Contains(result.Entries[index].RestartReason, test.wantReason) {
				t.Fatalf("wrong restart claim: %+v", result)
			}
		})
	}
}

func TestInspectorChecksServicesAndLaunchCredentialsWithoutExposingValues(t *testing.T) {
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"cli","probe":["probe"]},{"name":"missing","method":"env","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"},"oauth":{"url":"https://example.invalid/mcp"}}}`)
	result := inspector.Check(context.Background(), meta)
	for id, status := range map[string]string{"service:repo": "probe_succeeded", "service:missing": "missing", "mcp:tools": "probe_succeeded", "withheld:oauth": "withheld", "credential:FIXTURE_SECRET": "provided", "credential:FIXTURE_MISSING_TOKEN": "missing"} {
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
	inspector.Runtime = func(context.Context, string, state.TaskMeta) (RuntimeObservation, error) {
		return RuntimeObservation{Env: []string{"TEST_TOKEN=synthetic"}, Args: []string{"-c", "mcp_servers.tools.enabled=false"}}, nil
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
			if index < 0 || (result.Entries[index].Status == "probe_succeeded") != isSuccessful {
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
	inspector.Runtime = func(context.Context, string, state.TaskMeta) (RuntimeObservation, error) {
		return RuntimeObservation{}, errors.New("Goblin runtime unavailable.")
	}
	snapshot := inspector.Check(context.Background(), meta)
	if snapshot.Error == "" || len(snapshot.Entries) != 4 || !slices.ContainsFunc(snapshot.Entries, func(entry Entry) bool { return entry.ID == "connector:crawler" && entry.Status == "unchecked" }) {
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
	inspector.Runtime = func(context.Context, string, state.TaskMeta) (RuntimeObservation, error) {
		return RuntimeObservation{Args: []string{"-c", "mcp_servers.oauth.enabled=false"}}, nil
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

// A task carries only the services its record names. The panel used to call
// every other service's credential missing and offer to store it, though the
// value was stored and only withheld from this task.
func TestInspectorShowsAServiceTheTaskDoesNotCarryAsWithheld(t *testing.T) {
	// Arrange
	inspector, meta := inspectorFixture(t)
	meta.Credentials, meta.HasCredentials = []string{"repo"}, true
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET"]},{"name":"payments","method":"env","env":["FIXTURE_PAYMENTS_TOKEN"],"probe":["probe-payments"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	probed := false
	inspector.Runner = checkRunner(func(_ context.Context, request execx.Request) (execx.Result, error) {
		probed = probed || request.Name == "probe-payments"
		return execx.Result{}, nil
	})

	// Act
	result := inspector.Check(context.Background(), meta)

	// Assert
	for id, status := range map[string]string{"service:payments": "withheld", "credential:FIXTURE_PAYMENTS_TOKEN": "withheld", "credential:FIXTURE_SECRET": "provided"} {
		index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || result.Entries[index].Status != status {
			t.Errorf("%s did not have status %s: %+v", id, status, result.Entries)
			continue
		}
		if status == "withheld" && (len(result.Entries[index].Actions) != 0 || !strings.Contains(result.Entries[index].Detail, "goblins auth grant sample payments")) {
			t.Errorf("%s = %+v, want no repair offered and the grant that gives it named", id, result.Entries[index])
		}
	}
	if probed {
		t.Error("a service the task does not carry was probed for its panel")
	}
}

// A task an older build started has a record that names no services, and its
// terminal holds everything, so its panel reads as it always did.
func TestInspectorReadsATaskAnOlderBuildStartedAsBefore(t *testing.T) {
	// Arrange
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"payments","method":"env","env":["FIXTURE_PAYMENTS_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)

	// Act
	result := inspector.Check(context.Background(), meta)

	// Assert
	index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "credential:FIXTURE_PAYMENTS_TOKEN" })
	if index < 0 || result.Entries[index].Status != "missing" {
		t.Errorf("the credential did not read missing: %+v", result.Entries)
	}
}

// The board offers Connect for a service or a project MCP server the task
// does not carry and Disconnect for one its record names, the two that goblins
// auth grant and goblins auth revoke change. A credential variable and an MCP
// server from outside the project's .mcp.json offer neither.
func TestInspectorNamesWhatEachConnectorOffers(t *testing.T) {
	// Arrange
	inspector, meta := inspectorFixture(t)
	meta.Credentials, meta.HasCredentials, meta.MCPServers = []string{"repo"}, true, []string{"tools"}
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET"]},{"name":"payments","method":"env","env":["FIXTURE_PAYMENTS_TOKEN"]}]}`)
	writeConnectionFixture(t, filepath.Join(meta.TaskTmp, "mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"}}}`)
	writeConnectionFixture(t, filepath.Join(meta.Project, ".mcp.json"), `{"mcpServers":{"tools":{"command":"private-command"},"oauth":{"url":"https://example.invalid/mcp"}}}`)
	inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) {
		return []Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}, {ID: "mcp:personal", Name: "personal", Kind: "mcp", Status: "connected"}}, nil
	}

	// Act
	result := inspector.Check(context.Background(), meta)

	// Assert
	for id, want := range map[string]string{"service:repo": "revoke", "service:payments": "grant", "mcp:tools": "revoke", "withheld:oauth": "grant", "mcp:personal": "", "credential:FIXTURE_SECRET": "", "credential:FIXTURE_PAYMENTS_TOKEN": ""} {
		index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || result.Entries[index].Grant != want {
			t.Errorf("%s offers %q, want %q: %+v", id, entryGrant(result.Entries, index), want, result.Entries)
		}
	}
}

// An account's variables each say whether the goblin's terminal holds them,
// by name, with the service that declares them: none of them reads
// unverified for a check only the account can have.
func TestAnAccountsVariablesNameTheServiceThatDeclaresThemWithoutAWarningEach(t *testing.T) {
	// Arrange
	inspector, meta := inspectorFixture(t)
	meta.Credentials, meta.HasCredentials = []string{"repo"}, true
	for _, name := range []string{"FIXTURE_SECRET", "FIXTURE_URL", "FIXTURE_REPO_TOKEN"} {
		storeConnectionCredential(t, inspector, meta, name)
	}
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_SECRET","FIXTURE_URL","FIXTURE_REPO_TOKEN"]}]}`)

	// Act
	result := inspector.Check(context.Background(), meta)

	// Assert
	for id, want := range map[string][2]string{"service:repo": {"provided", ""}, "credential:FIXTURE_SECRET": {"provided", "repo"}, "credential:FIXTURE_URL": {"missing", "repo"}, "credential:FIXTURE_REPO_TOKEN": {"missing", "repo"}} {
		index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == id })
		if index < 0 || result.Entries[index].Status != want[0] || result.Entries[index].Account != want[1] {
			t.Errorf("%s is not %s under %q: %+v", id, want[0], want[1], result.Entries)
		}
	}
	if slices.ContainsFunc(result.Entries, func(entry Entry) bool { return entry.Kind == "credential" && entry.Status == "unverified" }) {
		t.Fatalf("a variable carries a warning of its own: %+v", result.Entries)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "synthetic-value") || strings.Contains(string(data), "synthetic-stored-value") {
		t.Fatal("report exposed a credential value")
	}
}

// A store that cannot be opened checks nothing, so an account it holds reads
// not checked, with why, and offers nothing to store into it.
func TestAnAccountReadsNotCheckedWhenTheStoreCannotBeOpened(t *testing.T) {
	// Arrange
	inspector, meta := inspectorFixture(t)
	writeConnectionFixture(t, auth.ManifestPath(inspector.DataDir, meta.Project), `{"project":"project","services":[{"name":"repo","method":"env","env":["FIXTURE_MISSING_TOKEN"]}]}`)
	inspector.OpenStore = func() (auth.Store, error) { return nil, errors.New("private diagnostic") }

	// Act
	result := inspector.Check(context.Background(), meta)

	// Assert
	index := slices.IndexFunc(result.Entries, func(entry Entry) bool { return entry.ID == "service:repo" })
	if index < 0 || result.Entries[index].Status != "unchecked" || result.Entries[index].Detail != "Not checked: the credential store could not be opened." || len(result.Entries[index].Actions) != 0 {
		t.Fatalf("store failure = %+v", result.Entries)
	}
	if result.Error != "Credential store is unavailable." {
		t.Fatalf("error = %q", result.Error)
	}
}

func entryGrant(entries []Entry, index int) string {
	if index < 0 {
		return "no entry"
	}
	return entries[index].Grant
}

// Every goblin's Connections lists the crawler connector, whichever harness
// it runs: provided when its terminal's PATH finds goblin-crawl, disabled
// when its brief left it out, and missing when the home never set it up, or
// set it up after the goblin's terminal started, which a relaunch carries.
func TestInspectorListsTheCrawlerAsTheGoblinsTerminalFindsIt(t *testing.T) {
	setUpHome, bareHome := t.TempDir(), t.TempDir()
	bin := crawler.BinDir(setUpHome)
	writeConnectionFixture(t, filepath.Join(bin, crawler.Command+".cmd"), "@node \"%~dp0..\\goblin-crawl.mjs\" %*\r\n")
	userPath := filepath.Join(setUpHome, "user-tools")
	for _, kind := range []string{"claude", "codex", "pi"} {
		for _, test := range []struct {
			name       string
			homeRoot   string
			path       string
			isLeftOut  bool
			wantStatus string
			wantDetail string
		}{
			{name: "carried", homeRoot: setUpHome, path: bin + string(os.PathListSeparator) + userPath, wantStatus: "provided"},
			{name: "left out by its brief", homeRoot: setUpHome, path: userPath, isLeftOut: true, wantStatus: "disabled", wantDetail: "connectors: none"},
			{name: "started before the home set it up", homeRoot: setUpHome, path: userPath, wantStatus: "missing", wantDetail: "its terminal started before the crawler was set up. A resume, switch or respawn starts one that carries it."},
			{name: "never set up", homeRoot: bareHome, path: filepath.Join(t.TempDir(), "empty") + string(os.PathListSeparator) + userPath, wantStatus: "missing", wantDetail: "The CEO installs it with goblins crawler setup."},
		} {
			t.Run(kind+" "+test.name, func(t *testing.T) {
				// Arrange
				inspector, meta := inspectorFixture(t)
				inspector.HomeRoot = test.homeRoot
				meta.Harness, meta.IsCrawlerLeftOut = kind, test.isLeftOut
				inspector.Runtime = func(context.Context, string, state.TaskMeta) (RuntimeObservation, error) {
					return RuntimeObservation{Env: []string{"Path=" + test.path}}, nil
				}
				inspector.Claude = func(context.Context, string, []string, []string) ([]Entry, error) { return nil, nil }
				inspector.Codex = func(context.Context, string, []string, []string) ([]Entry, error) { return nil, nil }

				// Act
				snapshot := inspector.Check(context.Background(), meta)

				// Assert
				index := slices.IndexFunc(snapshot.Entries, func(entry Entry) bool { return entry.ID == "connector:crawler" })
				if index < 0 {
					t.Fatalf("the %s goblin's Connections list no crawler: %+v", kind, snapshot.Entries)
				}
				entry := snapshot.Entries[index]
				if entry.Name != "crawler" || entry.Kind != "connector" || entry.Status != test.wantStatus || !strings.Contains(entry.Detail, test.wantDetail) || entry.Grant != "" {
					t.Errorf("the crawler entry = %+v, want %s naming %q and no grant", entry, test.wantStatus, test.wantDetail)
				}
			})
		}
	}
}
