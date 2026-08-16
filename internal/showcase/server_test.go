package showcase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server := NewServer(t.TempDir())
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return server, ts
}

func registerSession(t *testing.T, ts *httptest.Server, artifact string, reopen bool) registerResponse {
	t.Helper()
	body, _ := json.Marshal(registerRequest{Path: artifact, Reopen: reopen})
	response, err := http.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(response.Body)
		t.Fatalf("register status = %d: %s", response.StatusCode, detail)
	}
	var registered registerResponse
	if err := json.NewDecoder(response.Body).Decode(&registered); err != nil {
		t.Fatalf("register decode: %v", err)
	}
	return registered
}

func TestServerServesPageAndCollectsFeedback(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n\nReview me.\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	response, err := http.Get(ts.URL + registered.URL)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("page status = %d", response.StatusCode)
	}
	page, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(page), "Review me.") {
		t.Errorf("page lacks the rendered markdown")
	}
	if !strings.Contains(string(page), "conversation") && !strings.Contains(string(page), "Conversation") {
		t.Errorf("page lacks the conversation panel")
	}

	feedback, _ := json.Marshal(Feedback{Type: "annotation", Text: "reword the intro", Selector: "#plan p"})
	res, err := http.Post(ts.URL+registered.URL+"feedback", "application/json", bytes.NewReader(feedback))
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("feedback status = %d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + registered.URL + "state")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	defer res.Body.Close()
	var session Session
	if err := json.NewDecoder(res.Body).Decode(&session); err != nil {
		t.Fatalf("state decode: %v", err)
	}
	if len(session.Feedback) != 1 || session.Feedback[0].Text != "reword the intro" {
		t.Errorf("state feedback = %+v, want the posted annotation", session.Feedback)
	}
}

func TestServerEndProtocol(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	res, err := http.Post(ts.URL+registered.URL+"end", "application/json", nil)
	if err != nil {
		t.Fatalf("end: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("end status = %d", res.StatusCode)
	}

	// The browser session is over: feedback and silent reopens are refused.
	feedback, _ := json.Marshal(Feedback{Type: "message", Text: "too late"})
	res, err = http.Post(ts.URL+registered.URL+"feedback", "application/json", bytes.NewReader(feedback))
	if err != nil {
		t.Fatalf("feedback after end: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("feedback after user end status = %d, want 409", res.StatusCode)
	}

	body, _ := json.Marshal(registerRequest{Path: artifact})
	res, err = http.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register after end: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Errorf("register after user end status = %d, want 409", res.StatusCode)
	}

	registeredAgain := registerSession(t, ts, artifact, true)
	if registeredAgain.ID != registered.ID {
		t.Errorf("reopen id = %q, want the resumed session %q", registeredAgain.ID, registered.ID)
	}
}

func TestServerHTMLArtifactFrameSandbox(t *testing.T) {
	artifact := artifactPath(t, "mock.html", "<h1>Mock</h1>\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	res, err := http.Get(ts.URL + registered.URL)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	defer res.Body.Close()
	page, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(page), "allow-scripts") {
		t.Errorf("HTML artifact frame is not script-sandboxed")
	}
	if strings.Contains(string(page), "allow-same-origin") {
		t.Errorf("HTML artifact frame grants same-origin access to the artifact")
	}
}

func TestAllowMutationPinsLoopbackOrigin(t *testing.T) {
	server := NewServer(t.TempDir())

	newRequest := func(origin, host string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4321}))
		return req
	}

	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"cli sends no origin", "", "", true},
		{"localhost same port", "http://localhost:4321", "localhost:4321", true},
		{"loopback ip same port", "http://127.0.0.1:4321", "127.0.0.1:4321", true},
		{"mixed case localhost", "http://LOCALHOST:4321", "localhost:4321", true},
		{"dns rebinding attacker", "http://evil.example:4321", "evil.example:4321", false},
		{"null origin", "null", "localhost:4321", false},
		{"different port", "http://localhost:9999", "localhost:9999", false},
		{"https scheme", "https://localhost:4321", "localhost:4321", false},
	}
	for _, tc := range cases {
		if got := server.allowMutation(newRequest(tc.origin, tc.host)); got != tc.want {
			t.Errorf("%s: allowMutation = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestServerRejectsCrossOriginMutations(t *testing.T) {
	artifact := artifactPath(t, "mock.html", "<h1>Mock</h1>\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	for _, target := range []string{"/api/stop", registered.URL + "end"} {
		req, err := http.NewRequest(http.MethodPost, ts.URL+target, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", "null")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", target, res.StatusCode)
		}
	}
}

func TestServerServesArtifactSiblingsOnlyInsideTheDirectory(t *testing.T) {
	artifact := artifactPath(t, "mock.html", "<link rel=\"stylesheet\" href=\"style.css\"><h1>Mock</h1>\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	res, err := http.Get(ts.URL + registered.URL + "raw")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), "<h1>Mock</h1>") {
		t.Errorf("raw did not serve the artifact: %s", body)
	}

	res, err = http.Get(ts.URL + registered.URL + "..%2f..%2fetc%2fhostname")
	if err != nil {
		t.Fatalf("escape attempt: %v", err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		t.Errorf("path escape was served; want refusal")
	}
}

func TestServerAssetEndpointsAllowOpaqueOrigin(t *testing.T) {
	artifact := artifactPath(t, "mock.html", `<script type="module" src="app.js"></script>`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(artifact), "app.js"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	for _, path := range []string{"raw", "app.js"} {
		res, err := http.Get(ts.URL + registered.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		res.Body.Close()
		if got := res.Header.Get("Access-Control-Allow-Origin"); got != "null" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, want %q", path, got, "null")
		}
	}
}
