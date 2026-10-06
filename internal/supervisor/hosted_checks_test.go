package supervisor

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// hostedRollup is goblin cg-wakes's pull request 209 with the given checks and
// review decision, as gh pr list reports it.
func hostedRollup(review string, checks ...string) string {
	return `[{"number":209,"url":"https://github.com/o/r/pull/209","headRefName":"feat/wakes","headRefOid":"3d7072f8aa","reviewDecision":"` + review + `","statusCheckRollup":[` + strings.Join(checks, ",") + `]}]`
}

const (
	testRunning   = `{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":"","detailsUrl":"https://github.com/o/r/actions/runs/5/job/50"}`
	testFailed    = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"FAILURE","completedAt":"2026-09-30T12:30:00Z","detailsUrl":"https://github.com/o/r/actions/runs/5/job/50"}`
	testCancelled = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"CANCELLED","completedAt":"2026-09-30T12:30:00Z","detailsUrl":"https://github.com/o/r/actions/runs/5/job/50"}`
	testPassed    = `{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:30:00Z","detailsUrl":"https://github.com/o/r/actions/runs/5/job/50"}`
	lintRunning   = `{"__typename":"CheckRun","name":"lint","status":"QUEUED","conclusion":"","detailsUrl":"https://github.com/o/r/actions/runs/5/job/51"}`
	lintPassed    = `{"__typename":"CheckRun","name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-09-30T12:03:00Z","detailsUrl":"https://github.com/o/r/actions/runs/5/job/51"}`
	scanFailed    = `{"__typename":"StatusContext","context":"GitGuardian Security Checks","state":"FAILURE","targetUrl":"https://dashboard.gitguardian.com/workspace/1/incidents"}`
	scanPassed    = `{"__typename":"StatusContext","context":"GitGuardian Security Checks","state":"SUCCESS","targetUrl":"https://dashboard.gitguardian.com/workspace/1"}`
)

// cardChecks reads the hosted checks the board's snapshot shows on a task's
// card after the poll at now, as the JSON the board reads.
func cardChecks(t *testing.T, s *Service, id string, now time.Time) map[string]any {
	t.Helper()
	if err := s.checkFleet(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	s.cycle(context.Background(), false)
	snapshot, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(snapshot.Tasks, func(task Task) bool { return task.ID == id })
	if index < 0 {
		t.Fatalf("tasks = %+v, want %s", snapshot.Tasks, id)
	}
	data, err := json.Marshal(snapshot.Tasks[index])
	if err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatal(err)
	}
	checks, _ := card["hosted_checks"].(map[string]any)
	return checks
}

// A live goblin's pull request carries its hosted checks onto its card from
// each CI poll: pending while one runs and none has failed, failed as soon as
// one has, with the failed checks named and the first one's page linked,
// cancelled when they ended with one cancelled, and passed when all passed,
// with the reviewer's approval beside it. A status context's page is its
// target.
func TestHostedChecksReachTheCardOfTheGoblinWhosePullRequestTheyAreOn(t *testing.T) {
	for _, test := range []struct {
		name   string
		review string
		checks []string
		want   map[string]any
	}{
		{"one still running", "", []string{testRunning, lintPassed}, map[string]any{"state": "pending", "head": "3d7072f8aa", "checks": float64(2)}},
		{"one failed while another runs", "REVIEW_REQUIRED", []string{testFailed, lintRunning}, map[string]any{"state": "failed", "head": "3d7072f8aa", "checks": float64(2), "failed": []any{"test"}, "link": "https://github.com/o/r/actions/runs/5/job/50"}},
		{"a status context failed", "", []string{testPassed, scanFailed}, map[string]any{"state": "failed", "head": "3d7072f8aa", "checks": float64(2), "failed": []any{"GitGuardian Security Checks"}, "link": "https://dashboard.gitguardian.com/workspace/1/incidents"}},
		{"one was cancelled", "", []string{testCancelled, lintPassed}, map[string]any{"state": "cancelled", "head": "3d7072f8aa", "checks": float64(2), "failed": []any{"test"}, "link": "https://github.com/o/r/actions/runs/5/job/50"}},
		{"all passed and approved", "APPROVED", []string{testPassed, lintPassed, scanPassed}, map[string]any{"state": "passed", "head": "3d7072f8aa", "checks": float64(3), "approved": true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			s, h := fleetService(t)
			project := t.TempDir()
			liveGoblin(t, h, "cg-wakes", project)
			if err := state.AppendStatus(h.State, "cg-wakes", "done: PR https://github.com/o/r/pull/209"); err != nil {
				t.Fatal(err)
			}
			forge := forgeFor(project, "cg-wakes")
			forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", hostedRollup(test.review, test.checks...)
			s.Options.CI = forge

			// Act
			checks := cardChecks(t, s, "cg-wakes", time.Date(2026, 9, 30, 12, 31, 0, 0, time.UTC))

			// Assert
			delete(checks, "at")
			got, _ := json.Marshal(checks)
			want, _ := json.Marshal(test.want)
			if string(got) != string(want) {
				t.Errorf("the card's hosted checks = %s, want %s", got, want)
			}
		})
	}
}

// A pull request with no checks has nothing to show, and a goblin's card
// shows no other pull request's checks.
func TestHostedChecksShowNothingForAPullRequestWithoutChecksOrAnotherGoblins(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	liveGoblin(t, h, "cg-other", project)
	if err := state.AppendStatus(h.State, "cg-wakes", "done: PR https://github.com/o/r/pull/209"); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "cg-other", "done: PR https://github.com/o/r/pull/210"); err != nil {
		t.Fatal(err)
	}
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", hostedRollup("")
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 31, 0, 0, time.UTC)

	// Act
	none := cardChecks(t, s, "cg-wakes", now)
	other := cardChecks(t, s, "cg-other", now)

	// Assert
	if none != nil || other != nil {
		t.Errorf("hosted checks = %v on the pull request with none, %v on another goblin's card; want neither", none, other)
	}
}

// Once a pull request has not been read for as long as finished checks are
// kept, its record goes with them.
func TestHostedChecksAreForgottenWithTheChecksTheyCameFrom(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	project := t.TempDir()
	liveGoblin(t, h, "cg-wakes", project)
	if err := state.AppendStatus(h.State, "cg-wakes", "done: PR https://github.com/o/r/pull/209"); err != nil {
		t.Fatal(err)
	}
	forge := forgeFor(project, "cg-wakes")
	forge.branch, forge.runs, forge.pulls = "feat/wakes", "[]", hostedRollup("", testPassed)
	s.Options.CI = forge
	now := time.Date(2026, 9, 30, 12, 31, 0, 0, time.UTC)
	if cardChecks(t, s, "cg-wakes", now) == nil {
		t.Fatal("the first poll left no hosted checks on the card")
	}
	forge.pulls = "[]"

	// Act
	kept := cardChecks(t, s, "cg-wakes", now.Add(ciRecordFor-time.Hour))
	forgotten := cardChecks(t, s, "cg-wakes", now.Add(ciRecordFor+time.Hour))

	// Assert
	if kept == nil || forgotten != nil {
		t.Errorf("hosted checks = %v within the record's life, %v after it; want the last reading kept, then gone", kept, forgotten)
	}
}
