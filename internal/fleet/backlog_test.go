package fleet

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
)

func TestReadBacklogPreservesSectionsOrderAndUnstructuredRows(t *testing.T) {
	h := snapshotHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `# Backlog

## Queued
- [ ] q1 - Ship the view https://github.com/example/repo/pull/42 (repo: goblins, kind: ship, priority: high) blocked-by: prep - wait for API
- free-form queue note
- [ ] q2 - Scout result - data/q2/report.md (reported 2026-08-13)

## Done
- **d1** - Shipped https://github.com/example/repo/pull/7 (merged 2026-08-13)
- free-form done note
`
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	if !backlog.Present || len(backlog.Queued) != 3 || len(backlog.Done) != 2 {
		t.Fatalf("backlog = %+v, want source-order queued and done rows", backlog)
	}

	queued := backlog.Queued[0]
	if !queued.Structured || queued.ID != "q1" || queued.Title != "Ship the view" || queued.Repo != "goblins" || queued.Kind != "ship" {
		t.Errorf("queued row = %+v, want cleaned structured metadata", queued)
	}
	if queued.BlockedBy != "prep" || queued.BlockedReason != "wait for API" || queued.Artifact != "https://github.com/example/repo/pull/42" {
		t.Errorf("queued blockers/artifact = %+v, want blocker formatting and PR preference", queued)
	}
	if backlog.Queued[1].Structured || backlog.Queued[1].Raw != "- free-form queue note" {
		t.Errorf("unstructured queued row = %+v, want non-table source record", backlog.Queued[1])
	}
	if backlog.Queued[2].Title != "Scout result" || backlog.Queued[2].Artifact != "data/q2/report.md" {
		t.Errorf("queued report row = %+v, want report title cleanup", backlog.Queued[2])
	}

	done := backlog.Done[0]
	if !done.Structured || done.ID != "d1" || done.Title != "Shipped" || done.Artifact != "https://github.com/example/repo/pull/7" {
		t.Errorf("done row = %+v, want bold record and URL cleanup", done)
	}
	if backlog.Done[1].Structured || backlog.Done[1].Raw != "- free-form done note" {
		t.Errorf("unstructured done row = %+v, want non-table source record", backlog.Done[1])
	}
}

func TestReadBacklogMissingFileIsTypedEmpty(t *testing.T) {
	h := home.Home{Root: t.TempDir()}
	h.Data = filepath.Join(h.Root, "data")
	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	if backlog.Present || backlog.Queued == nil || backlog.Done == nil {
		t.Errorf("backlog = %#v, want typed empty missing backlog", backlog)
	}
}

func TestReadBacklogCleansWrappedURLsFromTitles(t *testing.T) {
	h := snapshotHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Queued\n- [ ] q1 - Wrapped link <https://github.com/example/repo/pull/42> (repo: goblins)\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	row := backlog.Queued[0]
	if row.Title != "Wrapped link" || row.Artifact != "https://github.com/example/repo/pull/42" {
		t.Errorf("wrapped URL row = %+v, want URL-free title and PR artifact", row)
	}
}

func TestReadBacklogRetainsCanonicalBlockersAndRawIndentation(t *testing.T) {
	h := snapshotHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Queued\r\n- [ ] q1 - Wait for prerequisite blocked-by: prep (repo: goblins)\r\n  - indented free-form queue note\r\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	canonical := backlog.Queued[0]
	if canonical.BlockedBy != "prep" || canonical.Title != "Wait for prerequisite" || canonical.Repo != "goblins" {
		t.Errorf("canonical blocker row = %+v, want blocker metadata outside the title", canonical)
	}
	if raw := backlog.Queued[1].Raw; raw != "  - indented free-form queue note" {
		t.Errorf("raw backlog row = %q, want its original normalized indentation", raw)
	}

	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, Snapshot{Backlog: backlog, Secondmates: []SecondmateRow{}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(markdown.Bytes(), []byte("  - indented free-form queue note")) {
		t.Errorf("Markdown does not retain raw backlog indentation:\n%s", markdown.String())
	}
}

func TestReadBacklogKeepsNestedHeadingsInsideTheCurrentSection(t *testing.T) {
	h := snapshotHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Queued\n- [ ] q1 - First\n### Notes\n- [ ] q2 - Second\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	if len(backlog.Queued) != 3 || backlog.Queued[0].ID != "q1" || backlog.Queued[1].Structured || backlog.Queued[1].Raw != "### Notes" || backlog.Queued[2].ID != "q2" {
		t.Errorf("queued rows = %+v, want both records and the nested heading as raw content", backlog.Queued)
	}
}

func TestReadBacklogPreservesEveryCanonicalBlocker(t *testing.T) {
	h := snapshotHome(t)
	if err := os.MkdirAll(h.Data, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "## Queued\n- [ ] q1 - Ship it blocked-by: worker blocked-by: review - needs final review (repo: goblins)\n"
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	backlog, err := ReadBacklog(h)
	if err != nil {
		t.Fatalf("ReadBacklog: %v", err)
	}
	row := backlog.Queued[0]
	if row.Title != "Ship it" || row.BlockedBy != "worker" || !reflect.DeepEqual(row.BlockedByIDs, []string{"worker", "review"}) || row.BlockedReason != "needs final review" {
		t.Errorf("multiple blocker row = %+v, want all blocker IDs and a clean final reason", row)
	}

	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, Snapshot{Backlog: backlog, Secondmates: []SecondmateRow{}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(markdown.Bytes(), []byte("| q1 | Ship it | goblins | - | worker, review - needs final review | - |")) {
		t.Errorf("Markdown does not show every blocker:\n%s", markdown.String())
	}
}
