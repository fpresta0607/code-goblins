package workreview

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

func writeFixture(t *testing.T, path, text string, modified time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if !modified.IsZero() {
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}
}

// fixtureHome is a home with one item of each kind a review finds, in the
// widgets project of acme/widgets: a paused task, whose row stays queued, a
// row whose issue is closed, a step for the Overlord whose date passed, two
// rows that name one open issue, the first waiting on another pull request, a parked row left alone since October, and a
// held row of a project with no repository.
func fixtureHome(t *testing.T) (home.Home, string) {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	checkout := filepath.Join(root, "widgets")
	briefTime := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

	pausedBrief := filepath.Join(h.State, "tasktmp", "paused-task", "brief.md")
	writeFixture(t, pausedBrief, "# Brief paused-task\n\n## Task\n\nExport https://github.com/acme/widgets/issues/40 from internal/export.go.\n", time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	if err := state.WriteMeta(state.TaskMetaPath(h.State, "paused-task"), map[string]string{"project": checkout, "brief": pausedBrief, "spawn_gen": "g1", "backend": "native", "pr": "https://github.com/acme/widgets/pull/50"}); err != nil {
		t.Fatal(err)
	}
	pause, err := state.NewPauseCondition("overlord", "", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "paused-task", Generation: "g1", Operation: "pause-1", Action: "pause", Phase: "paused", Started: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Pause: &pause}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, state.StatusPath(h.State, "paused-task"), "2026-11-01T10:00:00Z working: the export\n", time.Time{})

	writeFixture(t, filepath.Join(h.Data, "fold-a", "brief.md"), "# Brief fold-a\n\n## Task\n\nSort the list in internal/list.go.\n", briefTime)
	writeFixture(t, filepath.Join(h.Data, "fold-b", "brief.md"), "# Brief fold-b\n\n## Project\n\nwidgets (the widgets repository)\n\n## Task\n\nSort it again.\n", briefTime)
	writeFixture(t, filepath.Join(h.Data, "backlog.md"), `# Backlog

## Queued
- [ ] paused-task - Widgets: the export, issues 40 and 41 (repo: widgets)
- [ ] queued-done - Widgets: ship the list, issue 11 (repo: widgets)
- [ ] his-step - Approve the rollout (repo: widgets, kind: his) blocked-by: until 2026-11-20T00:00Z blocked-by: https://github.com/acme/widgets/pull/60
- [ ] fold-a - Sort the list, issue 30 (repo: widgets) blocked-by: https://github.com/acme/widgets/pull/70
- [ ] fold-b - Sort the list again, issue 30
- [ ] held-elsewhere - Fix #7 and acme/gadgets#8 (repo: gadgets) (hold: vendor)

## Parked
- [ ] parked-old - Set aside (repo: widgets)
  2026-10-01 parked for the Overlord's call
  05:43Z 18 Oct, ceo: still waiting

## Done
`, time.Time{})
	return h, checkout
}

func fixtureResolver(checkout string) Resolver {
	return func(project string) (string, string) {
		if project == "widgets" || project == checkout {
			return "acme/widgets", checkout
		}
		return "", ""
	}
}

func TestGatherReadsPausedTasksThenQueuedRowsThenParkedRows(t *testing.T) {
	// Arrange
	h, checkout := fixtureHome(t)
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	widgets := func(number int) tickets.IssueRef { return tickets.IssueRef{Repository: "acme/widgets", Number: number} }

	// Act
	items, problems, err := Gather(h, fixtureResolver(checkout), now)

	// Assert
	if err != nil || len(problems) != 0 {
		t.Fatalf("Gather: problems %q, err %v", problems, err)
	}
	want := []Item{
		{ID: "paused-task", Source: SourcePaused, Project: checkout, Repository: "acme/widgets", IsOwnerStep: true, Issues: []tickets.IssueRef{widgets(40), widgets(41)}, Pulls: []tickets.IssueRef{widgets(50)}, Paths: []string{"internal/export.go"}, LastActivity: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC), Order: 0},
		{ID: "queued-done", Source: SourceQueued, Project: "widgets", Repository: "acme/widgets", Issues: []tickets.IssueRef{widgets(11)}, Order: 1},
		{ID: "his-step", Source: SourceQueued, Project: "widgets", Repository: "acme/widgets", IsOwnerStep: true, Pulls: []tickets.IssueRef{widgets(60)}, Dates: []time.Time{time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}, Order: 2},
		{ID: "fold-a", Source: SourceQueued, Project: "widgets", Repository: "acme/widgets", Issues: []tickets.IssueRef{widgets(30)}, Paths: []string{"internal/list.go"}, LastActivity: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), Order: 3},
		{ID: "fold-b", Source: SourceQueued, Project: "widgets", Repository: "acme/widgets", Issues: []tickets.IssueRef{widgets(30)}, LastActivity: time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC), Order: 4},
		{ID: "held-elsewhere", Source: SourceQueued, Project: "gadgets", IsHeld: true, Issues: []tickets.IssueRef{{Repository: "acme/gadgets", Number: 8}}, Order: 5},
		{ID: "parked-old", Source: SourceParked, Project: "widgets", Repository: "acme/widgets", IsHeld: true, LastActivity: time.Date(2026, 10, 18, 5, 43, 0, 0, time.UTC), Order: 6},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items:\n got %+v\nwant %+v", items, want)
	}
}

func TestNoteLineStampReadsADateAndATimeWithNoYear(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		line string
		want time.Time
	}{
		{"date", "2026-11-18 parked on purpose", time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{"bulleted date", "- 2026-11-18 parked on purpose", time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)},
		{"time of this year", "05:43Z 18 Nov, ceo: waiting", time.Date(2026, 11, 18, 5, 43, 0, 0, time.UTC)},
		{"time later than now today is last year's", "13:00Z 24 Nov, ceo: waiting", time.Date(2025, 11, 24, 13, 0, 0, 0, time.UTC)},
		{"time that would be in the future is last year's", "05:43Z 30 Dec, ceo: waiting", time.Date(2025, 12, 30, 5, 43, 0, 0, time.UTC)},
		{"no stamp", "waiting for the Overlord", time.Time{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := noteLineStamp(test.line, now); !got.Equal(test.want) {
				t.Errorf("noteLineStamp(%q) = %v, want %v", test.line, got, test.want)
			}
		})
	}
}
