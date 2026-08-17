package showcase

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

// A selection comment is only useful if the agent can find the text again.
// These tests pin the anchor contract the browser posts and poll delivers:
// the exact quote, the element path, and the nearest heading or section
// header above it.

func TestSelectionAnchorReachesPollPayload(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n\n## Rollout\n\nShip on Friday.\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	posted := Feedback{
		Type:     "selection",
		Text:     "pick a date that is not a Friday",
		Quote:    "Ship on Friday.",
		Selector: "div > p:nth-of-type(1)",
		Section:  "Rollout",
	}
	body, _ := json.Marshal(posted)
	res, err := http.Post(ts.URL+registered.URL+"feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("feedback status = %d, want 204", res.StatusCode)
	}

	var out strings.Builder
	if err := Poll(context.Background(), artifact, &out); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	// The agent reads the payload as JSON, so pin the wire keys, not just
	// the Go field names.
	var wire struct {
		Feedback []map[string]any `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(out.String()), &wire); err != nil {
		t.Fatalf("poll output is not JSON: %q: %v", out.String(), err)
	}
	if len(wire.Feedback) != 1 {
		t.Fatalf("poll delivered %d items, want 1", len(wire.Feedback))
	}
	item := wire.Feedback[0]
	for key, want := range map[string]string{
		"type":     "selection",
		"text":     "pick a date that is not a Friday",
		"quote":    "Ship on Friday.",
		"selector": "div > p:nth-of-type(1)",
		"section":  "Rollout",
	} {
		if got, _ := item[key].(string); got != want {
			t.Errorf("payload %q = %q, want %q", key, got, want)
		}
	}
}

func TestSelectionAnchorFieldsAreClamped(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	body, _ := json.Marshal(Feedback{
		Type:     "selection",
		Text:     "  fix this  ",
		Quote:    strings.Repeat("q", 5000),
		Selector: strings.Repeat("s", 900),
		Section:  strings.Repeat("h", 900),
		Context:  strings.Repeat("c", 900),
	})
	res, err := http.Post(ts.URL+registered.URL+"feedback", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("feedback: %v", err)
	}
	res.Body.Close()

	session, err := Load(artifact)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(session.Feedback) != 1 {
		t.Fatalf("queued %d items, want 1", len(session.Feedback))
	}
	stored := session.Feedback[0]
	if stored.Text != "fix this" {
		t.Errorf("text = %q, want it trimmed", stored.Text)
	}
	for name, field := range map[string]struct {
		value string
		max   int
	}{
		"quote":    {stored.Quote, 2000},
		"selector": {stored.Selector, 400},
		"section":  {stored.Section, 200},
		"context":  {stored.Context, 200},
	} {
		if len(field.value) != field.max {
			t.Errorf("%s length = %d, want it clamped to %d", name, len(field.value), field.max)
		}
	}
}

// An HTML mock runs in an opaque origin, so the review page can only learn
// about a selection inside it if the preview response carries the forwarding
// helper. The artifact on disk and its export must stay untouched.
func TestLivePreviewCarriesTheFrameHelperAndTheArtifactDoesNot(t *testing.T) {
	source := "<h1 id=\"hero\">Mock</h1>\n"
	artifact := artifactPath(t, "mock.html", source)
	_, ts := newTestServer(t)
	registered := registerSession(t, ts, artifact, false)

	res, err := http.Get(ts.URL + registered.URL + "raw")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	preview, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(preview), "<h1 id=\"hero\">Mock</h1>") {
		t.Errorf("preview lost the artifact markup: %s", preview)
	}
	if !strings.Contains(string(preview), `__showcase: "selection"`) {
		t.Errorf("preview does not forward selections to the review page")
	}

	onDisk, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != source {
		t.Errorf("artifact on disk = %q, want it unmodified", onDisk)
	}

	out, err := Export(artifact, "")
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	exported, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(exported), "__showcase") {
		t.Errorf("export leaked the frame helper")
	}
}
