package supervisor

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestConnectionAPIIsCachedAndRejectsStaleTasksAndUntrustedRefreshes(t *testing.T) {
	store, _ := testStore(t)
	release := make(chan struct{})
	checks := connections.NewCache(func(ctx context.Context, _ string) connections.Snapshot {
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
	checks := connections.NewCache(func(context.Context, string) connections.Snapshot {
		return connections.Snapshot{Entries: []connections.Entry{{ID: "service:sample", Status: "unauthorized", Actions: []string{"login"}}}}
	})
	defer checks.Close()
	checks.Get("task-1\ng1", false)
	deadline := time.Now().Add(time.Second)
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
	for index := range store.db.Runs {
		store.db.Runs[index].RunAction = "action"
		store.db.Runs[index].State = "running"
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
