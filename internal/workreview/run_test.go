package workreview

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// fixtureStates is what GitHub says of the numbers the fixture home names.
var fixtureStates = map[string]map[int]string{
	"acme/widgets": {11: "closed", 30: "open", 40: "open", 41: "open", 50: "open", 60: "open"},
	"acme/gadgets": {8: "open"},
}

func TestRunFindsEachKindWithOneReadForEachRepository(t *testing.T) {
	// Arrange
	h, checkout := fixtureHome(t)
	reads := map[string]int{}
	read := func(_ context.Context, repository string, numbers []int) (map[int]string, error) {
		reads[repository]++
		states := map[int]string{}
		for _, number := range numbers {
			states[number] = fixtureStates[repository][number]
		}
		return states, nil
	}

	// Act
	report, err := Run(context.Background(), h, fixtureResolver(checkout), read, time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 7*24*time.Hour)

	// Assert
	if err != nil || len(report.Problems) != 0 {
		t.Fatalf("Run: problems %q, err %v", report.Problems, err)
	}
	var found []string
	for _, finding := range report.Findings {
		found = append(found, finding.Key())
	}
	want := []string{
		"done already:queued-done",
		"overtaken:his-step",
		"fold:fold-a,fold-b",
		"stale:paused-task",
		"stale:parked-old",
	}
	if !slices.Equal(found, want) {
		t.Errorf("findings = %q, want %q", found, want)
	}
	if wantReads := map[string]int{"acme/widgets": 1, "acme/gadgets": 1}; !maps.Equal(reads, wantReads) {
		t.Errorf("reads = %v, want %v", reads, wantReads)
	}
}

func TestRunRecordsAFailedReadAsAProblemAndSettlesNothingByIt(t *testing.T) {
	// Arrange
	h, checkout := fixtureHome(t)
	read := func(context.Context, string, []int) (map[int]string, error) {
		return nil, errors.New("GitHub is unreachable")
	}

	// Act
	report, err := Run(context.Background(), h, fixtureResolver(checkout), read, time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 7*24*time.Hour)

	// Assert
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Problems) != 2 || !strings.Contains(report.Problems[0], "acme/gadgets") || !strings.Contains(report.Problems[1], "acme/widgets") {
		t.Errorf("problems = %q, want one for each repository, in order", report.Problems)
	}
	for _, finding := range report.Findings {
		if finding.Kind == KindDone {
			t.Errorf("finding %s settled an issue that was never read", finding.Key())
		}
	}
}

func TestRunThatCannotReadARepositoryFindsNothingOfItsWorkAndTheNextReadFindsItAgain(t *testing.T) {
	// Arrange: a paused task whose issue is closed and whose last activity is
	// ten days old, and three queued rows that fold together, a and b by an
	// open issue and b and c by a path.
	root := t.TempDir()
	h := home.Home{Root: root, Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	checkout := filepath.Join(root, "widgets")
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	brief := filepath.Join(h.State, "tasktmp", "done-task", "brief.md")
	writeFixture(t, brief, "# Brief done-task\n\n## Task\n\nShip https://github.com/acme/widgets/issues/12.\n", now.Add(-10*24*time.Hour))
	if err := state.WriteMeta(state.TaskMetaPath(h.State, "done-task"), map[string]string{"project": checkout, "brief": brief, "spawn_gen": "g1", "backend": "native"}); err != nil {
		t.Fatal(err)
	}
	pause, err := state.NewPauseCondition("memory", "", now.Add(-10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: "done-task", Generation: "g1", Operation: "pause-1", Action: "pause", Phase: "paused", Started: now.Add(-10 * 24 * time.Hour), Pause: &pause}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(h.Data, "fold-b", "brief.md"), "# Brief fold-b\n\n## Task\n\nSort internal/list.go.\n", time.Time{})
	writeFixture(t, filepath.Join(h.Data, "fold-c", "brief.md"), "# Brief fold-c\n\n## Task\n\nSort internal/list.go again and fix internal/sort.go.\n", time.Time{})
	// fold-d names no issue and links only to fold-c, so a failed read leaves
	// fold-c and fold-d out only through the fold closure.
	writeFixture(t, filepath.Join(h.Data, "fold-d", "brief.md"), "# Brief fold-d\n\n## Task\n\nSpeed up internal/sort.go.\n", time.Time{})
	writeFixture(t, filepath.Join(h.Data, "backlog.md"), "# Backlog\n\n## Queued\n- [ ] fold-a - Sort the list, issue 11 (repo: widgets)\n- [ ] fold-b - Sort the list, issue 11 (repo: widgets)\n- [ ] fold-c - Sort the list once more (repo: widgets)\n- [ ] fold-d - Sort faster (repo: widgets)\n", time.Time{})
	isUnread := false
	read := func(_ context.Context, _ string, numbers []int) (map[int]string, error) {
		if isUnread {
			return nil, errors.New("GitHub is unreachable")
		}
		states := map[int]string{}
		for _, number := range numbers {
			states[number] = map[int]string{11: "open", 12: "closed"}[number]
		}
		return states, nil
	}
	steps := []struct {
		name         string
		isUnread     bool
		wantFindings []string
		wantProblems int
	}{
		{name: "a clean read", wantFindings: []string{"done already:done-task", "fold:fold-a,fold-b,fold-c,fold-d"}},
		{name: "a read that fails", isUnread: true, wantProblems: 1},
		{name: "the next clean read", wantFindings: []string{"done already:done-task", "fold:fold-a,fold-b,fold-c,fold-d"}},
	}
	for _, step := range steps {
		// Act
		isUnread = step.isUnread
		report, err := Run(context.Background(), h, fixtureResolver(checkout), read, now, 7*24*time.Hour)

		// Assert
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		var found []string
		for _, finding := range report.Findings {
			found = append(found, finding.Key())
		}
		if !slices.Equal(found, step.wantFindings) || len(report.Problems) != step.wantProblems {
			t.Fatalf("%s: findings %q and problems %q, want %q and %d problems", step.name, found, report.Problems, step.wantFindings, step.wantProblems)
		}
	}
}

func TestFullyReadLeavesEverythingOutForADroppedItemItCannotPlace(t *testing.T) {
	// Arrange
	items := []Item{{ID: "a", Repository: "acme/widgets", Project: "widgets"}, {ID: "b", Project: "gadgets"}}

	// Act
	kept := fullyRead(items, []Item{{ID: "unplaced", Source: SourcePaused}}, map[string]bool{})

	// Assert
	if len(kept) != 0 {
		t.Errorf("kept %+v, want none, since the dropped item could fold with any", kept)
	}
}

func TestRunThatCannotReadAnItemFindsNothingThatCouldFoldWithIt(t *testing.T) {
	// Arrange: three queued rows of widgets that fold by issue 11, fold-b's
	// brief unreadable in the second run, and a row of gadgets alone.
	root := t.TempDir()
	h := home.Home{Root: root, Data: filepath.Join(root, "data"), State: filepath.Join(root, "state")}
	checkout := filepath.Join(root, "widgets")
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	writeFixture(t, filepath.Join(h.Data, "backlog.md"), "# Backlog\n\n## Queued\n- [ ] fold-a - Sort the list, issue 11 (repo: widgets)\n- [ ] fold-b - Sort the list, issue 11 (repo: widgets)\n- [ ] fold-c - Sort the list, issue 11 (repo: widgets)\n- [ ] gadgets-done - Ship acme/gadgets#8 (repo: gadgets)\n", time.Time{})
	read := func(_ context.Context, _ string, numbers []int) (map[int]string, error) {
		states := map[int]string{}
		for _, number := range numbers {
			states[number] = map[int]string{11: "open", 8: "closed"}[number]
		}
		return states, nil
	}
	unreadBrief := filepath.Join(h.Data, "fold-b", "brief.md")
	steps := []struct {
		name          string
		isBriefUnread bool
		wantFindings  []string
		wantProblems  int
	}{
		{name: "a clean read", wantFindings: []string{"done already:gadgets-done", "fold:fold-a,fold-b,fold-c"}},
		{name: "a read that cannot read fold-b", isBriefUnread: true, wantFindings: []string{"done already:gadgets-done"}, wantProblems: 1},
		{name: "the next clean read", wantFindings: []string{"done already:gadgets-done", "fold:fold-a,fold-b,fold-c"}},
	}
	for _, step := range steps {
		if err := os.RemoveAll(unreadBrief); err != nil {
			t.Fatal(err)
		}
		if step.isBriefUnread {
			// A brief that is a folder cannot be read as a file.
			if err := os.MkdirAll(unreadBrief, 0o700); err != nil {
				t.Fatal(err)
			}
		}

		// Act
		report, err := Run(context.Background(), h, fixtureResolver(checkout), read, now, 7*24*time.Hour)

		// Assert
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		var found []string
		for _, finding := range report.Findings {
			found = append(found, finding.Key())
		}
		if !slices.Equal(found, step.wantFindings) || len(report.Problems) != step.wantProblems {
			t.Fatalf("%s: findings %q and problems %q, want %q and %d problems", step.name, found, report.Problems, step.wantFindings, step.wantProblems)
		}
	}
}
