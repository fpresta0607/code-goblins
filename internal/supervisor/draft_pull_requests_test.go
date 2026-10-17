package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

const goTestSkipped = `{"__typename":"CheckRun","name":"test","workflowName":"go","status":"COMPLETED","conclusion":"SKIPPED","completedAt":"2026-10-14T12:01:00Z"}`

// draftRollup is one pull request of the fake forge, a draft or not.
func draftRollup(head, mergeable string, isDraft bool, checks ...string) string {
	return fmt.Sprintf(`[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":%q,"baseRefName":"main","mergeable":%q,"isDraft":%t,"author":{"login":"o"},"statusCheckRollup":[%s]}]`, head, mergeable, isDraft, strings.Join(checks, ","))
}

// A goblin's pull request stays a draft until its head is the one it means
// to merge, and a draft's workflows skip their jobs, so nothing about its CI
// is news: no ci_finished claims a draft's skipped checks passed, and none
// says no workflow ran for it. Once it is marked ready, a head whose checks
// are still only the skipped ones of its draft run has not been tested, and
// it finishes only when its ready run does.
func TestCIWakesWaitForADraftToBeMarkedReady(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	runsWorkflows(t, project)
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs = "feat/wakes", "[]"
	s.Options.CI = forge
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	// Act and Assert
	forge.pulls = draftRollup("ed99ac3f00", "MERGEABLE", true, scanPassedRun, goTestSkipped)
	for poll := 0; poll < 10; poll++ {
		if woke := pollForge(t, s, h, &now); len(woke) != 0 {
			t.Fatalf("poll %d of a draft raised ci wakes: %+v", poll, woke)
		}
	}
	forge.pulls = draftRollup("ed99ac3f00", "MERGEABLE", false, scanPassedRun, goTestSkipped)
	var skipped []wake.Record
	for poll := 0; poll < 10 && len(skipped) == 0; poll++ {
		skipped = pollForge(t, s, h, &now)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0].Detail, "no workflow ran") || strings.Contains(skipped[0].Detail, "passed") {
		t.Fatalf("a ready head whose only workflow check was skipped = %+v, want one wake saying no workflow ran and none that it passed", skipped)
	}
	forge.pulls = draftRollup("ed99ac3f00", "MERGEABLE", false, scanPassedRun, goTestPassed)
	var finished []wake.Record
	for poll := 0; poll < 5 && len(finished) == 0; poll++ {
		finished = pollForge(t, s, h, &now)
	}
	if len(finished) != 1 || !strings.Contains(finished[0].Detail, "all 2 passed") {
		t.Fatalf("the ready run finished = %+v, want one wake counting both checks once the gap after the last wake passed", finished)
	}
}

// A draft is not yet the head its goblin means to merge, so a draft that is
// only behind its base asks for nothing: main comes in when it is ready. A
// draft that conflicts is told, since it must merge main before it can be.
func TestPRHealthLeavesADraftThatIsOnlyBehindAlone(t *testing.T) {
	for _, test := range []struct {
		name      string
		mergeable string
		want      string
	}{
		{"only behind", "MERGEABLE", ""},
		{"in conflict with its base", "CONFLICTING", "conflicts with its base"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h, forge, now := healthService(t, true)
			forge.pulls = draftRollup("head-one", test.mergeable, true)
			forge.comparisons = healthComparison("head-one", 2)

			// Act
			if err := service.checkFleet(context.Background(), now); err != nil {
				t.Fatal(err)
			}

			// Assert
			wakes := prWakes(t, h, "pr_health")
			if test.want == "" {
				if len(wakes) != 0 {
					t.Fatalf("health wakes = %+v, want none for a draft that is only behind", wakes)
				}
				return
			}
			if len(wakes) != 1 || !strings.Contains(wakes[0].Detail, test.want) {
				t.Fatalf("health wakes = %+v, want one naming %q", wakes, test.want)
			}
		})
	}
}

const draftStatePassed = `{"__typename":"CheckRun","name":"draft state","workflowName":"go","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-10-14T12:00:30Z"}`

// The workflows' draft job reads whether a pull request is a draft now and
// tests nothing (train.DraftStateCheck): on a draft it succeeds while every
// other job is skipped. A head that only a draft's run has seen is no more
// tested for that success: marked ready, it finishes only when its ready run
// does, and the draft job's check is never one of the checks counted (#963).
func TestCIWakesPassOverTheDraftStateCheck(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	runsWorkflows(t, project)
	liveGoblin(t, h, "cg-wakes", project)
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs = "feat/wakes", "[]"
	s.Options.CI = forge
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	// Act
	forge.pulls = draftRollup("ed99ac3f00", "MERGEABLE", false, scanPassedRun, draftStatePassed, goTestSkipped)
	var skipped []wake.Record
	for poll := 0; poll < 10 && len(skipped) == 0; poll++ {
		skipped = pollForge(t, s, h, &now)
	}
	forge.pulls = draftRollup("ed99ac3f00", "MERGEABLE", false, scanPassedRun, draftStatePassed, goTestPassed)
	var finished []wake.Record
	for poll := 0; poll < 5 && len(finished) == 0; poll++ {
		finished = pollForge(t, s, h, &now)
	}

	// Assert
	if len(skipped) != 1 || !strings.Contains(skipped[0].Detail, "no workflow ran") || strings.Contains(skipped[0].Detail, "passed") {
		t.Fatalf("a ready head with only its draft run's checks = %+v, want one wake saying no workflow ran and none that it passed", skipped)
	}
	if len(finished) != 1 || !strings.Contains(finished[0].Detail, "all 2 passed") {
		t.Fatalf("the ready run finished = %+v, want one wake counting the scan and the test, never the draft job's check", finished)
	}
}
