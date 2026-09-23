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
	"testing/fstest"
	"time"
)

func TestTerminalStyleNonceIsUniqueAndDoesNotRelaxScripts(t *testing.T) {
	store, _ := testStore(t)
	h := NewHTTP(&Service{Store: store}, "board.local", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")}})
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/", nil))
		policy := response.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "script-src 'self';") || strings.Contains(policy, "unsafe-inline") || !strings.Contains(policy, "style-src 'self' 'nonce-") {
			t.Fatal("unexpected CSP", policy)
		}
		if seen[response.Body.String()] || !strings.Contains(response.Body.String(), "cfo-style-nonce") {
			t.Fatal("style nonce missing or reused")
		}
		seen[response.Body.String()] = true
	}
}

func TestNativeCaptureRequestsAreBoundedAcrossRecipients(t *testing.T) {
	store, _ := testStore(t)
	entered, release := make(chan struct{}), make(chan struct{})
	service := &Service{Store: store, Options: Options{Peek: func(ctx context.Context, _ string, _ int) (string, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 8*time.Second {
			return "", fmt.Errorf("capture has no bounded deadline")
		}
		close(entered)
		<-release
		return "Native output\nSecond line", nil
	}}}
	handler := NewHTTP(service, "board.local", nil)
	completed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/api/tasks/task-1/terminal", nil))
		completed <- response
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("capture did not begin")
	}
	for _, path := range []string{"/api/cfo", "/api/tasks/task-1/terminal"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		if response.Code != 503 {
			close(release)
			t.Fatalf("overlapping capture admitted: %d", response.Code)
		}
	}
	close(release)
	if response := <-completed; response.Code != 200 || !strings.Contains(response.Body.String(), `Native output\nSecond line`) {
		t.Fatalf("native capture failed: %d %s", response.Code, response.Body.String())
	}
}

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
