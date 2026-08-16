package showcase

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestAllowMutationOriginHostCaseInsensitive(t *testing.T) {
	server := NewServer(t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
	req.Host = "LOCALHOST:1234"
	req.Header.Set("Origin", "http://localhost:1234")
	if !server.allowMutation(req) {
		t.Errorf("allowMutation rejected a same-origin request whose host case differs")
	}

	cross := httptest.NewRequest(http.MethodPost, "/api/stop", nil)
	cross.Host = "localhost:1234"
	cross.Header.Set("Origin", "http://evil.example")
	if server.allowMutation(cross) {
		t.Errorf("allowMutation accepted a cross-origin request")
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
