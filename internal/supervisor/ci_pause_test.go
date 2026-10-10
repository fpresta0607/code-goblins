package supervisor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestCIPauseResumesOnlyForNewCompletionOfTheAwaitedHead(t *testing.T) {
	for _, testCase := range []struct {
		name, reason, prefix, head string
		isOld, shouldResume        bool
	}{
		{name: "CI completes", reason: "ci", prefix: "pr:", head: strings.Repeat("a", 40), shouldResume: true},
		{name: "deploy completes", reason: "deploy", prefix: "run:", head: strings.Repeat("a", 40), shouldResume: true},
		{name: "wrong head", reason: "ci", prefix: "pr:", head: strings.Repeat("b", 40)},
		{name: "earlier completion", reason: "ci", prefix: "pr:", head: strings.Repeat("a", 40), isOld: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			now := time.Now().UTC()
			url := "https://github.com/owner/repo/pull/42"
			if testCase.prefix == "run:" {
				url = "https://github.com/owner/repo/actions/runs/42"
			}
			pausedGoblin(t, h, "waiting-task", testCase.reason, testCase.prefix+url+"@"+strings.Repeat("a", 40), now.Add(-time.Hour))
			completedAt := now
			if testCase.isOld {
				completedAt = now.Add(-2 * time.Hour)
			}
			if err := writeFleetWakes(h.State, fleetWakes{Schema: fleetWakesSchema, Checks: map[string]reportedChecks{url: {Signature: testCase.head + "|CI=SUCCESS@" + completedAt.Format(time.RFC3339), At: completedAt, ReportedAt: completedAt}}}); err != nil {
				t.Fatal(err)
			}

			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			if testCase.shouldResume {
				calls := awaitDispatch(t, handler.Service, spawner, 1)
				if calls[0][0] != "resume" || calls[0][1] != "waiting-task" {
					t.Fatalf("CI completion dispatched %v", calls)
				}
			} else if calls := spawner.recorded(); len(calls) != 0 {
				t.Fatalf("unrelated completion resumed: %v", calls)
			}
		})
	}
}

func TestAwaitedRunPollReportsOnlyTheExactHeadAndItsCompletedAttempt(t *testing.T) {
	for _, testCase := range []struct {
		name, status, head string
		shouldReport       bool
	}{
		{name: "pending", status: "in_progress", head: strings.Repeat("a", 40)},
		{name: "wrong head", status: "completed", head: strings.Repeat("b", 40)},
		{name: "completed", status: "completed", head: strings.Repeat("a", 40), shouldReport: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, h := fleetService(t)
			now := time.Now().UTC()
			target := "https://github.com/owner/repo/actions/runs/42"
			pausedGoblin(t, h, "waiting-task", "deploy", "run:"+target+"@"+strings.Repeat("a", 40), now.Add(-time.Hour))
			forge := forgeFor(h.Root, "waiting-task")
			forge.jobs = fmt.Sprintf(`{"databaseId":42,"workflowName":"deploy","status":%q,"conclusion":"success","headSha":%q,"url":%q,"attempt":1,"startedAt":"2026-10-02T12:00:00Z","updatedAt":"2026-10-02T12:10:30Z"}`, testCase.status, testCase.head, target)
			service.Options.CI = forge
			watched := fleetWakes{}

			for range 2 {
				if err := service.pollAwaitedRuns(t.Context(), &watched, now); err != nil {
					t.Fatal(err)
				}
			}

			want := 0
			if testCase.shouldReport {
				want = 1
			}
			if got := len(fleetWakeRecords(t, h, "ci")); got != want {
				t.Fatalf("completed wakes=%d, want %d", got, want)
			}
			if testCase.shouldReport && (len(watched.Durations) != 1 || watched.Durations[0].Seconds != 630) {
				t.Fatalf("deploy timing not retained: %+v", watched.Durations)
			}
		})
	}
}

// An awaited run whose read times out under the fleet's load is read again
// on the next poll and reported only once the read failed on three polls in
// a row; a poll that reads it starts the count again.
func TestAnAwaitedRunReadIsReportedOnlyOnTheThirdFailingPollInARow(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	now := time.Now().UTC()
	target := "https://github.com/owner/repo/actions/runs/42"
	pausedGoblin(t, h, "waiting-task", "deploy", "run:"+target+"@"+strings.Repeat("a", 40), now.Add(-time.Hour))
	forge := forgeFor(h.Root, "waiting-task")
	forge.jobs = fmt.Sprintf(`{"databaseId":42,"workflowName":"deploy","status":"in_progress","conclusion":"","headSha":%q,"url":%q,"attempt":1}`, strings.Repeat("a", 40), target)
	service.Options.CI = forge
	watched := fleetWakes{}
	timedOut := `Get "https://api.github.com/repos/owner/repo/actions/runs/42": context deadline exceeded`

	for poll, step := range []struct {
		failure      string
		shouldReport bool
	}{{timedOut, false}, {timedOut, false}, {timedOut, true}, {"", false}, {timedOut, false}} {
		forge.ghFailure = step.failure

		// Act
		err := service.pollAwaitedRuns(t.Context(), &watched, now.Add(time.Duration(poll)*ciPollEvery))

		// Assert
		if (err != nil) != step.shouldReport {
			t.Fatalf("poll %d returned %v, want an error only on the third failing poll in a row", poll+1, err)
		}
	}
}

func TestCIPauseUsesFreshConfirmationWithoutRepeatingTheCompletionWake(t *testing.T) {
	service, h := fleetService(t)
	now := time.Now().UTC()
	head := strings.Repeat("a", 40)
	pr := ghPullRequest{Number: 42, URL: "https://github.com/owner/repo/pull/42", HeadRefOid: head, Checks: []ghCheck{{Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}}
	watched := fleetWakes{}
	if err := reportChecks(h.State, &watched, "waiting-task", pr, false, now.Add(-time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	meta := pausedGoblin(t, h, "waiting-task", "ci", "pr:"+pr.URL+"@"+head, now.Add(-time.Minute))
	condition, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := reportChecks(h.State, &watched, meta.ID, pr, false, now, nil); err != nil {
		t.Fatal(err)
	}
	isReady, err := service.pauseCleared(t.Context(), *condition.Pause, now, &watched)

	if err != nil || !isReady || len(fleetWakeRecords(t, h, "ci")) != 1 {
		t.Fatalf("fresh completion did not clear pause without duplicate wake: ready=%v err=%v", isReady, err)
	}
}
