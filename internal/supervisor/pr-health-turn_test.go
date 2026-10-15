package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// Each time main moved, pr_health told a goblin to merge it into its pull
// request, and the head that merge replaced had run CI for nothing: in
// PrecisionDocs about $27 a month of runs on heads replaced by bringing main
// in, at 3.3 runs per merge (data/ci-actions-spend/report.md). A merge train
// tests its riders on the main they land on, so a goblin's own pull request
// that is only behind asks for nothing, alone too, since one alone rides a
// train of its own once it waited loneRiderWait (#852). A conflict is told at
// once, since no train can take it. Failed checks are ci_finished's to tell,
// never a reason to merge main.
func TestPRHealthAsksForMainOnlyWhenItConflictsOrIsNextToLandAlone(t *testing.T) {
	const failed = `[{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE"}]`
	const mine = "tell cg-health to merge"
	held := fmt.Sprintf(`[{"name":%q}]`, train.HoldLabel)
	for _, test := range []struct {
		name                      string
		mergeable, checks, labels string
		isReportedDone            bool
		// other is a second green pull request its goblin reported done,
		// which rides a train with this one.
		other string
		want  []string
	}{
		{name: "in conflict with its base", mergeable: "CONFLICTING", checks: passedCheck, labels: "[]", isReportedDone: true, want: []string{"conflicts with its base", mine}},
		{name: "its checks failed", mergeable: "MERGEABLE", checks: failed, labels: "[]", isReportedDone: true},
		{name: "it has no checks", mergeable: "MERGEABLE", checks: "[]", labels: "[]"},
		{name: "green and not yet reported done", mergeable: "MERGEABLE", checks: passedCheck, labels: "[]"},
		{name: "green, reported done and held", mergeable: "MERGEABLE", checks: passedCheck, labels: held, isReportedDone: true},
		{name: "green, reported done, with another to ride a train beside it", mergeable: "MERGEABLE", checks: passedCheck, labels: "[]", isReportedDone: true, other: "head-two"},
		{name: "green, reported done and alone, which a train of its own takes", mergeable: "MERGEABLE", checks: passedCheck, labels: "[]", isReportedDone: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, forge, now := healthService(t, true)
			forge.pulls = finishedPull(test.mergeable, "o", test.checks, test.labels)
			forge.comparisons = healthComparison("head-one", 2)
			if test.other != "" {
				forge.pulls = strings.TrimSuffix(forge.pulls, "]") + "," + fmt.Sprintf(`{"number":210,"url":"https://github.com/o/r/pull/210","headRefName":"feat/other","headRefOid":%q,"baseRefName":"main","mergeable":"MERGEABLE","author":{"login":"o"},"labels":[],"statusCheckRollup":%s}]`, test.other, passedCheck)
				forge.comparisons = behindBy(map[int]comparedHead{209: {"head-one", 2}, 210: {test.other, 2}})
				if err := state.AppendStatus(h.State, "cg-health", "done: PR https://github.com/o/r/pull/210"); err != nil {
					t.Fatal(err)
				}
			}
			if test.isReportedDone {
				if err := state.AppendStatus(h.State, "cg-health", "done: PR https://github.com/o/r/pull/209"); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}

			// Assert
			wakes := prWakes(t, h, "pr_health")
			if len(test.want) == 0 {
				if len(wakes) != 0 {
					t.Fatalf("health wakes = %+v, want none", wakes)
				}
				return
			}
			if len(wakes) != 1 {
				t.Fatalf("health wakes = %+v, want one", wakes)
			}
			for _, want := range test.want {
				if !strings.Contains(wakes[0].Detail, want) {
					t.Errorf("wake %q lacks %q", wakes[0].Detail, want)
				}
			}
		})
	}
}
