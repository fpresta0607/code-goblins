package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fpresta0607/code-goblins/internal/state"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAPIOriginIdempotencySafePathsAndReconnect(t *testing.T) {
	_, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	meta.Mode = "local-only"
	_ = state.WriteTaskMeta(h.State, meta)
	sent := make(chan string, 2)
	s, err := Start(context.Background(), h, Options{Send: func(_ context.Context, _ string, text string) error { sent <- text; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	request := func(method, path, body, origin, token, host string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CFO-Token", token)
		if host != "" {
			req.Host = host
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, data
	}
	body := `{"id":"request-1","task_id":"task-1","generation":"g1","kind":"feedback","text":"please inspect the test"}`
	for _, origin := range []string{"", "https://evil.invalid"} {
		code, _ := request("POST", "/api/actions", body, origin, s.Instance, "")
		if code != 403 {
			t.Fatalf("untrusted origin allowed: %d", code)
		}
	}
	if code, _ := request("GET", "/api/snapshot", "", "", "", "evil.invalid"); code != 403 {
		t.Fatal("rebound host allowed")
	}
	for i := 0; i < 2; i++ {
		code, data := request("POST", "/api/actions", body, server.URL, s.Instance, "")
		if code != 202 {
			t.Fatalf("%d %s", code, data)
		}
	}
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("feedback not delivered")
	}
	select {
	case <-sent:
		t.Fatal("replayed feedback was sent twice")
	case <-time.After(100 * time.Millisecond):
	}
	if code, _ := request("GET", "/api/tasks/task-1/diff?path=../.env", "", "", "", ""); code != 422 {
		t.Fatal("unsafe preview admitted")
	}
	code, data := request("GET", "/api/tasks/task-1/files", "", "", "", "")
	if code != 200 || !strings.Contains(string(data), "main.go") {
		t.Fatalf("file API %d %s", code, data)
	}
	code, data = request("GET", "/api/snapshot", "", "", "", "")
	var snapshot Snapshot
	if code != 200 || json.Unmarshal(data, &snapshot) != nil || len(snapshot.Tasks) != 1 {
		t.Fatalf("snapshot %d %s", code, data)
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/events", nil)
		req.Header.Set("Last-Event-ID", "obsolete-instance:999")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		buf := make([]byte, 512)
		n, _ := response.Body.Read(buf)
		response.Body.Close()
		cancel()
		if !strings.Contains(string(buf[:n]), fmt.Sprintf("id: %s:", s.Instance)) {
			t.Fatal("reconnect did not receive authoritative snapshot")
		}
	}
}
