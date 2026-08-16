package showcase

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func artifactPath(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenCreatesSessionBesideArtifact(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	session, err := Open(artifact, KindMarkdown, false)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if session.ID != SessionID(artifact) {
		t.Errorf("session id = %q, want %q", session.ID, SessionID(artifact))
	}
	if _, err := os.Stat(StatePath(artifact)); err != nil {
		t.Errorf("session file missing at %s: %v", StatePath(artifact), err)
	}
}

func TestQueuedFeedbackSurvivesReload(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := AppendFeedback(artifact, Feedback{Type: "annotation", Text: "rename this heading", Quote: "Plan", Selector: "#plan"}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}

	// A fresh load stands in for a restarted poll or server process: the
	// queued item must still be pending.
	session, err := Load(artifact)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(session.Feedback) != 1 || session.Feedback[0].Delivered {
		t.Fatalf("feedback after reload = %+v, want one pending item", session.Feedback)
	}
	if session.Feedback[0].ID != 1 {
		t.Errorf("feedback id = %d, want 1", session.Feedback[0].ID)
	}
}

func TestConsumeDeliversOnceAndReportsEndOnce(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := AppendFeedback(artifact, Feedback{Type: "message", Text: "looks good"}); err != nil {
		t.Fatalf("AppendFeedback: %v", err)
	}
	if err := End(artifact, "user"); err != nil {
		t.Fatalf("End: %v", err)
	}

	payload, delivered, err := Consume(artifact)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !delivered || len(payload.Feedback) != 1 || payload.Feedback[0].Text != "looks good" {
		t.Fatalf("first payload = %+v (delivered %t), want the queued message", payload, delivered)
	}
	if !payload.Ended || payload.EndedBy != "user" {
		t.Errorf("first payload ended = %t by %q, want ended by user", payload.Ended, payload.EndedBy)
	}

	// Feedback is consumed; the ended state was reported with it. A second
	// consume must have nothing left to deliver.
	payload, delivered, err = Consume(artifact)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if delivered {
		t.Errorf("second consume delivered %+v, want silence", payload)
	}
}

func TestEndWithoutFeedbackIsReportedOnce(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if err := End(artifact, "user"); err != nil {
		t.Fatalf("End: %v", err)
	}
	payload, delivered, err := Consume(artifact)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !delivered || !payload.Ended || payload.EndedBy != "user" || len(payload.Feedback) != 0 {
		t.Fatalf("payload = %+v (delivered %t), want a single ended report", payload, delivered)
	}
	if _, delivered, err := Consume(artifact); err != nil || delivered {
		t.Errorf("second consume delivered=%t err=%v, want none", delivered, err)
	}
}

func TestUserEndedSessionRefusesReopenAndFeedback(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := End(artifact, "user"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := Open(artifact, KindMarkdown, false); !errors.Is(err, ErrEndedByUser) {
		t.Errorf("Open after user end = %v, want ErrEndedByUser", err)
	}
	if err := AppendFeedback(artifact, Feedback{Type: "message", Text: "late"}); !errors.Is(err, ErrEndedByUser) {
		t.Errorf("AppendFeedback after user end = %v, want ErrEndedByUser", err)
	}
	if _, err := Open(artifact, KindMarkdown, true); err != nil {
		t.Fatalf("Open with reopen: %v", err)
	}
	session, err := Load(artifact)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if session.EndedBy != "" || session.EndedAt != nil {
		t.Errorf("reopened session still ended: %+v", session)
	}
}

func TestAgentEndedSessionReopensFreely(t *testing.T) {
	artifact := artifactPath(t, "plan.md", "# Plan\n")
	if err := End(artifact, "agent"); err != nil {
		t.Fatalf("End: %v", err)
	}
	if _, err := Open(artifact, KindMarkdown, false); err != nil {
		t.Errorf("Open after agent end: %v", err)
	}
}

func TestStatePathNestsOnlyOutsideShowcaseDir(t *testing.T) {
	plain := StatePath(filepath.Join("work", "plan.md"))
	if filepath.Base(filepath.Dir(plain)) != StateDirName {
		t.Errorf("StatePath outside .showcase = %q, want a .showcase parent", plain)
	}
	nested := StatePath(filepath.Join("work", StateDirName, "plan.md"))
	if filepath.Base(filepath.Dir(nested)) != StateDirName || filepath.Base(filepath.Dir(filepath.Dir(nested))) == StateDirName {
		t.Errorf("StatePath inside .showcase = %q, want the state file beside the artifact", nested)
	}
}

func TestLoadMissingSessionIsNotExist(t *testing.T) {
	artifact := filepath.Join(t.TempDir(), "plan.md")
	if _, err := Load(artifact); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load missing = %v, want ErrNotExist", err)
	}
}
