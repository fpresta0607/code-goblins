package train

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func greenPull(number int) PullRequest {
	pr := PullRequest{
		Number: number, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), HeadRefName: fmt.Sprintf("feat/%d", number),
		HeadRefOid: fmt.Sprintf("%040d", number), BaseRefName: "main", Mergeable: "MERGEABLE",
		Checks: []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}, {Kind: "StatusContext", Context: "scan", State: "SUCCESS"}},
	}
	pr.Author.Login = fleetAccount
	return pr
}

func TestRidersLeaveOutWhatMustNotRide(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		change func(*PullRequest)
		why    string
	}{
		"a draft":                    {func(pr *PullRequest) { pr.IsDraft = true }, "it is a draft"},
		"a held pull request":        {func(pr *PullRequest) { pr.Labels = []Label{{Name: "Hold"}} }, "it is held"},
		"one from another fork":      {func(pr *PullRequest) { pr.IsCrossRepository = true }, "another repository"},
		"one in conflict with main":  {func(pr *PullRequest) { pr.Mergeable = "CONFLICTING" }, "it conflicts with main"},
		"one with changes requested": {func(pr *PullRequest) { pr.ReviewDecision = "CHANGES_REQUESTED" }, "it waits on a review"},
		"one with no checks":         {func(pr *PullRequest) { pr.Checks = nil }, "it has no checks"},
		"one whose every check was skipped": {func(pr *PullRequest) {
			pr.Checks = []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SKIPPED"}, {Kind: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "NEUTRAL"}}
		}, "no check ran on its head"},
		"one whose checks run":        {func(pr *PullRequest) { pr.Checks[1].State = "PENDING" }, "still running"},
		"one whose checks failed":     {func(pr *PullRequest) { pr.Checks[0].Conclusion = "FAILURE" }, "its checks failed"},
		"one no goblin reported done": {func(pr *PullRequest) { pr.URL += "0" }, "no goblin reported it done"},
		"a teammate's pull request":   {func(pr *PullRequest) { pr.Author.Login = "teammate" }, "opened by teammate, not by fleet"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pr := greenPull(1)
			goblins := goblinsFor(pr)
			test.change(&pr)

			// Act
			riders, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblins, nil)

			// Assert
			if len(riders) != 0 || len(left) != 1 || !strings.Contains(left[0], test.why) {
				t.Fatalf("riders %v, left %q, want it left out because %s", riders, left, test.why)
			}
		})
	}
}

func TestRidersSkipTrainsAndOtherBasesWithoutComment(t *testing.T) {
	t.Parallel()
	// Arrange
	train := greenPull(1)
	train.HeadRefName = BranchPrefix + "20261007-160000"
	other := greenPull(2)
	other.BaseRefName = "release"

	// Act
	riders, left := Riders([]PullRequest{train, other}, "main", fleetAccount, goblinsFor(train, other), nil)

	// Assert
	if len(riders) != 0 || len(left) != 0 {
		t.Fatalf("riders %v, left %q, want neither listed", riders, left)
	}
}

func TestRidersQueueInTheOrderGoblinsReportedDone(t *testing.T) {
	t.Parallel()
	// Arrange
	early, late, unordered := greenPull(9), greenPull(3), greenPull(5)
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	goblins := []Goblin{
		{Task: "late", Done: map[string]time.Time{late.URL: at.Add(time.Hour)}},
		{Task: "early", Done: map[string]time.Time{early.URL: at}},
		{Task: "unordered", Done: map[string]time.Time{unordered.URL: at.Add(time.Hour)}},
	}

	// Act
	riders, _ := Riders([]PullRequest{late, unordered, early}, "main", fleetAccount, goblins, nil)

	// Assert
	var order []string
	for _, car := range riders {
		order = append(order, fmt.Sprintf("%s#%d", car.Task, car.Number))
	}
	if !slices.Equal(order, []string{"early#9", "late#3", "unordered#5"}) {
		t.Fatalf("order = %v, want the first done first and a tie by number", order)
	}
	if riders[0].Head != early.HeadRefOid || riders[0].State != CarRiding || riders[0].Branch != early.HeadRefName {
		t.Fatalf("car = %+v, want it riding at its listed head", riders[0])
	}
}

// A car keeps the name and title of the goblin that reported it done, so the
// board and the train pull request name it after the goblin has gone.
func TestRidersCarryTheGoblinsNameAndTitle(t *testing.T) {
	t.Parallel()
	// Arrange
	pr := greenPull(7)
	goblins := []Goblin{{Task: "cg-x", Name: "Jerry", Title: "Code Designer", Done: map[string]time.Time{pr.URL: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}}}

	// Act
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblins, nil)

	// Assert
	if len(riders) != 1 || riders[0].Task != "cg-x" || riders[0].Goblin != "Jerry" || riders[0].GoblinTitle != "Code Designer" {
		t.Fatalf("riders = %+v, want #7 carried for Jerry the Code Designer", riders)
	}
}

func TestRidersKeepACulpritOffUntilItsHeadChanges(t *testing.T) {
	t.Parallel()
	// Arrange
	pr := greenPull(4)
	past := []Train{{ID: "r-20261007-150000", Cars: []Car{{URL: pr.URL, Head: pr.HeadRefOid, State: CarCulprit}}}}

	// Act
	same, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), past)
	pr.HeadRefOid = fmt.Sprintf("%040d", 44)
	fixed, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), past)

	// Assert
	if len(same) != 0 || len(left) != 1 || !strings.Contains(left[0], "r-20261007-150000") {
		t.Fatalf("same head: riders %v, left %q, want it kept off", same, left)
	}
	if len(fixed) != 1 {
		t.Fatalf("new head: riders %v, want it riding again", fixed)
	}
}

// A done report made before its goblin's terminal last started, as before a
// pause, stands for the head it was made of: the pull request rides while
// every check of that head had finished by the time of the report. A report
// made since stands whatever came after, and so does one whose goblin's
// terminal has not started again since.
func TestRidersHoldAReportMadeBeforeARelaunchToTheHeadItWasMadeOf(t *testing.T) {
	t.Parallel()
	reported := time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	stamp := func(after time.Duration) string { return reported.Add(after).Format(time.RFC3339) }
	for name, test := range map[string]struct {
		relaunched time.Time
		checks     []Check
		isRider    bool
	}{
		"its checks finished before the report": {reported.Add(time.Hour), []Check{
			{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(-time.Minute)},
			{Kind: "StatusContext", Context: "scan", State: "SUCCESS", StartedAt: stamp(-2 * time.Minute)},
		}, true},
		"a check finished in the second of the report": {reported.Add(time.Hour), []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(0)}}, true},
		"a check finished after the report": {reported.Add(time.Hour), []Check{
			{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(-time.Minute)},
			{Kind: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(time.Minute)},
		}, false},
		"a commit status set after the report":              {reported.Add(time.Hour), []Check{{Kind: "StatusContext", Context: "scan", State: "SUCCESS", StartedAt: stamp(time.Minute)}}, false},
		"a check whose time GitHub does not give":           {reported.Add(time.Hour), []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}, false},
		"a check finished after a report made since":        {reported.Add(-time.Hour), []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(time.Minute)}}, true},
		"a relaunch in the second of the report":            {reported.Add(500 * time.Millisecond), []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(time.Minute)}}, true},
		"a goblin whose terminal start is not known at all": {time.Time{}, []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: stamp(time.Minute)}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pr := greenPull(1)
			pr.Checks = test.checks
			goblins := []Goblin{{Task: "g1", Done: map[string]time.Time{pr.URL: reported}, Reported: map[string]time.Time{pr.URL: reported}, Relaunched: test.relaunched}}

			// Act
			riders, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblins, nil)

			// Assert
			if isRider := len(riders) == 1; isRider != test.isRider {
				t.Fatalf("riders %v, left %q, want it to ride: %v", riders, left, test.isRider)
			}
			if !test.isRider && (len(left) != 1 || !strings.Contains(left[0], "rides once reported done again")) {
				t.Errorf("left %q, want it to say the pull request rides once reported done again", left)
			}
		})
	}
}

func TestARateLimitedDeployCountsAsNotRunNeverAsFailed(t *testing.T) {
	t.Parallel()
	passed := Check{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}
	failed := Check{Kind: "CheckRun", Name: "lint", Status: "COMPLETED", Conclusion: "FAILURE"}
	skipped := Check{Kind: "CheckRun", Name: "e2e", Status: "COMPLETED", Conclusion: "SKIPPED"}
	// The first deploy is the Vercel entry gh returned live for a head the host
	// rate limited, but for the team's name: no description, only the target
	// of its upgrade page for that limit (#1194). The second failed to build.
	var deploys Checks
	if err := json.Unmarshal([]byte(`[{"__typename":"StatusContext","context":"Vercel","startedAt":"2026-10-19T05:10:00Z","state":"FAILURE","targetUrl":"https://vercel.com/acme-projects?upgradeToPro=build-rate-limit"},{"__typename":"StatusContext","context":"Vercel","startedAt":"2026-10-19T05:10:00Z","state":"FAILURE","targetUrl":"https://vercel.com/acme-projects/app/9xYzBuildFailed"}]`), &deploys); err != nil {
		t.Fatal(err)
	}
	limited, broken := deploys[0], deploys[1]
	for name, test := range map[string]struct {
		checks  []Check
		outcome string
		left    string
		note    string
	}{
		"a rate-limited deploy alone among passing checks rides": {[]Check{passed, limited}, "passed", "", "the host rate limited the deploy of Vercel"},
		"a rate-limited deploy beside a real failure does not":   {[]Check{passed, failed, limited}, "failed", "its checks failed", ""},
		"a failed deploy with another target does not":           {[]Check{passed, broken}, "failed", "its checks failed", ""},
		"only rate-limited and skipped checks do not":            {[]Check{skipped, limited}, "none", "no check ran on its head: every one was skipped, and the host rate limited the deploy of Vercel", ""},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pr := greenPull(1)
			pr.Checks = test.checks

			// Act
			outcome, _ := checksOutcome(pr.Checks)
			riders, left := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)

			// Assert
			if outcome != test.outcome {
				t.Errorf("checksOutcome = %q, want %q", outcome, test.outcome)
			}
			if test.left != "" {
				if len(riders) != 0 || len(left) != 1 || !strings.Contains(left[0], test.left) {
					t.Fatalf("riders %v, left %q, want it left out because %s", riders, left, test.left)
				}
				return
			}
			if len(riders) != 1 || len(left) != 0 || !strings.Contains(riders[0].Note, test.note) {
				t.Fatalf("riders %+v, left %q, want it riding with a note that says %s", riders, left, test.note)
			}
		})
	}
}
