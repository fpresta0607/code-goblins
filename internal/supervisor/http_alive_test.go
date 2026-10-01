package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// /api/alive is how goblins, its status and its stop tell a live supervisor
// from a stale record without building the fleet's snapshot: it answers this
// supervisor's own pid, and only on the board's own host, like every route.
func TestTheAliveRouteAnswersTheSupervisorsPidOnlyOnItsOwnHost(t *testing.T) {
	_, h := testStore(t)
	s, err := Start(context.Background(), h, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := NewHTTP(s, "", nil)
	server := httptest.NewServer(handler)
	defer server.Close()
	handler.Host = strings.TrimPrefix(server.URL, "http://")
	get := func(host string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(http.MethodGet, server.URL+"/api/alive", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = host
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}

	response := get(handler.Host)
	var alive struct {
		PID int `json:"pid"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&alive) != nil || alive.PID != os.Getpid() {
		t.Fatalf("GET /api/alive = HTTP %d pid %d, want 200 naming this supervisor's pid %d", response.StatusCode, alive.PID, os.Getpid())
	}

	if untrusted := get("evil.example:4310"); untrusted.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /api/alive from another host = HTTP %d, want 403", untrusted.StatusCode)
	}
}
