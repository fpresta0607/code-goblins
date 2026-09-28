package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestQueuedTaskIncludesOnlyItsOwnDetailsAndRevision(t *testing.T) {
	data := t.TempDir()
	h := home.Home{Data: data}
	text := "## Queued\n- **first** - Fix search (repo: code-goblins)\n  Search by name.\n  Keep the current styling.\n- **second** - Another task\n  Not this detail.\n## Parked\n- **parked** - Later\n"
	if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := ReadQueuedTask(h, "first")
	if err != nil || task.Row.Title != "Fix search" || task.Row.Repo != "code-goblins" || task.Detail != "Search by name.\nKeep the current styling." || task.Revision == "" {
		t.Fatalf("queued task = %+v %v", task, err)
	}
	if _, err := ReadQueuedTask(h, "parked"); err == nil {
		t.Fatal("parked task read as queued")
	}
	if err := os.WriteFile(filepath.Join(data, "backlog.md"), []byte(text+"\n## Queued\n- **first** - duplicate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueuedTask(h, "first"); err == nil {
		t.Fatal("ambiguous task was accepted")
	}
}

func TestSaveQueuedTaskChangesItsTextAndBriefButPreservesSettings(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	original := "## Queued\n- **first** - Old title (repo: project, mode: direct-PR, priority: high) blocked-by: dependency\n  Old detail.\n- **second** - Keep me\n## Parked\n"
	path := filepath.Join(h.Data, "backlog.md")
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(h.Data, "first"), 0o700); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(h.Data, "first", "brief.md")
	if err := os.WriteFile(brief, []byte("# Brief first\n\n## Project\n\nproject\n\n## Task\n\nOld title\n\n## Constraints\n\nKeep this rule.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ReadQueuedTask(h, "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveQueuedTask(h, "first", before.Revision, "New title\nNew detail."); err != nil {
		t.Fatal(err)
	}
	after, err := ReadQueuedTask(h, "first")
	if err != nil || after.Row.Title != "New title" || after.Detail != "New detail." || after.Row.Mode != "direct-PR" || after.Row.BlockedBy != "dependency" || before.Revision == after.Revision {
		t.Fatalf("saved=%+v %v", after, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "priority: high") || !strings.Contains(string(data), "- **second** - Keep me") {
		t.Fatalf("unrelated data changed: %s", data)
	}
	data, _ = os.ReadFile(brief)
	for _, want := range []string{"New title", "New detail.", "Keep this rule.", "Adjusted from the board"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("brief missing %q: %s", want, data)
		}
	}
	if err := SaveQueuedTask(h, "first", before.Revision, "Stale edit"); err == nil {
		t.Fatal("stale edit accepted")
	}
	if err := SaveQueuedTask(h, "first", after.Revision, "\n\t"); err == nil {
		t.Fatal("empty edit accepted")
	}
}

func TestStopQueuedTaskKeepsItsBriefAndUnrelatedBacklog(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	path := filepath.Join(h.Data, "backlog.md")
	if err := os.WriteFile(path, []byte("## Queued\n- **first** - Stop me (repo: project)\n  My detail.\n- **second** - Keep me\n## Done\n- **older** - Delivered\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := ReadQueuedTask(h, "first")
	if err := RemoveQueuedTask(h, "first", before.Revision); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "Stop me") || strings.Contains(string(data), "My detail.") || !strings.Contains(string(data), "Keep me") || !strings.Contains(string(data), "Delivered") {
		t.Fatalf("backlog=%s", data)
	}
}

func TestAdjustUndispatchedBriefAddsItsTaskWithoutLosingSettings(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	if err := os.Mkdir(filepath.Join(h.Data, "task"), 0o700); err != nil {
		t.Fatal(err)
	}
	brief := "## Project\n\nproject\n\n## Task\n\nOriginal title\n\nOriginal detail.\n\n## Delivery\n\nmode: local-only\nharness: codex\n"
	path := filepath.Join(h.Data, "task", "brief.md")
	if err := os.WriteFile(path, []byte(brief), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := ReadQueuedTask(h, "task")
	if err != nil || !before.IsBriefOnly || before.Row.Title != "Original title" || before.Row.Mode != "local-only" {
		t.Fatalf("queued brief=%+v %v", before, err)
	}
	if err := SaveQueuedTask(h, "task", before.Revision, "Revised title\nRevised detail."); err != nil {
		t.Fatal(err)
	}
	after, err := ReadQueuedTask(h, "task")
	if err != nil || after.IsBriefOnly || after.Row.Title != "Revised title" || after.Detail != "Revised detail." || after.Row.Repo != "project" || after.Row.Harness != "codex" || after.Row.Mode != "local-only" {
		t.Fatalf("saved brief=%+v %v", after, err)
	}
	for _, detail := range []string{"New title\n## Done", "New title\n- **injected** - New task", "New title\nnull\x00detail"} {
		if err := SaveQueuedTask(h, "task", after.Revision, detail); err == nil {
			t.Fatalf("accepted backlog syntax as detail: %q", detail)
		}
	}
	unchanged, err := ReadQueuedTask(h, "task")
	if err != nil || unchanged.Revision != after.Revision {
		t.Fatalf("invalid detail changed the task: %+v %v", unchanged, err)
	}
}

func TestAnArchivedBriefIsNotAnUndispatchedTask(t *testing.T) {
	h := home.Home{Data: t.TempDir(), State: t.TempDir()}
	if err := os.Mkdir(filepath.Join(h.Data, "task"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "task", "brief.md"), []byte("## Project\nproject\n## Task\nAlready ended\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.State, "archive", "task.20260928T000000Z"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueuedTask(h, "task"); err != ErrNotQueued {
		t.Fatalf("archived brief became queued: %v", err)
	}
}
