package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestSaveQueuedEnginePreservesTaskAndOtherSettings(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	path := filepath.Join(h.Data, "backlog.md")
	original := "## Queued\n- **first** - Keep this title (small, focused) (repo: project, harness: claude, model: old, mode: direct-PR, effort: high) blocked-by: second\n  Keep this detail.\n- **second** - Keep me\n## Done\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ReadQueuedTask(h, "first")
	if err != nil {
		t.Fatal(err)
	}

	err = SaveQueuedEngine(h, "first", before.Revision, "pi", "z-ai/glm-5.3", "xhigh")

	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadQueuedTask(h, "first")
	if err != nil || after.Row.Title != before.Row.Title || after.Detail != before.Detail || after.Row.Repo != "project" || after.Row.Mode != "direct-PR" || after.Row.BlockedBy != "second" || after.Row.Harness != "pi" || after.Row.Model != "z-ai/glm-5.3" || after.Row.Effort != "xhigh" || after.Revision == before.Revision {
		t.Fatalf("saved choice = %+v, %v", after, err)
	}
	if err := SaveQueuedEngine(h, "first", before.Revision, "codex", "other", "high"); !errors.Is(err, ErrQueueChanged) {
		t.Fatalf("stale choice = %v", err)
	}
	other, err := ReadQueuedTask(h, "second")
	if err != nil || other.Row.Title != "Keep me" {
		t.Fatalf("other task = %+v, %v", other, err)
	}
}

func TestSaveQueuedEngineAddsABriefOnlyTaskWithoutRewritingItsBrief(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	path := filepath.Join(h.Data, "task", "brief.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	brief := "## Project\nproject\n## Task\nKeep this title\nKeep this detail.\n## Delivery\nmode: local-only\nharness: claude\n"
	if err := os.WriteFile(path, []byte(brief), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ReadQueuedTask(h, "task")
	if err != nil {
		t.Fatal(err)
	}

	err = SaveQueuedEngine(h, "task", before.Revision, "codex", "gpt-6.1-sol", "high")

	if err != nil {
		t.Fatal(err)
	}
	after, err := ReadQueuedTask(h, "task")
	if err != nil || after.IsBriefOnly || after.Row.Harness != "codex" || after.Row.Mode != "local-only" || after.Detail != before.Detail {
		t.Fatalf("saved brief task = %+v, %v", after, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != brief {
		t.Fatalf("brief changed = %q, %v", data, err)
	}
}

func TestSaveQueuedEngineRejectsBacklogInjection(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	for _, value := range []string{"bad\nvalue", "bad, effort: max", "bad) (mode: local-only", "bad\x00value"} {
		t.Run(value, func(t *testing.T) {
			if err := SaveQueuedEngine(h, "task", "revision", "codex", value, "high"); err == nil {
				t.Fatal("invalid engine choice accepted")
			}
		})
	}
}
