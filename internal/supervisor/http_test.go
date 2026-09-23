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

func TestTerminalCSPAllowsColorAttributesWithoutRelaxingScriptsOrStyleElements(t *testing.T) {
	store, _ := testStore(t)
	h := NewHTTP(&Service{Store: store}, "board.local", fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body></body></html>")}})
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local/", nil))
		policy := response.Header().Get("Content-Security-Policy")
		directives := map[string]string{}
		for _, directive := range strings.Split(policy, ";") {
			fields := strings.Fields(directive)
			if len(fields) == 0 {
				continue
			}
			if _, duplicate := directives[fields[0]]; duplicate {
				t.Fatal("duplicate CSP directive", fields[0])
			}
			directives[fields[0]] = strings.Join(fields[1:], " ")
		}
		want := map[string]string{
			"default-src": "'self'", "script-src": "'self'", "style-src-attr": "'unsafe-inline'",
			"img-src": "'self' data:", "connect-src": "'self'", "frame-ancestors": "'none'",
			"base-uri": "'none'", "form-action": "'self'",
		}
		// Unlisted script/style element directives could override these fallbacks.
		if len(directives) != len(want)+1 {
			t.Fatal("unexpected CSP directive", policy)
		}
		for directive, sources := range want {
			if directives[directive] != sources {
				t.Fatalf("%s = %q, want %q", directive, directives[directive], sources)
			}
		}
		styles := strings.Fields(directives["style-src"])
		if len(styles) != 2 || styles[0] != "'self'" || !strings.HasPrefix(styles[1], "'nonce-") || !strings.HasSuffix(styles[1], "'") {
			t.Fatal("inline style elements are not nonce restricted", policy)
		}
		nonce := strings.TrimSuffix(strings.TrimPrefix(styles[1], "'nonce-"), "'")
		if nonce == "" || !strings.Contains(response.Body.String(), `content="`+nonce+`"`) {
			t.Fatal("style nonce does not match the document")
		}
		if seen[response.Body.String()] || !strings.Contains(response.Body.String(), "cfo-style-nonce") {
			t.Fatal("style nonce missing or reused")
		}
		seen[response.Body.String()] = true
	}
}

func TestRetiredTextCaptureEndpointsAreNotExposed(t *testing.T) {
	store, _ := testStore(t)
	handler := NewHTTP(&Service{Store: store}, "board.local", nil)
	for _, path := range []string{"/api/cfo", "/api/tasks/task-1/terminal"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://board.local"+path, nil))
		if response.Code != 404 {
			t.Fatalf("retired capture endpoint %s returned %d", path, response.Code)
		}
	}
}

func TestAPIOriginIdempotencySafePathsAndReconnect(t *testing.T) {
	_, h := testStore(t)
	meta, _ := state.ReadTaskMeta(h.State, "task-1")
	gitFixture(t, meta.Worktree)
	meta.Mode = "local-only"
	_ = state.WriteTaskMeta(h.State, meta)
	s, err := Start(context.Background(), h, Options{})
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
	body := `{"id":"request-1","task_id":"task-1","generation":"g1","kind":"evaluate"}`
	for _, origin := range []string{"", "https://evil.invalid"} {
		code, _ := request("POST", "/api/actions", body, origin, s.Instance, "")
		if code != 403 {
			t.Fatalf("untrusted origin allowed: %d", code)
		}
	}
	if code, _ := request("GET", "/api/snapshot", "", "", "", "evil.invalid"); code != 403 {
		t.Fatal("rebound host allowed")
	}
	for _, obsolete := range []string{
		`{"id":"obsolete-feedback","task_id":"task-1","generation":"g1","kind":"feedback","text":"please inspect"}`,
		`{"id":"obsolete-message","generation":"g1","kind":"cfo_message","text":"hello"}`,
	} {
		code, _ := request("POST", "/api/actions", obsolete, server.URL, s.Instance, "")
		if code != 409 {
			t.Fatalf("obsolete public action admitted: %d", code)
		}
	}
	for i := 0; i < 2; i++ {
		code, data := request("POST", "/api/actions", body, server.URL, s.Instance, "")
		if code != 202 {
			t.Fatalf("%d %s", code, data)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (len(s.Store.Snapshot().Actions) != 1 || s.Store.Snapshot().Actions[0].Status != "succeeded") {
		time.Sleep(10 * time.Millisecond)
	}
	if actions := s.Store.Snapshot().Actions; len(actions) != 1 || actions[0].Status != "succeeded" {
		t.Fatalf("idempotent evaluation was not processed once: %+v", actions)
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
