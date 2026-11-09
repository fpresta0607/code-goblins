package train

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// The native no-mistakes-required.yml workflow checks live PR body/head
// compliance, not the combined source tree (issue #2008).
func nativeCheck(t *testing.T, conclusion string) Check {
	t.Helper()
	return Check{Kind: "CheckRun", Name: "PR must be raised via no-mistakes", WorkflowName: "Require no-mistakes", Status: "COMPLETED", Conclusion: conclusion, DetailsURL: "https://github.com/o/r/actions/runs/900/job/9001",
		DatabaseID: 9001, NodeID: "CR9001", SuiteID: 900, SuiteNodeID: "CS900", AppID: githubActions,
		Repository: "o/r", HeadSHA: fmt.Sprintf("%040d", 1), RunID: 900, RunNodeID: "WFR900", RunAttempt: 1,
		Event: "pull_request", WorkflowID: 99, WorkflowNodeID: "W99", WorkflowPath: nativeAttestationPath, WorkflowRepository: "o/r"}
}

func TestRidersRequireOneSuccessfulNativeAttestation(t *testing.T) {
	for _, conclusion := range []string{"SKIPPED", "NEUTRAL", "FAILURE", ""} {
		t.Run(conclusion, func(t *testing.T) {
			pr := greenPull(1)
			pr.Checks = append(pr.Checks, nativeCheck(t, conclusion))
			riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
			if len(riders) != 0 {
				t.Fatalf("native attestation %q admitted a rider", conclusion)
			}
		})
	}
	pr := greenPull(1)
	pr.Checks = append(pr.Checks, nativeCheck(t, "SUCCESS"), nativeCheck(t, "SUCCESS"))
	if riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil); len(riders) != 0 {
		t.Fatal("ambiguous native attestations admitted a rider")
	}
}

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

func capturedNativePull(t *testing.T) PullRequest {
	t.Helper()
	data, err := os.ReadFile("testdata/native-attestation-pr.json")
	if err != nil {
		t.Fatal(err)
	}
	var graph GraphPullRequest
	if err := json.Unmarshal(data, &graph); err != nil {
		t.Fatal(err)
	}
	return graph.PullRequest()
}

func TestRidersAdmitDistinctSuccessfulNativeAttestations(t *testing.T) {
	// Arrange: the captured PR122 rollup has 23 successes, two distinct native runs.
	pr := capturedNativePull(t)
	before := slices.Clone(pr.Checks)

	// Act
	riders, left := Riders([]PullRequest{pr}, "main", pr.Author.Login, goblinsFor(pr), nil)

	// Assert
	if len(riders) != 1 || len(left) != 0 {
		t.Fatalf("riders %v, left %v: distinct native successes must admit", riders, left)
	}
	if len(pr.Checks) != 23 || !slices.Equal(pr.Checks, before) {
		t.Fatal("admission changed the original check values or multiplicity")
	}
	witness := riders[0].NativeAttestation
	if witness == nil || witness.DetailsURL != "https://github.com/fpresta0607/no-mistakes/actions/runs/42236780731/job/127956358474" {
		t.Fatalf("witness %+v, want the immutable first check", witness)
	}
}

func TestRidersRequireEveryNativeAttestationToBeProvenAndSuccessful(t *testing.T) {
	for name, change := range map[string]func(*Check){
		"failed sibling":              func(c *Check) { c.Conclusion = "FAILURE" },
		"pending sibling":             func(c *Check) { c.Status, c.Conclusion = "IN_PROGRESS", "" },
		"skipped sibling":             func(c *Check) { c.Conclusion = "SKIPPED" },
		"neutral sibling":             func(c *Check) { c.Conclusion = "NEUTRAL" },
		"cancelled sibling":           func(c *Check) { c.Conclusion = "CANCELLED" },
		"status context":              func(c *Check) { c.Kind, c.Context = "StatusContext", c.Name },
		"missing check ID":            func(c *Check) { c.DatabaseID = 0 },
		"missing check node":          func(c *Check) { c.NodeID = "" },
		"missing suite":               func(c *Check) { c.SuiteID = 0 },
		"missing suite node":          func(c *Check) { c.SuiteNodeID = "" },
		"foreign app":                 func(c *Check) { c.AppID++ },
		"missing run":                 func(c *Check) { c.RunID = 0 },
		"missing run node":            func(c *Check) { c.RunNodeID = "" },
		"missing attempt":             func(c *Check) { c.RunAttempt = 0 },
		"foreign head":                func(c *Check) { c.HeadSHA = strings.Repeat("a", 40) },
		"foreign repository":          func(c *Check) { c.Repository = "foreign/repo" },
		"foreign workflow repository": func(c *Check) { c.WorkflowRepository = "foreign/repo" },
		"foreign workflow":            func(c *Check) { c.WorkflowID++ },
		"missing workflow node":       func(c *Check) { c.WorkflowNodeID = "" },
		"wrong workflow name":         func(c *Check) { c.WorkflowName = "test" },
		"wrong file":                  func(c *Check) { c.WorkflowPath = ".github/workflows/test.yml" },
		"wrong event":                 func(c *Check) { c.Event = "push" },
		"wrong run link":              func(c *Check) { c.DetailsURL = strings.Replace(c.DetailsURL, "42237139244", "42236780731", 1) },
		"duplicate check ID":          func(c *Check) { c.DatabaseID = 127956358474 },
		"duplicate suite":             func(c *Check) { c.SuiteID = 114442038361 },
		"same run and attempt":        func(c *Check) { c.RunID = 42236780731 },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			pr := capturedNativePull(t)
			admitted, _ := nativeRiderAttestation(pr, nil)
			change(&pr.Checks[3])
			before := slices.Clone(pr.Checks)
			// Act / Assert: a valid first witness cannot hide an invalid sibling.
			for _, witness := range []*Check{nil, admitted} {
				if proof, present := nativeRiderAttestation(pr, witness); proof != nil || !present {
					t.Fatalf("accepted invalid native group: %+v, present %v", proof, present)
				}
			}
			if !slices.Equal(pr.Checks, before) {
				t.Fatal("validation rewrote its input")
			}
		})
	}
}

func TestRidersNativeAttestationWitnessIsStableByImmutableIdentity(t *testing.T) {
	// Arrange
	pr := capturedNativePull(t)
	admitted, _ := nativeRiderAttestation(pr, nil)
	if admitted == nil {
		t.Fatal("missing admitted witness")
	}
	for _, variant := range []string{"reordered", "timestamps", "new successful sibling", "replacement", "changed witness", "legacy witness", "identical duplicate"} {
		t.Run(variant, func(t *testing.T) {
			current := pr
			current.Checks = slices.Clone(pr.Checks)
			witness := *admitted
			shouldPass := true
			switch variant {
			case "reordered":
				slices.Reverse(current.Checks)
			case "timestamps":
				current.Checks[3].StartedAt, current.Checks[3].CompletedAt = "2000-01-01T00:00:00Z", "2000-01-01T00:00:01Z"
			case "new successful sibling":
				sibling := current.Checks[3]
				sibling.DatabaseID++
				sibling.SuiteID++
				sibling.RunID++
				sibling.NodeID, sibling.SuiteNodeID, sibling.RunNodeID = "new-check", "new-suite", "new-run"
				sibling.DetailsURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", sibling.Repository, sibling.RunID, sibling.DatabaseID)
				current.Checks = append(current.Checks, sibling)
			case "replacement":
				current.Checks = append(current.Checks[:2:2], current.Checks[3:]...)
				shouldPass = false
			case "changed witness":
				current.Checks[2].CompletedAt = "2030-01-01T00:00:00Z"
				shouldPass = false
			case "legacy witness":
				witness.DatabaseID, witness.NodeID = 0, ""
				shouldPass = false
			case "identical duplicate":
				current.Checks = append(current.Checks, current.Checks[2])
				shouldPass = false
			}
			// Act
			proof, present := nativeRiderAttestation(current, &witness)
			// Assert
			if !present || (proof != nil) != shouldPass || proof != nil && *proof != *admitted {
				t.Fatalf("proof %+v, want admitted %+v, should pass %v", proof, admitted, shouldPass)
			}
			if variant == "reordered" || variant == "timestamps" {
				fresh, _ := nativeRiderAttestation(current, nil)
				if fresh == nil || fresh.DatabaseID != admitted.DatabaseID {
					t.Fatal("selection depended on order or time")
				}
			}
		})
	}
}
