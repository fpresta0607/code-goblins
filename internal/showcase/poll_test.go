package showcase

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPollDeliversQueuedFeedbackAsJSON(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := AppendFeedback(artifact, Feedback{Type: "selection", Text: "tighten this", Quote: "Plan"}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}

	var out strings.Builder
	if err := Poll(context.Background(), artifact, &out); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	var payload Payload
	if err := json.Unmarshal([]byte(out.String()), &payload); err != nil {
		t.Fatalf("poll output is not JSON: %q: %v", out.String(), err)
	}
	if payload.Session != SessionID(artifact) || payload.Kind != KindMarkdown {
		t.Errorf("payload = %+v, want session id and kind", payload)
	}
	if len(payload.Feedback) != 1 || payload.Feedback[0].Quote != "Plan" {
		t.Errorf("payload feedback = %+v, want the queued selection", payload.Feedback)
	}
}

func TestPollWaitsForFeedback(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}

	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Poll(context.Background(), artifact, &out)
	}()

	select {
	case err := <-done:
		t.Fatalf("Poll returned %v before any feedback existed", err)
	case <-time.After(1200 * time.Millisecond):
		// Still waiting, as promised.
	}

	if err := AppendFeedback(artifact, Feedback{Type: "message", Text: "ship it"}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Poll did not deliver queued feedback")
	}
	if !strings.Contains(out.String(), "ship it") {
		t.Errorf("poll output = %q, want the queued message", out.String())
	}
}

func TestPollHonorsContextCancel(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	var out strings.Builder
	err := Poll(ctx, artifact, &out)
	if err == nil {
		t.Fatal("Poll returned nil without feedback")
	}
	if out.String() != "" {
		t.Errorf("Poll printed %q while silent waiting was expected", out.String())
	}
}
