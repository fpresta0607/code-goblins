package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestConnectionAPIRejectsTaskChangesDuringAsynchronousInspection(t *testing.T) {
	for _, change := range []string{"none", "generation", "grants", "workspace"} {
		t.Run(change, func(t *testing.T) {
			store, home := testStore(t)
			meta, err := state.ReadTaskMeta(home.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Project = gitFixture(t)
			meta.Worktree = filepath.Join(home.Root, "linked")
			command := execx.Command("git", "-C", meta.Project, "worktree", "add", "--detach", meta.Worktree, "HEAD")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("linked fixture: %s %v", output, err)
			}
			if err := state.WriteTaskMeta(home.State, meta); err != nil {
				t.Fatal(err)
			}
			inspector := connections.NewInspector(home.Root, home.Data, home.State)
			inspector.Runtime = func(context.Context, string, state.TaskMeta) (connections.RuntimeObservation, error) {
				return connections.RuntimeObservation{Identity: connections.RuntimeIdentity{ProcessID: 42, StartedAt: time.Unix(100, 0), Workspace: meta.Worktree}, Args: []string{"--strict-mcp-config"}}, nil
			}
			inspector.Claude = func(context.Context, string, []string, []string) ([]connections.Entry, error) {
				current := meta
				switch change {
				case "generation":
					current.SpawnGen = "replacement"
				case "grants":
					current.HasCredentials, current.Credentials = true, []string{"repo"}
				case "workspace":
					current.Worktree = meta.Project
				}
				if err := state.WriteTaskMeta(home.State, current); err != nil {
					return nil, err
				}
				return []connections.Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}}, nil
			}
			meta.Harness = "claude"
			if err := state.WriteTaskMeta(home.State, meta); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store, Instance: "test-instance", connectionInspector: inspector}
			checks, _ := service.connections()
			defer checks.Close()

			checks.Get(meta.ID+"\n"+meta.SpawnGen, false)
			result := waitForConnectionCheck(t, checks, meta.ID+"\n"+meta.SpawnGen)

			if change != "none" {
				if result.Error == "" || len(result.Entries) != 0 || result.Runtime != nil {
					t.Fatalf("published changed task: %+v", result)
				}
				return
			}
			handler := NewHTTP(service, "board.local", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/connections?task=task-1&generation=g1", nil))
			var snapshot connections.Snapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || snapshot.Generation != "g1" || snapshot.Runtime == nil || snapshot.Runtime.Workspace != meta.Worktree {
				t.Fatalf("lost runtime provenance: %s", response.Body)
			}
			for _, entry := range snapshot.Entries {
				if entry.ID == "mcp:tools" && entry.Status == "probe_succeeded" && entry.Scope == "separate_probe" && entry.LiveStatus == "unknown" {
					return
				}
			}
			t.Fatalf("API omitted separate probe scope: %s", response.Body)
		})
	}
}

func TestConnectionAPIIsCachedAndRejectsStaleTasksAndUntrustedRefreshes(t *testing.T) {
	store, _ := testStore(t)
	release := make(chan struct{})
	checks := connections.NewCache(time.Minute, time.Second, func(ctx context.Context, _ string) connections.Snapshot {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return connections.Snapshot{Entries: []connections.Entry{{ID: "mcp:tools", Status: "connected"}}}
	})
	defer checks.Close()
	service := &Service{Store: store, Instance: "test-instance", connectionChecks: checks}
	handler := NewHTTP(service, "board.local", nil)
	for _, test := range []struct {
		method, path, body, origin, token string
		code                              int
	}{
		{"GET", "/api/connections?task=task-1&generation=g1", "", "", "", 200},
		{"GET", "/api/connections?task=task-1&generation=old", "", "", "", 409},
		{"POST", "/api/connections/check", `{"task":"task-1","generation":"g1"}`, "http://elsewhere", "test-instance", 403},
		{"POST", "/api/connections/check", `{"task":"task-1","generation":"g1"}`, "http://board.local", "wrong", 403},
		{"POST", "/api/connections/check", `{"task":"task-1","generation":"g1"}`, "http://board.local", "test-instance", 200},
		{"POST", "/api/connections/check", `{"task":"task-1","generation":"old"}`, "http://board.local", "test-instance", 409},
	} {
		request := httptest.NewRequest(test.method, "http://board.local"+test.path, strings.NewReader(test.body))
		request.Header.Set("Origin", test.origin)
		request.Header.Set("X-CFO-Token", test.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("%s %s: %d %s", test.method, test.path, response.Code, response.Body)
		}
		if response.Code == 200 && !strings.Contains(response.Body.String(), `"checking":true`) {
			t.Fatal("request blocked on the checker")
		}
	}
	close(release)
}

func TestConnectionFixDerivesOAuthURLAndRejectsInjectedOrStaleRepairs(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	manifestPath := auth.ManifestPath(h.Data, meta.Project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"project":"work","services":[{"name":"sample","method":"oauth","url":"https://example.invalid/login"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	checks := connections.NewCache(time.Minute, time.Minute, func(context.Context, string) connections.Snapshot {
		return connections.Snapshot{Entries: []connections.Entry{{ID: "service:sample", Status: "unauthorized", Actions: []string{"login"}}}}
	})
	defer checks.Close()
	checks.Get("task-1\ng1", false)
	deadline := time.Now().Add(time.Minute)
	for checks.Get("task-1\ng1", false).Checking {
		if time.Now().After(deadline) {
			t.Fatal("cache did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	handler := NewHTTP(&Service{Store: store, Instance: "test-instance", connectionChecks: checks}, "board.local", nil)
	for _, test := range []struct {
		body string
		code int
	}{
		{`{"task":"task-1","generation":"g1","connection":"service:sample","action":"login"}`, 200},
		{`{"task":"task-1","generation":"old","connection":"service:sample","action":"login"}`, 409},
		{`{"task":"task-1","generation":"g1","connection":"service:other","action":"login"}`, 409},
		{`{"task":"task-1","generation":"g1","connection":"service:sample","action":"cli","command":"evil"}`, 400},
	} {
		request := httptest.NewRequest("POST", "http://board.local/api/connections/fix", strings.NewReader(test.body))
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("X-CFO-Token", "test-instance")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("fix returned %d: %s", response.Code, response.Body)
		}
		if test.code == 200 {
			var result map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["url"] != "https://example.invalid/login" {
				t.Fatal("wrong sign-in destination", result, err)
			}
		}
	}
}

func TestConnectionClipboardCardNeverReadsClipboardAndCannotRunForReplacedTask(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	service := &Service{Store: store, Instance: "test-instance"}
	run, err := service.connectionRun(meta, connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:TEST_TOKEN", Action: "store:TEST_TOKEN"}, connections.Repair{Credential: "TEST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(run.Command, "Get-Clipboard -Raw |") || strings.Contains(run.Command, "Set-Clipboard") || run.ConnectionTask != meta.ID || run.ConnectionGeneration != meta.SpawnGen {
		t.Fatal("unsafe clipboard card")
	}
	meta.SpawnGen = "replacement"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	for index := range store.db.Runs {
		store.db.Runs[index].RunAction = "action"
		store.db.Runs[index].State = "running"
	}
	err = store.save()
	store.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.startRun(context.Background(), Action{ID: "action", RunID: run.ID, Generation: run.Identity})
	if err == nil {
		t.Fatal("ran a stale repair")
	}
	if service.connectionChecks != nil {
		service.connectionChecks.Close()
	}
	if stored := store.Snapshot().Runs[0]; stored.State != "failed" {
		t.Fatalf("stale run stayed pending: %s", stored.State)
	}
}

func TestCompletedConnectionRepairRechecksItsTaskAndPublishesTheOwner(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	var calls atomic.Int32
	checks := connections.NewCache(time.Minute, time.Minute, func(context.Context, string) connections.Snapshot {
		calls.Add(1)
		return connections.Snapshot{Entries: []connections.Entry{{ID: "credential:TEST_TOKEN", Status: "provided"}}}
	})
	defer checks.Close()
	key := meta.ID + "\n" + meta.SpawnGen
	checks.Get(key, false)
	deadline := time.Now().Add(time.Minute)
	for checks.Get(key, false).Checking {
		if time.Now().After(deadline) {
			t.Fatal("cache did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	service := &Service{Store: store, Instance: "test-instance", connectionChecks: checks}
	run, err := service.connectionRun(meta, connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:TEST_TOKEN", Action: "store:TEST_TOKEN"}, connections.Repair{Credential: "TEST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range store.db.Runs {
		store.db.Runs[index].RunAction = "action"
		store.db.Runs[index].State = "running"
	}
	run.RunAction = "action"
	code := 0
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatalf("completion error = %v", err)
	}
	for checks.Get(key, false).Checking {
		if time.Now().After(deadline.Add(time.Second)) {
			t.Fatal("recheck did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatalf("checks = %d, want a recheck after the repair", calls.Load())
	}
	data, err := json.Marshal(store.Snapshot().Runs[0])
	if err != nil || !strings.Contains(string(data), `"connection_task":"task-1"`) || !strings.Contains(string(data), `"finished_at"`) {
		t.Fatalf("published run = %s, %v", data, err)
	}
}

func waitForConnectionCheck(t *testing.T, checks *connections.Cache, key string) connections.Snapshot {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		snapshot := checks.Cached(key)
		if !snapshot.Checking && !snapshot.CheckedAt.IsZero() {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatal("connection check did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIdleRepairClickUsesTheShownCheckWithoutStartingAnother(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	manifestPath := auth.ManifestPath(h.Data, meta.Project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"project":"work","services":[{"name":"sample","method":"oauth","url":"https://example.invalid/login"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	release := make(chan struct{})
	checks := connections.NewCache(10*time.Millisecond, time.Minute, func(context.Context, string) connections.Snapshot {
		if calls.Add(1) > 1 {
			<-release
		}
		return connections.Snapshot{Entries: []connections.Entry{{ID: "service:sample", Status: "unauthorized", Actions: []string{"login"}}}}
	})
	defer checks.Close()
	defer close(release)
	key := "task-1\ng1"
	checks.Get(key, false)
	waitForConnectionCheck(t, checks, key)
	time.Sleep(20 * time.Millisecond)
	handler := NewHTTP(&Service{Store: store, Instance: "test-instance", connectionChecks: checks}, "board.local", nil)
	fix := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://board.local/api/connections/fix", strings.NewReader(`{"task":"task-1","generation":"g1","connection":"service:sample","action":"login"}`))
		request.Header.Set("Origin", "http://board.local")
		request.Header.Set("X-CFO-Token", "test-instance")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := fix(); response.Code != 200 || !strings.Contains(response.Body.String(), "https://example.invalid/login") {
		t.Fatalf("idle click returned %d: %s", response.Code, response.Body)
	}
	if calls.Load() != 1 || checks.Cached(key).Checking {
		t.Fatalf("idle click started a check: %d", calls.Load())
	}
	checks.Get(key, true)
	if response := fix(); response.Code != 409 {
		t.Fatalf("click during a running check returned %d: %s", response.Code, response.Body)
	}
}

func TestRepairFinishedDuringARunningCheckRechecksAfterIt(t *testing.T) {
	store, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	var calls atomic.Int32
	release := make(chan struct{})
	checks := connections.NewCache(time.Minute, time.Minute, func(context.Context, string) connections.Snapshot {
		status := "provided"
		if calls.Add(1) == 1 {
			<-release
			status = "missing"
		}
		return connections.Snapshot{Entries: []connections.Entry{{ID: "credential:TEST_TOKEN", Status: status}}}
	})
	defer checks.Close()
	key := meta.ID + "\n" + meta.SpawnGen
	checks.Get(key, false)
	service := &Service{Store: store, Instance: "test-instance", connectionChecks: checks}
	run, err := service.connectionRun(meta, connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:TEST_TOKEN", Action: "store:TEST_TOKEN"}, connections.Repair{Credential: "TEST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range store.db.Runs {
		store.db.Runs[index].RunAction = "action"
		store.db.Runs[index].State = "running"
	}
	run.RunAction = "action"
	code := 0
	if err := service.completeRun(context.Background(), run, &code, ""); err != nil {
		t.Fatalf("completion error = %v", err)
	}
	close(release)
	snapshot := waitForConnectionCheck(t, checks, key)
	if calls.Load() != 2 || len(snapshot.Entries) != 1 || snapshot.Entries[0].Status != "provided" {
		t.Fatalf("checks=%d snapshot=%+v, want the post-repair result", calls.Load(), snapshot)
	}
}
