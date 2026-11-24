package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/workreview"
)

// widgetsResolver resolves every project to the checkout of acme/widgets.
func widgetsResolver(checkout string) workreview.Resolver {
	return func(string) (string, string) { return "acme/widgets", checkout }
}

func TestReviewWorkRaisesOneWakeForTheFindingsNotReportedBefore(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	reads := 0
	isUnread := false
	service.Options.WorkReview = &WorkReview{
		Resolve: widgetsResolver(t.TempDir()),
		States: func(_ context.Context, _ string, numbers []int) (map[int]string, error) {
			reads++
			if isUnread {
				return nil, errors.New("GitHub did not answer")
			}
			states := map[int]string{}
			for _, number := range numbers {
				states[number] = "closed"
			}
			return states, nil
		},
	}
	const rowA, rowB, rowC = "- [ ] row-a - Ship a, issue 11 (repo: widgets)\n", "- [ ] row-b - Ship b, issue 12 (repo: widgets)\n", "- [ ] row-c - Ship c, issue 13 (repo: widgets)\n"
	start := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		name       string
		rows       string
		after      time.Duration
		isRenewed  bool
		isUnread   bool
		wantReads  int
		wantWakes  int
		wantNamed  string
		wantAbsent string
	}{
		{name: "a first run raises one wake naming its finding", rows: rowA, wantReads: 1, wantWakes: 1, wantNamed: "row-a"},
		{name: "the same finding raises none", rows: rowA, after: 25 * time.Hour, wantReads: 1, wantWakes: 1},
		{name: "one more finding raises one wake naming only it", rows: rowA + rowB, after: 50 * time.Hour, wantReads: 1, wantWakes: 2, wantNamed: "row-b", wantAbsent: "row-a"},
		{name: "a run within 24 hours reads nothing", rows: rowA + rowB + rowC, after: 60 * time.Hour, wantWakes: 2},
		{name: "a renewal makes the run due", rows: rowA + rowB + rowC, after: 61 * time.Hour, isRenewed: true, wantReads: 1, wantWakes: 3, wantNamed: "row-c", wantAbsent: "row-b"},
		{name: "a finding that went away raises none", rows: rowA, after: 86 * time.Hour, wantReads: 1, wantWakes: 3},
		{name: "a finding that comes back is new again", rows: rowA + rowB, after: 111 * time.Hour, wantReads: 1, wantWakes: 4, wantNamed: "row-b", wantAbsent: "row-a"},
		{name: "a run that cannot read raises none", rows: rowA + rowB, after: 136 * time.Hour, isUnread: true, wantReads: 1, wantWakes: 4},
		{name: "the next run that reads raises none for what was reported", rows: rowA + rowB, after: 161 * time.Hour, wantReads: 1, wantWakes: 4},
	}
	for _, step := range steps {
		// Act
		writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n"+step.rows)
		before := reads
		isUnread = step.isUnread
		if err := service.reviewWork(t.Context(), start.Add(step.after), step.isRenewed); (err != nil) != step.isUnread {
			t.Fatalf("%s: error %v", step.name, err)
		}

		// Assert
		var wakes []string
		for _, record := range fleetWakeRecords(t, h, "notify") {
			if record.Key == "work-review" {
				wakes = append(wakes, record.Detail)
			}
		}
		if reads-before != step.wantReads || len(wakes) != step.wantWakes {
			t.Fatalf("%s: %d reads and %d wakes, want %d and %d", step.name, reads-before, len(wakes), step.wantReads, step.wantWakes)
		}
		if step.wantNamed == "" {
			continue
		}
		newest := wakes[len(wakes)-1]
		if !strings.HasPrefix(newest, "work review: 1 new finding\n") || !strings.Contains(newest, step.wantNamed) || (step.wantAbsent != "" && strings.Contains(newest, step.wantAbsent)) || !strings.HasSuffix(newest, "Run goblins backlog review for the evidence.") {
			t.Fatalf("%s: the wake is %q, want one finding naming %s and not %s", step.name, newest, step.wantNamed, step.wantAbsent)
		}
	}
}
