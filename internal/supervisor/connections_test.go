package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
				if change == "generation" {
					return
				}
			}
			handler := NewHTTP(service, "board.local", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/connections?task=task-1&generation=g1", nil))
			var snapshot connections.Snapshot
			if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if change != "none" {
				if response.Code != 200 || snapshot.Generation != "g1" || snapshot.Error == "" || len(snapshot.Entries) != 0 {
					t.Fatalf("error response lost its generation: %s", response.Body)
				}
				return
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

// #2394: the Connectors page reads every running goblin each time it opens,
// and each goblin's check starts a harness process. A result stood a minute,
// so every open after one started a harness for each goblin. A result now
// stands ten minutes: an open within them starts none, one after them shows
// each goblin's last result at once while its recheck runs behind it, and a
// grant made with goblins auth, which changes the task's record, rechecks
// that goblin at the next open.
func TestConnectorsPageOpenStartsHarnessesOnlyForOldResultsAndChangedRecords(t *testing.T) {
	// Arrange: three running goblins of one repository.
	store, home := testStore(t)
	project := gitFixture(t)
	var tasks []state.TaskMeta
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		meta := state.TaskMeta{ID: id, Project: project, Worktree: filepath.Join(home.Root, id), Harness: "claude", Backend: "native", SpawnGen: "g1"}
		if output, err := execx.Command("git", "-C", project, "worktree", "add", "--detach", meta.Worktree, "HEAD").CombinedOutput(); err != nil {
			t.Fatalf("linked fixture: %s %v", output, err)
		}
		if err := state.WriteTaskMeta(home.State, meta); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, meta)
	}
	inspector := connections.NewInspector(home.Root, home.Data, home.State)
	inspector.Runtime = func(_ context.Context, _ string, meta state.TaskMeta) (connections.RuntimeObservation, error) {
		return connections.RuntimeObservation{Identity: connections.RuntimeIdentity{ProcessID: 42, StartedAt: time.Unix(100, 0), Workspace: meta.Worktree}, Args: []string{"--strict-mcp-config"}}, nil
	}
	var harnesses atomic.Int32
	inspector.Claude = func(context.Context, string, []string, []string) ([]connections.Entry, error) {
		harnesses.Add(1)
		return []connections.Entry{{ID: "mcp:tools", Name: "tools", Kind: "mcp", Status: "connected"}}, nil
	}
	service := &Service{Store: store, Instance: "test-instance", connectionInspector: inspector}
	checks, _ := service.connections()
	defer checks.Close()
	var ahead atomic.Int64
	checks.Now = func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) } // stall-accepted: the offset the test moves the cache's clock by, not a wait.
	handler := NewHTTP(service, "board.local", nil)
	read := func(meta state.TaskMeta) connections.Snapshot {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/connections?task="+meta.ID+"&generation="+meta.SpawnGen, nil))
		var snapshot connections.Snapshot
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
			t.Fatalf("read %s: %d %s", meta.ID, response.Code, response.Body)
		}
		return snapshot
	}
	// open reads every goblin as the page does, each until its check has
	// ended, and gives what each one's first read showed.
	open := func() []connections.Snapshot {
		t.Helper()
		var shown []connections.Snapshot
		for _, meta := range tasks {
			first := read(meta)
			shown = append(shown, first)
			deadline := time.Now().Add(time.Minute)
			for latest := first; latest.Checking; latest = read(meta) {
				if time.Now().After(deadline) {
					t.Fatalf("the check of %s did not end", meta.ID)
				}
				time.Sleep(time.Millisecond)
			}
		}
		return shown
	}
	open()
	if harnesses.Load() != 3 {
		t.Fatalf("the first open started %d harnesses, want one for each of 3 goblins", harnesses.Load())
	}

	// Act: the page opens again five minutes later.
	ahead.Store(int64(5 * time.Minute))
	shown := open()

	// Assert: no harness, and each goblin's result at once.
	if harnesses.Load() != 3 {
		t.Fatalf("an open five minutes later started %d harnesses, want none", harnesses.Load()-3)
	}
	for at, snapshot := range shown {
		if snapshot.Checking || len(snapshot.Entries) == 0 {
			t.Fatalf("%s showed %+v, want its last result and no check", tasks[at].ID, snapshot)
		}
	}

	// Act: the page opens eleven minutes after the checks.
	ahead.Store(int64(11 * time.Minute))
	shown = open()

	// Assert: each goblin's last result shows at once while one recheck
	// each runs behind it.
	if harnesses.Load() != 6 {
		t.Fatalf("an open after ten minutes started %d harnesses, want one for each of 3 goblins", harnesses.Load()-3)
	}
	for at, snapshot := range shown {
		if !snapshot.Checking || !slices.ContainsFunc(snapshot.Entries, func(entry connections.Entry) bool { return entry.ID == "mcp:tools" }) {
			t.Fatalf("%s showed %+v, want its last result while its recheck runs", tasks[at].ID, snapshot)
		}
	}

	// Act: a grant made with goblins auth gives the second goblin a server,
	// and the page opens again at once.
	tasks[1].MCPServers = []string{"tools"}
	if err := state.WriteTaskMeta(home.State, tasks[1]); err != nil {
		t.Fatal(err)
	}
	shown = open()

	// Assert: only the goblin whose record changed is checked again, and its
	// last result shows while it is.
	if harnesses.Load() != 7 {
		t.Fatalf("an open after a grant started %d harnesses, want one for the granted goblin", harnesses.Load()-6)
	}
	if !shown[1].Checking || len(shown[1].Entries) == 0 {
		t.Fatalf("the granted goblin showed %+v, want its last result while its recheck runs", shown[1])
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
		request.Header.Set("X-CEO-Token", test.token)
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
		request.Header.Set("X-CEO-Token", "test-instance")
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

// A clipboard card says whether it stores a missing variable or replaces a
// set one, and runs the same store either way (#2389).
func TestAClipboardCardSaysWhetherItStoresOrReplacesTheVariable(t *testing.T) {
	for _, test := range []struct{ action, want string }{
		{"store:TEST_TOKEN", "Store TEST_TOKEN from clipboard for "},
		{"replace:TEST_TOKEN", "Replace TEST_TOKEN from clipboard for "},
	} {
		t.Run(test.action, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, _ := state.ReadTaskMeta(h.State, "task-1")
			service := &Service{Store: store, Instance: "test-instance"}

			// Act
			run, err := service.connectionRun(meta, connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:TEST_TOKEN", Action: test.action}, connections.Repair{Credential: "TEST_TOKEN"})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if run.Title != test.want+filepath.Base(meta.Project) {
				t.Fatalf("title = %q, want %q", run.Title, test.want+filepath.Base(meta.Project))
			}
			if !strings.Contains(run.Command, "Get-Clipboard -Raw | & ") || !strings.Contains(run.Command, " auth store --project ") || !strings.HasSuffix(run.Command, " 'TEST_TOKEN'\nexit $LASTEXITCODE\n") {
				t.Fatalf("not the clipboard store: %q", run.Command)
			}
		})
	}
}

// The board's Replace and Store from clipboard reach their card through the
// fix endpoint the board posts to, on the check the page shows: a variable the
// goblin's terminal holds and its repository declares gets a card titled
// Replace, a missing one gets Store's, each piping the clipboard into goblins
// auth store for that repository, and a replace the check did not offer, for
// a variable the terminal does not hold or no manifest declares, is refused
// and records no card (#2389).
func TestConnectionFixMakesTheClipboardCardItsCheckOffers(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	meta, err := state.ReadTaskMeta(home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Project = gitFixture(t)
	meta.Worktree = filepath.Join(home.Root, "linked")
	if output, err := execx.Command("git", "-C", meta.Project, "worktree", "add", "--detach", meta.Worktree, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("linked fixture: %s %v", output, err)
	}
	meta.Harness = "claude"
	meta.Credentials, meta.HasCredentials = []string{"repo"}, true
	if err := state.WriteTaskMeta(home.State, meta); err != nil {
		t.Fatal(err)
	}
	manifestPath := auth.ManifestPath(home.Data, meta.Project)
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(`{"project":"work","services":[{"name":"repo","method":"env","env":["TEST_TOKEN","UNSET_TOKEN"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	credentials, err := auth.OpenFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inspector := connections.NewInspector(home.Root, home.Data, home.State)
	inspector.OpenStore = func() (auth.Store, error) { return credentials, nil }
	inspector.Runtime = func(context.Context, string, state.TaskMeta) (connections.RuntimeObservation, error) {
		return connections.RuntimeObservation{Identity: connections.RuntimeIdentity{ProcessID: 42, StartedAt: time.Unix(100, 0), Workspace: meta.Worktree}, Env: []string{"TEST_TOKEN=synthetic-value", "LOOSE_TOKEN=synthetic-value"}, Args: []string{"--strict-mcp-config"}}, nil
	}
	inspector.Claude = func(context.Context, string, []string, []string) ([]connections.Entry, error) { return nil, nil }
	service := &Service{Store: store, Instance: "test-instance", connectionInspector: inspector}
	checks, _ := service.connections()
	defer checks.Close()
	checks.Get(meta.ID+"\n"+meta.SpawnGen, false)
	waitForConnectionCheck(t, checks, meta.ID+"\n"+meta.SpawnGen)
	handler := NewHTTP(service, "board.local", nil)

	for _, test := range []struct {
		name, variable, action, wantTitle string
		wantCode                          int
	}{
		{"replace a set declared variable", "TEST_TOKEN", "replace:TEST_TOKEN", "Replace TEST_TOKEN from clipboard for " + filepath.Base(meta.Project), 200},
		{"store a missing declared variable", "UNSET_TOKEN", "store:UNSET_TOKEN", "Store UNSET_TOKEN from clipboard for " + filepath.Base(meta.Project), 200},
		{"replace an unset variable", "UNSET_TOKEN", "replace:UNSET_TOKEN", "", 409},
		{"replace an undeclared variable", "LOOSE_TOKEN", "replace:LOOSE_TOKEN", "", 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			before := len(store.Snapshot().Runs)
			body, err := json.Marshal(connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:" + test.variable, Action: test.action})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "http://board.local/api/connections/fix", strings.NewReader(string(body)))
			request.Header.Set("Origin", "http://board.local")
			request.Header.Set("X-CEO-Token", "test-instance")
			response := httptest.NewRecorder()

			// Act
			handler.ServeHTTP(response, request)

			// Assert
			if response.Code != test.wantCode || strings.Contains(response.Body.String(), "synthetic-value") {
				t.Fatalf("fix returned %d: %s", response.Code, response.Body)
			}
			runs := store.Snapshot().Runs
			if test.wantCode != 200 {
				if len(runs) != before {
					t.Fatalf("a refused fix recorded a card: %d runs, want %d", len(runs), before)
				}
				return
			}
			var result map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(runs, func(run Run) bool { return run.ID == result["run_id"] })
			if index < 0 {
				t.Fatalf("run %q was not recorded", result["run_id"])
			}
			run := runs[index]
			if run.Title != test.wantTitle {
				t.Fatalf("title = %q, want %q", run.Title, test.wantTitle)
			}
			if !strings.Contains(run.Command, "Get-Clipboard -Raw | & ") || !strings.HasSuffix(run.Command, " auth store --project '"+meta.Project+"' '"+test.variable+"'\nexit $LASTEXITCODE\n") || strings.Contains(run.Command, "synthetic-value") {
				t.Fatalf("not the clipboard store for %s: %q", test.variable, run.Command)
			}
		})
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
		request.Header.Set("X-CEO-Token", "test-instance")
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
