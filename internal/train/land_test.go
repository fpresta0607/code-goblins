package train

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNativePRAttestationFailureDoesNotFailCombinedSourceCI(t *testing.T) {
	t.Parallel()
	s, gh, engine, started, _ := nativeTrain(t)

	// Act
	landed := s.step(engine, started.ID)

	// Assert: real CI passed, the failed attestation was never rerun or called
	// a passing test, and the exact tested commit landed both rider heads.
	if landed.State != StateLanded || len(gh.merged) != 2 || len(gh.rerunCalls()) != 0 {
		t.Fatalf("state %s, merged %v, retries %v", landed.State, gh.merged, gh.rerunCalls())
	}
	if checks := landed.History[0].Passed; len(checks) != 1 || checks[0].Name != "test" || checks[0].Conclusion != "SUCCESS" {
		t.Fatalf("source CI proof includes a failed PR check: %+v", checks)
	}
	if excluded := landed.History[0].PRChecks; len(excluded) != 1 || excluded[0].Conclusion != "FAILURE" {
		t.Fatalf("PR-only failure was not preserved separately: %+v", excluded)
	}
	if s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatal("landed tree differs from the tested train")
	}
}

func nativeTrain(t *testing.T) (*scratch, *fakeGitHub, Engine, Train, []PullRequest) {
	t.Helper()
	// Arrange: two independently attested rider heads and the native PR-only
	// check failing on a synthetic train with otherwise green source CI.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	gh.readiness = "UNSTABLE"
	gh.nativeRuns = map[string]map[string]any{}
	gh.nativeWorkflows = map[string]map[string]any{"99": {"id": 99, "node_id": "W99", "name": "Require no-mistakes", "path": nativeAttestationPath}}
	var pulls []PullRequest
	for _, number := range []int{11, 12} {
		branch := fmt.Sprintf("feat/%d", number)
		pr := gh.open(number, branch, s.branch(branch, fmt.Sprintf("%d.txt", number), "change\n"))
		for _, offset := range []int{0, 1000} {
			check := nativeCheck(t, "SUCCESS")
			check.HeadSHA = pr.HeadRefOid
			check.RunID, check.SuiteID, check.DatabaseID = int64(900+number+offset), int64(900+number+offset), int64(9000+number+offset)
			check.RunNodeID, check.SuiteNodeID, check.NodeID = fmt.Sprint("WFR", check.RunID), fmt.Sprint("CS", check.SuiteID), fmt.Sprint("CR", check.DatabaseID)
			check.DetailsURL = fmt.Sprintf("https://github.com/o/r/actions/runs/%d/job/%d", check.RunID, check.DatabaseID)
			pr.Checks = append(pr.Checks, check)
			gh.nativeRuns[fmt.Sprint(check.RunID)] = nativeRunResponse(check)
		}
		gh.pulls[pr.URL].checks = pr.Checks
		pulls = append(pulls, pr)
	}
	riders, _ := Riders(pulls, "main", fleetAccount, goblinsFor(pulls...), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	failure := nativeCheck(t, "FAILURE")
	failure.HeadSHA = started.Head
	gh.workflowOf(started.Head).extra = []Check{failure}
	gh.nativeRuns["900"] = nativeRunResponse(failure)
	return s, gh, engine, started, pulls
}

func nativeRunResponse(check Check) map[string]any {
	return map[string]any{
		"id": check.RunID, "node_id": check.RunNodeID,
		"check_suite_id": check.SuiteID, "check_suite_node_id": check.SuiteNodeID,
		"workflow_id": check.WorkflowID, "run_attempt": check.RunAttempt,
		"name": "PR body compliance - dynamic event title", "display_title": "Separate display title",
		"path": check.WorkflowPath, "event": check.Event, "head_sha": check.HeadSHA,
		"repository": map[string]any{"full_name": check.Repository},
	}
}

func TestNativeAttestationExceptionRetainsOtherCIAndExactProvenance(t *testing.T) {
	t.Parallel()
	_, gh, engine, started, _ := nativeTrain(t)
	w := gh.workflowOf(started.Head)
	original := w.extra[0]
	for _, test := range []struct {
		name      string
		change    func()
		outcome   string
		wantError bool
	}{
		{"real source failure", func() { w.conclusion = "failure" }, "failed", false},
		{"source pending", func() { gh.pending = 1 }, "pending", false},
		{"no source check ran", func() { gh.skipped = true }, "none", false},
		{"unknown failure", func() { w.extra = append(w.extra, Check{Kind: "StatusContext", Context: "security", State: "FAILURE"}) }, "failed", false},
		{"wrong workflow", func() { w.extra[0].WorkflowName = "Other workflow" }, "failed", false},
		{"foreign repository", func() { w.extra[0].DetailsURL = "https://github.com/other/repo/actions/runs/900/job/1" }, "failed", false},
		{"duplicate native check", func() { w.extra = append(w.extra, original) }, "failed", false},
		{"wrong workflow file", func() { gh.nativeRuns["900"]["path"] = ".github/workflows/tests.yml" }, "failed", false},
		{"wrong event", func() { gh.nativeRuns["900"]["event"] = "push" }, "failed", false},
		{"wrong train head", func() { gh.nativeRuns["900"]["head_sha"] = "other" }, "failed", false},
		{"wrong rider workflow file", func() { gh.nativeRuns["911"]["path"] = ".github/workflows/tests.yml" }, "unproven", true},
		{"wrong rider head", func() { gh.nativeRuns["911"]["head_sha"] = "other" }, "unproven", true},
		{"missing admitted proof", func() { started.Cars[0].NativeAttestation = nil }, "unproven", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			w.conclusion, gh.pending, gh.skipped = "", 0, false
			w.extra = []Check{original}
			gh.nativeRuns["900"]["path"], gh.nativeRuns["900"]["event"], gh.nativeRuns["900"]["head_sha"] = ".github/workflows/no-mistakes-required.yml", "pull_request", started.Head
			gh.nativeRuns["911"]["path"], gh.nativeRuns["911"]["head_sha"] = ".github/workflows/no-mistakes-required.yml", started.Cars[0].Head
			test.change()
			outcome, _, _, _, err := engine.ci(context.Background(), started)
			if outcome != test.outcome || (err != nil) != test.wantError || len(gh.merged) != 0 {
				t.Fatalf("outcome %q, error %v, merges %v", outcome, err, gh.merged)
			}
		})
	}
}

func TestNativeAttestationIsRecheckedBeforeLanding(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"missing", "pending", "failed", "skipped", "changed", "ambiguous", "head", "held", "draft", "review", "base", "sibling failed", "sibling pending", "sibling foreign", "replacement", "legacy"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			s, gh, engine, started, pulls := nativeTrain(t)
			pr := gh.pulls[pulls[0].URL]
			if change == "legacy" {
				started.Cars[0].NativeAttestation.DatabaseID = 0
				if err := engine.save(&started); err != nil {
					t.Fatal(err)
				}
			}
			gh.beforeList = func() {
				switch change {
				case "missing":
					pr.checks = pr.checks[:1]
				case "pending":
					pr.checks[1].Status, pr.checks[1].Conclusion = "IN_PROGRESS", ""
				case "failed":
					pr.checks[1].Conclusion = "FAILURE"
				case "skipped":
					pr.checks[1].Conclusion = "SKIPPED"
				case "changed":
					pr.checks[1].DetailsURL += "0"
				case "ambiguous":
					pr.checks = append(pr.checks, pr.checks[1])
				case "sibling failed":
					pr.checks[2].Conclusion = "FAILURE"
				case "sibling pending":
					pr.checks[2].Status, pr.checks[2].Conclusion = "IN_PROGRESS", ""
				case "sibling foreign":
					pr.checks[2].AppID++
				case "replacement":
					pr.checks = append(pr.checks[:1:1], pr.checks[2:]...)
				case "head":
					s.extend(pulls[0].HeadRefName, "more.txt", "more\n")
				case "held":
					pr.isHeld = true
				case "draft":
					pr.isDraft = true
				case "review":
					pr.reviewDecision = "REVIEW_REQUIRED"
				case "base":
					pr.baseRefName = "release"
				}
			}
			stepped := s.step(engine, started.ID)
			if len(gh.merged) != 0 || stepped.Cars[0].State != CarReturned || stepped.History[0].Result != RunChanged {
				t.Fatalf("merged %v, cars %+v, history %+v", gh.merged, stepped.Cars, stepped.History)
			}
			if change == "legacy" && stepped.Cars[0].NativeAttestation.DatabaseID != 0 {
				t.Fatal("legacy proof was backfilled")
			}
		})
	}
}

func TestNativeAttestationExceptionSurvivesRealCIRetryAndRecovery(t *testing.T) {
	t.Parallel()
	s, gh, engine, started, _ := nativeTrain(t)
	gh.workflowOf(started.Head).isFlaky = true
	retrying := s.step(engine, started.ID)
	if retrying.State != StateTesting || !slices.Equal(gh.rerunCalls(), []string{"101"}) || len(retrying.History[0].FailedOnce) != 1 || retrying.History[0].FailedOnce[0].Name != "test" {
		t.Fatalf("retry included PR-only check: %+v, retries %v", retrying.History, gh.rerunCalls())
	}
	gh.readiness = "BLOCKED"
	held := s.step(s.engine(gh), started.ID)
	if len(held.History[0].Passed) != 1 || len(held.History[0].PRChecks) != 1 || held.History[0].MergeHold == "" || len(gh.merged) != 0 {
		t.Fatalf("lost saved proof: %+v", held.History)
	}
	gh.readiness = "UNSTABLE"
	landed := s.step(s.engine(gh), started.ID)
	if landed.State != StateLanded || len(gh.merged) != 2 || len(gh.rerunCalls()) != 1 {
		t.Fatalf("recovery state %s, merges %v, retries %v", landed.State, gh.merged, gh.rerunCalls())
	}
}

func TestNativeAttestationRecoversSavedUnstableReadinessHold(t *testing.T) {
	t.Parallel()
	// Arrange: preserve the zero-attempt hold written before UNSTABLE could
	// be explained by the separately proven PR-only failure.
	s, gh, engine, started, _ := nativeTrain(t)
	gh.readiness = "BLOCKED"
	held := s.step(engine, started.ID)
	held.History[0].MergeHold = "GitHub reports merge readiness UNSTABLE"
	held.History[0].MergeReadiness = "UNSTABLE"
	if err := engine.save(&held); err != nil {
		t.Fatal(err)
	}
	gh.readiness = "UNSTABLE"

	// Act
	landed := s.step(s.engine(gh), held.ID)

	// Assert
	if landed.State != StateLanded || landed.Runs != 1 || landed.History[0].MergeAttempts != 1 || !slices.Equal(gh.mergeCalls(), []string{held.PR + "@" + held.Head}) || len(gh.rerunCalls()) != 0 {
		t.Fatalf("saved hold did not land its original proof: %+v, merges %v", landed, gh.mergeCalls())
	}
	if !slices.Equal(landed.History[0].Passed, held.History[0].Passed) || !slices.Equal(landed.History[0].PRChecks, held.History[0].PRChecks) || s.tree("refs/heads/main") != s.tree(held.Head) {
		t.Fatalf("recovery changed the saved proof: %+v", landed.History)
	}
}

func TestNativeAttestationUnstableMergeRefusalKeepsBoundedAttempts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		refusal  string
		attempts int
	}{
		{"branch policy", "the base branch policy prohibits the merge", 1},
		{"transient refusal", "merge temporarily refused", maxErrors},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s, gh, engine, started, _ := nativeTrain(t)
			gh.refuse[started.PR] = test.refusal

			// Act: restart the engine on each poll, including after the refusal
			// clears without a readiness transition.
			var held Train
			for range maxErrors + 2 {
				held, _ = s.engine(gh).Advance(context.Background(), started.ID)
			}
			delete(gh.refuse, started.PR)
			kept := s.step(engine, started.ID)

			// Assert: branch protection refusals retain proof and polling
			// cannot replenish a spent attempt.
			if kept.IsFinished() || kept.History[0].MergeHold == "" || kept.History[0].MergeReadiness != "UNSTABLE" || kept.History[0].MergeAttempts != test.attempts || len(gh.mergeCalls()) != test.attempts || len(gh.merged) != 0 || len(gh.rerunCalls()) != 0 {
				t.Fatalf("refusal lost custody or renewed attempts: %+v, merges %v", kept, gh.mergeCalls())
			}
			if !slices.Equal(kept.History[0].Passed, held.History[0].Passed) || !slices.Equal(kept.History[0].PRChecks, held.History[0].PRChecks) {
				t.Fatal("polling changed the saved proof")
			}
			gh.readiness = "BLOCKED"
			s.step(engine, started.ID)
			gh.readiness = "UNSTABLE"
			landed := s.step(s.engine(gh), started.ID)
			if landed.State != StateLanded || landed.History[0].MergeAttempts != test.attempts+1 || len(gh.mergeCalls()) != test.attempts+1 {
				t.Fatalf("positive transition did not grant exactly one attempt: %+v", landed)
			}
			for _, command := range gh.commandLines {
				if slices.Contains(command, "--admin") {
					t.Fatal("landing bypassed branch protection")
				}
			}
		})
	}
}

func TestUnstableTrainWithoutExcludedNativeFailureKeepsItsHold(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := provenRider(s, gh, 5)
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.readiness = "UNSTABLE"

	// Act
	held := s.step(engine, started.ID)
	kept := s.step(s.engine(gh), started.ID)

	// Assert
	if kept.IsFinished() || kept.History[0].MergeHold == "" || len(kept.History[0].PRChecks) != 0 || len(kept.History[0].Passed) == 0 || len(gh.mergeCalls()) != 0 || !slices.Equal(kept.History[0].Passed, held.History[0].Passed) {
		t.Fatalf("unexplained UNSTABLE was allowed to merge: %+v", kept)
	}
}

func TestNativeAttestationRecoveryRejectsChangedSavedChecks(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"source", "PR-only", "rider"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			s, gh, engine, started, pulls := nativeTrain(t)
			gh.readiness = "BLOCKED"
			held := s.step(engine, started.ID)
			gh.readiness = "UNSTABLE"
			switch change {
			case "source":
				gh.workflowOf(started.Head).extra = append(gh.workflowOf(started.Head).extra, Check{Kind: "CheckRun", Name: "extra", Status: "COMPLETED", Conclusion: "SUCCESS"})
			case "PR-only":
				gh.workflowOf(started.Head).extra[0].CompletedAt = "2026-10-07T17:00:00Z"
			case "rider":
				gh.pulls[pulls[0].URL].checks = gh.pulls[pulls[0].URL].checks[:1]
			}
			stepped := s.step(s.engine(gh), started.ID)
			if len(gh.merged) != 0 || stepped.State == StateLanded {
				t.Fatalf("changed proof landed: %+v", stepped)
			}
			if !slices.Equal(stepped.History[0].Passed, held.History[0].Passed) || !slices.Equal(stepped.History[0].PRChecks, held.History[0].PRChecks) {
				t.Fatalf("changed checks overwrote saved proof: %+v", stepped.History)
			}
		})
	}
}

func TestNativeAttestationRecoveryReconcilesLifecycle(t *testing.T) {
	t.Parallel()
	for _, observation := range []string{"changed failure", "pending", "success", "missing", "ambiguous", "incomplete", "rider rerun"} {
		for _, lifecycle := range []string{"closed", "base moved", "rider withdrawn", "base moved and rider withdrawn"} {
			t.Run(observation+"/"+lifecycle, func(t *testing.T) {
				t.Parallel()
				s, gh, engine, started, pulls := nativeTrain(t)
				gh.readiness = "BLOCKED"
				held := s.step(engine, started.ID)
				if len(held.History[0].Passed) != 1 || len(held.History[0].PRChecks) != 1 || held.History[0].MergeHold == "" {
					t.Fatalf("no saved proof to recover: %+v", held.History)
				}
				workflow := gh.workflowOf(held.Head)
				switch observation {
				case "changed failure":
					workflow.extra[0].CompletedAt = "2026-10-07T17:00:00Z"
				case "pending":
					workflow.extra[0].Status, workflow.extra[0].Conclusion = "IN_PROGRESS", ""
				case "success":
					workflow.extra[0].Conclusion = "SUCCESS"
				case "missing":
					workflow.extra = nil
				case "ambiguous":
					workflow.extra = append(workflow.extra, workflow.extra[0])
				case "incomplete":
					workflow.isCheckRollupIncomplete = true
				case "rider rerun":
					gh.nativeRuns["911"]["run_attempt"] = 2
					gh.pulls[pulls[0].URL].checks[1].RunAttempt = 2
				}
				base := held.BaseSHA
				if lifecycle == "base moved" || lifecycle == "base moved and rider withdrawn" {
					base = s.advanceMain("outside.txt", "outside\n")
				}
				if lifecycle == "rider withdrawn" || lifecycle == "base moved and rider withdrawn" {
					gh.pulls[pulls[0].URL].isHeld = true
				}
				if lifecycle == "closed" {
					gh.state[held.PR] = "CLOSED"
				}
				gh.readiness = "UNSTABLE"

				stepped := s.step(s.engine(gh), held.ID)

				if !slices.Equal(stepped.History[0].Passed, held.History[0].Passed) || !slices.Equal(stepped.History[0].PRChecks, held.History[0].PRChecks) || len(gh.mergeCalls()) != 0 || len(gh.rerunCalls()) != 0 {
					t.Fatalf("recovery changed proof, merged or retried: %+v, calls %v", stepped, gh.calls)
				}
				if lifecycle == "closed" {
					if stepped.State != StateFailed || stepped.History[0].Result != RunStopped || s.hasBranch(held.Branch) {
						t.Fatalf("closed PR stranded the train: %+v", stepped)
					}
					return
				}
				result := RunMoved
				if lifecycle == "rider withdrawn" {
					result = RunChanged
				}
				if stepped.IsFinished() || stepped.Runs != 2 || stepped.BaseSHA != base || stepped.History[0].Result != result || stepped.Cars[1].State != CarRiding || *stepped.Cars[1].NativeAttestation != *held.Cars[1].NativeAttestation {
					t.Fatalf("lifecycle did not rebuild eligible riders: %+v", stepped)
				}
				if (lifecycle != "base moved" || observation == "rider rerun") && (stepped.Cars[0].State != CarReturned || !slices.Equal(stepped.History[1].Riders, []int{12})) {
					t.Fatalf("withdrawn rider stayed on the train: %+v", stepped)
				}
				landed := s.step(s.engine(gh), held.ID)
				if landed.State != StateLanded || s.tree("refs/heads/main") != s.tree(stepped.Head) || !slices.Equal(landed.History[0].Passed, held.History[0].Passed) || !slices.Equal(landed.History[0].PRChecks, held.History[0].PRChecks) {
					t.Fatalf("rebuilt train did not land with saved proof intact: %+v", landed)
				}
			})
		}
	}
}

func TestNativeAttestationUnprovenCIKeepsSavedProof(t *testing.T) {
	t.Parallel()
	for _, observation := range []string{"incomplete", "rider rerun", "unknown mergeability"} {
		t.Run(observation, func(t *testing.T) {
			t.Parallel()
			s, gh, engine, started, pulls := nativeTrain(t)
			gh.readiness = "BLOCKED"
			held := s.step(engine, started.ID)
			switch observation {
			case "incomplete":
				gh.workflowOf(held.Head).isCheckRollupIncomplete = true
			case "rider rerun":
				gh.nativeRuns["911"]["run_attempt"] = 2
			case "unknown mergeability":
				gh.pulls[pulls[0].URL].mergeable = "UNKNOWN"
				gh.pulls[pulls[1].URL].isHeld = true
			}
			gh.readiness = "UNSTABLE"

			stepped, err := engine.Advance(context.Background(), held.ID)

			if err == nil || len(gh.mergeCalls()) != 0 || len(gh.rerunCalls()) != 0 || stepped.Runs != 1 || stepped.Head != held.Head {
				t.Fatalf("unproven CI authorized work: %+v, error %v, calls %v", stepped, err, gh.calls)
			}
			if !slices.Equal(stepped.History[0].Passed, held.History[0].Passed) || !slices.Equal(stepped.History[0].PRChecks, held.History[0].PRChecks) || *stepped.Cars[0].NativeAttestation != *held.Cars[0].NativeAttestation {
				t.Fatalf("unproven CI replaced saved proof: %+v", stepped)
			}
			if observation == "unknown mergeability" && (stepped.Cars[1].State != CarReturned || stepped.History[0].Result != RunChanged) {
				t.Fatalf("unknown mergeability stranded the later held rider: %+v", stepped)
			}
		})
	}
}

func TestNativeAttestationIncompleteRiderReconcilesLifecycle(t *testing.T) {
	t.Parallel()
	for _, incomplete := range []int{0, 1} {
		for _, lifecycle := range []string{"unchanged", "held", "draft", "closed", "head", "base", "review", "conflict", "landed"} {
			t.Run(fmt.Sprintf("rider %d/%s", incomplete, lifecycle), func(t *testing.T) {
				t.Parallel()
				s, gh, engine, started, pulls := nativeTrain(t)
				gh.readiness = "BLOCKED"
				held := s.step(engine, started.ID)
				gh.pulls[pulls[incomplete].URL].isCheckRollupIncomplete = true
				other := 1 - incomplete
				pr := gh.pulls[pulls[other].URL]
				switch lifecycle {
				case "held":
					pr.isHeld = true
				case "draft":
					pr.isDraft = true
				case "closed":
					pr.isClosed = true
				case "head":
					s.extend(pr.branch, "late.txt", "late\n")
				case "base":
					pr.baseRefName = "release"
				case "review":
					pr.reviewDecision = "REVIEW_REQUIRED"
				case "conflict":
					pr.mergeable = "CONFLICTING"
				case "landed":
					s.git(s.github, "push", "-q", "origin", held.Cars[other].Head+":refs/heads/main")
				}
				gh.readiness = "UNSTABLE"

				stepped, err := engine.Advance(context.Background(), held.ID)

				if err == nil || !strings.Contains(err.Error(), "check rollup is incomplete") || len(gh.mergeCalls()) != 0 || len(gh.rerunCalls()) != 0 {
					t.Fatalf("incomplete proof was used: %+v, error %v, calls %v", stepped, err, gh.calls)
				}
				if !slices.Equal(stepped.History[0].Passed, held.History[0].Passed) || !slices.Equal(stepped.History[0].PRChecks, held.History[0].PRChecks) || *stepped.Cars[incomplete].NativeAttestation != *held.Cars[incomplete].NativeAttestation {
					t.Fatalf("saved proof changed: %+v", stepped)
				}
				state := CarReturned
				if lifecycle == "unchanged" {
					state = CarRiding
				} else if lifecycle == "landed" {
					state = CarLanded
				}
				if stepped.Cars[other].State != state || lifecycle != "unchanged" && stepped.History[0].Result == "" {
					t.Fatalf("lifecycle change was lost behind incomplete proof: %+v", stepped)
				}
				kept, readErr := Read(engine.StateDir, held.ID)
				if readErr != nil || kept.Cars[other].State != state {
					t.Fatalf("lifecycle change was not persisted: %+v, %v", kept, readErr)
				}
				if lifecycle != "unchanged" {
					gh.pulls[pulls[incomplete].URL].isCheckRollupIncomplete = false
					rebuilt := s.step(s.engine(gh), held.ID)
					if rebuilt.Runs != 2 || !slices.Equal(rebuilt.History[1].Riders, []int{held.Cars[incomplete].Number}) {
						t.Fatalf("recovery did not rebuild the eligible remainder: %+v", rebuilt)
					}
				}
			})
		}
	}
}

func TestNativeAttestationRecoveryKeepsStaleHeadProof(t *testing.T) {
	t.Parallel()
	s, gh, engine, started, pulls := nativeTrain(t)
	gh.readiness = "BLOCKED"
	held := s.step(engine, started.ID)
	s.extend(held.Branch, "external.txt", "external\n")
	externalHead := s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)
	s.advanceMain("outside.txt", "outside\n")
	gh.pulls[pulls[0].URL].isHeld = true
	s.now = s.now.Add(noCIWithin)
	gh.readiness = "UNSTABLE"

	kept := s.step(s.engine(gh), held.ID)

	if kept.IsFinished() || kept.Runs != 1 || kept.Head != held.Head || kept.BaseSHA != held.BaseSHA || !slices.Equal(kept.History[0].Passed, held.History[0].Passed) || !slices.Equal(kept.History[0].PRChecks, held.History[0].PRChecks) || s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch) != externalHead || len(gh.mergeCalls()) != 0 || len(gh.rerunCalls()) != 0 {
		t.Fatalf("stale head lost custody or saved proof: %+v", kept)
	}
}

func TestAdvanceLandsAGreenTrainInOrderAndMainMatchesTheTrain(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	third := gh.open(13, "feat/c", s.branch("feat/c", "c.txt", "c\n"))
	riders, _ := Riders([]PullRequest{first, second, third}, "main", fleetAccount, goblinsFor(first, second, third), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if landed.State != StateLanded || landed.Runs != 1 {
		t.Fatalf("train = %+v, want landed after its one run", landed)
	}
	for _, car := range landed.Cars {
		if car.State != CarLanded {
			t.Fatalf("car #%d = %s, want landed", car.Number, car.State)
		}
	}
	if got := gh.mergeCalls(); !slices.Equal(got, []string{gh.trainURL + "@" + started.Head}) || !slices.Equal(gh.merged, []int{11, 12, 13}) {
		t.Fatalf("merges = %v, merged %v: want the train's own pull request merged once, pinned to the head CI tested, and each rider merged by it in train order", got, gh.merged)
	}
	if s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatal("main's tree differs from the train's tree after landing")
	}
	evidence := " by merge train " + started.ID + ": CI passed on " + gh.trainURL + " at " + started.Head[:7] + ", built on main at " + started.BaseSHA[:7]
	if want := []string{first.URL + evidence, second.URL + evidence, third.URL + evidence}; !slices.Equal(s.landed, want) {
		t.Fatalf("landed = %q, want each pull request heard of in train order with the run that proved it", s.landed)
	}
	if body := s.git(s.remote, "log", "-1", "--format=%b", "refs/heads/main"); !strings.Contains(body, strings.TrimPrefix(evidence, " by ")) {
		t.Fatalf("the train's merge commit says %q, want the run that proved it", body)
	}
	if len(gh.closed) != 0 || s.hasBranch(started.Branch) {
		t.Fatalf("closed %v, branch kept %v: want the train pull request merged, never closed, and its branch deleted", gh.closed, s.hasBranch(started.Branch))
	}
	if len(s.cfo) != 2 || !strings.Contains(s.cfo[1], "landed #11, #12, #13") {
		t.Fatalf("CFO told %q, want the landing", s.cfo)
	}
	if len(s.told) != 0 {
		t.Fatalf("goblins told %q, want nothing for a train that landed", s.told)
	}
	if got := runLog(landed); !slices.Equal(got, []string{"1 #11 #12 #13 landed"}) || landed.History[0].Base != started.BaseSHA || landed.History[0].Head != started.Head || !landed.History[0].Pushed.Equal(started.Pushed) {
		t.Fatalf("runs %q (%+v), want its one run on the base and head it tested, landed", got, landed.History)
	}
}

func TestAdvanceWaitsWhileTheTrainsChecksRun(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.pending = 1

	// Act
	waiting, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if waiting.State != StateTesting || len(gh.mergeCalls()) != 0 {
		t.Fatalf("train = %s with merges %v, want still testing", waiting.State, gh.mergeCalls())
	}
}

func TestAdvanceRetestsOnTheNewMainWhenMainMovedDuringTheRun(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	moved := s.advanceMain("outside.txt", "outside\n")

	// Act
	retest, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if retest.State != StateTesting || retest.Runs != 2 || retest.BaseSHA != moved || len(gh.mergeCalls()) != 0 {
		t.Fatalf("train = %+v with merges %v, want a second run on the new main and nothing merged", retest, gh.mergeCalls())
	}
	if s.fileOn(retest.Head, "outside.txt") != "outside\n" || s.fileOn(retest.Head, "a.txt") != "a\n" || !strings.Contains(retest.Note, "main moved") {
		t.Fatalf("retest head does not hold the new main and the riders, note %q", retest.Note)
	}

	// Act
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if landed.State != StateLanded || s.tree("refs/heads/main") != s.tree(retest.Head) {
		t.Fatalf("train = %s, want it landed on the new main", landed.State)
	}
	if got := runLog(landed); !slices.Equal(got, []string{"1 #11 #12 moved", "2 #11 #12 landed"}) || landed.History[1].Base != moved {
		t.Fatalf("runs %q, want the run main moved under and the run that landed on the new main", got)
	}
}

// GitHub refuses a merge whose base moved under it. One merge lands every
// rider or none, so nothing merged, and the next step reads main again
// rather than merging onto a main CI did not test.
func TestAMergeGitHubRefusesLandsNothingAndTheNextStepReadsMainAgain(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.modified = 1

	// Act
	refused, refusedErr := engine.Advance(context.Background(), started.ID)
	moved := s.advanceMain("outside.txt", "outside\n")
	retest := s.step(engine, started.ID)
	untested := slices.Clone(gh.merged)
	landed := s.step(engine, started.ID)

	// Assert
	if refusedErr == nil || refused.State != StateTesting || refused.Landing || refused.Errors != 1 || !slices.Equal(carStates(refused), []string{"#11=riding", "#12=riding"}) {
		t.Fatalf("refused: train %s landing %v with %d errors, cars %v, error %v: want it testing with nothing landed and one error counted", refused.State, refused.Landing, refused.Errors, carStates(refused), refusedErr)
	}
	if retest.State != StateTesting || retest.Runs != 2 || retest.BaseSHA != moved || len(untested) != 0 {
		t.Fatalf("after main moved: train %s on run %d at %s, merged %v: want it tested again on the new main with nothing merged", retest.State, retest.Runs, retest.BaseSHA, untested)
	}
	if landed.State != StateLanded || !slices.Equal(gh.merged, []int{11, 12}) || s.tree("refs/heads/main") != s.tree(retest.Head) || len(gh.closed) != 0 {
		t.Fatalf("train %s, merged %v, closed %v: want both landed on the tree CI tested last", landed.State, gh.merged, gh.closed)
	}
}

func TestAMergeGitHubKeepsRefusingHoldsItsGreenProofOnceItsErrorBudgetIsSpent(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	third := gh.open(13, "feat/c", s.branch("feat/c", "c.txt", "c\n"))
	riders, _ := Riders([]PullRequest{first, second, third}, "main", fleetAccount, goblinsFor(first, second, third), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	main := s.main()
	gh.refuse[gh.trainURL] = "Pull request is not mergeable: a required review is missing"

	// Act
	retried, retryErr := engine.Advance(context.Background(), started.ID)
	var failed Train
	for range maxErrors - 1 {
		failed, err = engine.Advance(context.Background(), started.ID)
	}

	// Assert
	if retryErr == nil || retried.State != StateTesting || retried.Landing || retried.Errors != 1 {
		t.Fatalf("first refusal: train %+v, error %v, want it testing with one error counted", retried, retryErr)
	}
	if err != nil || failed.IsFinished() || failed.Errors != maxErrors || len(gh.merged) != 0 || s.main() != main {
		t.Fatalf("train = %s (%v), merged %v, main moved %v: want it held with the exhausted budget and nothing merged", failed.State, err, gh.merged, s.main() != main)
	}
	if got := carStates(failed); !slices.Equal(got, []string{"#11=riding", "#12=riding", "#13=riding"}) {
		t.Fatalf("car states = %v", got)
	}
	for _, want := range []string{"holds its merge after 5 attempt(s)", gh.trainURL + " did not merge", "a required review is missing"} {
		if !strings.Contains(failed.Note, want) {
			t.Fatalf("note = %q, want %q", failed.Note, want)
		}
	}
	for range 3 {
		failed = s.step(s.engine(gh), started.ID)
	}
	if len(gh.closed) != 0 || !s.hasBranch(started.Branch) || len(gh.mergeCalls()) != maxErrors || len(failed.History[0].MergeFailures) != maxErrors || failed.History[0].MergeAttempts != maxErrors || failed.Errors != maxErrors {
		t.Fatalf("train %+v, closed %v, merges %v: want no discarded proof or renewed attempts", failed, gh.closed, gh.mergeCalls())
	}
	if len(s.cfo) != 2 || !strings.Contains(s.cfo[1], "holds its merge") {
		t.Fatalf("CFO told %q, want one hold notice", s.cfo)
	}
}

// A merge that got no answer because it never reached GitHub landed nothing:
// the riders land together on the next step, never some of them.
func TestALandingCutShortLandsEveryRiderOnTheNextStepAndNoneBefore(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	third := gh.open(13, "feat/c", s.branch("feat/c", "c.txt", "c\n"))
	riders, _ := Riders([]PullRequest{first, second, third}, "main", fleetAccount, goblinsFor(first, second, third), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	main := s.main()
	gh.unanswered["merge "+gh.trainURL] = 1

	// Act
	cut, cutErr := engine.Advance(context.Background(), started.ID)
	between := s.main()
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if cutErr == nil || cut.State != StateTesting || between != main || !slices.Equal(carStates(cut), []string{"#11=riding", "#12=riding", "#13=riding"}) {
		t.Fatalf("cut short: train %s, main moved %v, cars %v, error %v: want nothing landed", cut.State, between != main, carStates(cut), cutErr)
	}
	if err != nil || landed.State != StateLanded || !slices.Equal(gh.merged, []int{11, 12, 13}) {
		t.Fatalf("next step: train %s (%v), merged %v, want every rider merged with no new CI run", landed.State, err, gh.merged)
	}
	if landed.Runs != 1 || landed.Errors != 0 || s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatalf("train = %+v, want one run, no errors left and main holding its tree", landed)
	}
}

func TestAMergeThatWentThroughWhileItsAnswerWasLostIsTakenAsLanded(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.lost["merge "+gh.trainURL] = true

	// Act
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil || landed.State != StateLanded || len(gh.mergeCalls()) != 1 || !slices.Equal(gh.merged, []int{11, 12}) || len(gh.closed) != 0 {
		t.Fatalf("train %s (%v), merge calls %v, merged %v, closed %v: want the train found on main and never merged twice", landed.State, err, gh.mergeCalls(), gh.merged, gh.closed)
	}
}

func TestLandingRetestsWithoutARiderThatChangedAfterItRode(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(s *scratch, p *pull){
		"its head moved":    func(s *scratch, p *pull) { s.extend(p.branch, "late.txt", "late\n") },
		"it was held":       func(_ *scratch, p *pull) { p.isHeld = true },
		"it became a draft": func(_ *scratch, p *pull) { p.isDraft = true },
		"it was closed":     func(_ *scratch, p *pull) { p.isClosed = true },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
			third := gh.open(13, "feat/c", s.branch("feat/c", "c.txt", "c\n"))
			riders, _ := Riders([]PullRequest{first, second, third}, "main", fleetAccount, goblinsFor(first, second, third), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			change(s, gh.pulls[second.URL])

			// Act
			retest, err := engine.Advance(context.Background(), started.ID)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(gh.merged) != 0 {
				t.Fatalf("merged %v, want nothing merged once a rider changed", gh.merged)
			}
			if got := carStates(retest); retest.State != StateTesting || retest.Runs != 2 || !slices.Equal(got, []string{"#11=riding", "#12=returned", "#13=riding"}) {
				t.Fatalf("train %s after %d runs: cars %v, want #11 and #13 tested again without #12", retest.State, retest.Runs, got)
			}
			if !strings.Contains(retest.Note, "#12") || s.fileOn(retest.Head, "b.txt") != "" || s.fileOn(retest.Head, "c.txt") != "c\n" {
				t.Fatalf("note %q; the new run must name #12 and leave it out", retest.Note)
			}
			if got := runLog(retest); !slices.Equal(got, []string{"1 #11 #12 #13 changed", "2 #11 #13 open"}) {
				t.Fatalf("runs %q, want the run a rider changed after and the run without it", got)
			}
		})
	}
}

func TestARunWhosePushGitHubNeverShowedIsPushedAgain(t *testing.T) {
	t.Parallel()
	// Arrange: the record names a run the remote never got, as a restart
	// between recording a run and pushing it leaves it.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	lost := started
	lost.Head = strings.Repeat("0", 40)
	if err := write(s.state, lost); err != nil {
		t.Fatal(err)
	}

	// Act
	early, earlyErr := engine.Advance(context.Background(), started.ID)
	s.now = s.now.Add(noCIWithin)
	pushed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if earlyErr != nil || early.Head != lost.Head || early.Runs != 1 {
		t.Fatalf("before the wait ran out: train %+v, error %v, want it waiting", early, earlyErr)
	}
	if err != nil || pushed.Runs != 2 || pushed.Head == lost.Head || s.git(s.remote, "rev-parse", "refs/heads/"+started.Branch) != pushed.Head {
		t.Fatalf("train %+v (%v), want its run built and pushed again", pushed, err)
	}
	if gh.created != 1 || pushed.PR != started.PR {
		t.Fatalf("train pull requests opened %d, want the first kept", gh.created)
	}
	if got := runLog(pushed); !slices.Equal(got, []string{"1 #11 repushed", "2 #11 open"}) {
		t.Fatalf("runs %q, want the run pushed again and the run CI tests", got)
	}
}

func TestAStartCutShortAfterItOpenedItsPullRequestFindsItRatherThanOpeningAnother(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	gh.lost["create"] = true

	// Act
	cut, cutErr := engine.Start(context.Background(), s.repository(), riders)
	resumed, err := engine.Advance(context.Background(), cut.ID)

	// Assert
	if cutErr == nil || cut.State != StateTesting || cut.PR != "" {
		t.Fatalf("cut short: train %+v, error %v, want it kept without a pull request", cut, cutErr)
	}
	if err != nil || resumed.PR != gh.trainURL || gh.created != 1 || resumed.Head == "" {
		t.Fatalf("resumed: train %+v (%v), pull requests opened %d, want the one opened found", resumed, err, gh.created)
	}
}

func TestAStepThatKeepsFailingStopsTheTrainOnceItsErrorBudgetIsSpent(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.unanswered["view"] = maxErrors

	// Act
	var kept Train
	for range maxErrors - 1 {
		kept, _ = engine.Advance(context.Background(), started.ID)
	}
	failed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if kept.State != StateTesting || kept.Errors != maxErrors-1 {
		t.Fatalf("before the budget is spent: train %s with %d errors, want it kept", kept.State, kept.Errors)
	}
	if err != nil || failed.State != StateFailed || !strings.Contains(failed.Note, "the network dropped") || failed.Cars[0].State != CarReturned {
		t.Fatalf("train %+v (%v), want it failed naming the last error, its rider returned", failed, err)
	}
}

func TestCIThatNeverFinishesStopsTheTrainAtItsDeadline(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.pending = 2

	// Act
	s.now = s.now.Add(RunDeadline - time.Minute)
	running, runningErr := engine.Advance(context.Background(), started.ID)
	s.now = s.now.Add(time.Minute)
	stopped, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if runningErr != nil || running.State != StateTesting {
		t.Fatalf("before the deadline: train %s (%v), want it waiting", running.State, runningErr)
	}
	if err != nil || stopped.State != StateFailed || !strings.Contains(stopped.Note, "did not finish within") {
		t.Fatalf("at the deadline: train %+v (%v), want it failed", stopped, err)
	}
	if got := runLog(running); !slices.Equal(got, []string{"1 #11 open"}) {
		t.Fatalf("runs before the deadline %q, want its one run open", got)
	}
	if got := runLog(stopped); !slices.Equal(got, []string{"1 #11 stopped"}) {
		t.Fatalf("runs at the deadline %q, want its run stopped with the train", got)
	}
}

func TestATrainStopsWhenMainMovesDuringEveryRun(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	var stopped Train
	for run := range maxMoved {
		s.advanceMain(fmt.Sprintf("outside-%d.txt", run), "outside\n")
		if stopped, err = engine.Advance(context.Background(), started.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Assert
	if stopped.State != StateFailed || stopped.Runs != maxMoved || len(gh.merged) != 0 || !strings.Contains(stopped.Note, "moved during 3 runs in a row") {
		t.Fatalf("train %s after %d runs, merged %v, note %q", stopped.State, stopped.Runs, gh.merged, stopped.Note)
	}
	if got := runLog(stopped); !slices.Equal(got, []string{"1 #11 moved", "2 #11 moved", "3 #11 moved"}) {
		t.Fatalf("runs %q, want every run main moved under", got)
	}
	if gh.created != 1 || !slices.Equal(gh.closed, []string{gh.trainURL}) || !strings.Contains(gh.comments[0], "moved during 3 runs in a row") || s.hasBranch(started.Branch) || gh.reads(first.URL) != "OPEN" {
		t.Fatalf("opened %d, closed %v saying %q, branch kept %v, #11 reads %s: want its one pull request closed with why, its branch removed and #11 left open", gh.created, gh.closed, gh.comments, s.hasBranch(started.Branch), gh.reads(first.URL))
	}
}

func TestALandingThatLeavesMainWithAnotherTreeFailsTheTrain(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.moveMain = true

	// Act
	failed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil || failed.State != StateFailed || !strings.Contains(failed.Note, "does not hold the tree CI tested") || failed.Cars[0].State != CarLanded {
		t.Fatalf("train %+v (%v), want it failed with #11 landed and main's tree named", failed, err)
	}
}

// A run whose every check was skipped tested nothing, as a run that never
// reported did not: GitHub counts a skipped check as passing, and a train
// that took one for green would land what no CI ran on.
func TestAdvanceFailsATrainNoCIRanOn(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]func(*fakeGitHub){
		"no check reported":   func(gh *fakeGitHub) { gh.noChecks = true },
		"every check skipped": func(gh *fakeGitHub) { gh.skipped = true },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			answer(gh)

			// Act
			early, earlyErr := engine.Advance(context.Background(), started.ID)
			s.now = s.now.Add(noCIWithin)
			late, lateErr := engine.Advance(context.Background(), started.ID)

			// Assert
			if earlyErr != nil || lateErr != nil {
				t.Fatal(earlyErr, lateErr)
			}
			if early.State != StateTesting {
				t.Fatalf("train = %s before the wait for CI ran out, want testing", early.State)
			}
			if late.State != StateFailed || !strings.Contains(late.Note, "no CI") || late.Cars[0].State != CarReturned {
				t.Fatalf("train = %+v, want failed with no CI and its pull request returned", late)
			}
		})
	}
}

func TestAdvanceLeavesAFinishedTrainAlone(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Advance(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	calls := len(gh.calls)
	s.now = s.now.Add(time.Hour)

	// Act
	again, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil || again.State != StateLanded || len(gh.calls) != calls {
		t.Fatalf("advance of a landed train = %s, %v, with %d new gh calls", again.State, err, len(gh.calls)-calls)
	}
}

func TestABaseThatMovesDuringTheLandingRecheckCountsAsAMovedRun(t *testing.T) {
	t.Parallel()
	// Arrange: the run is green, and main moves while the riders are rechecked
	// before the merge.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	var moved string
	gh.beforeList = func() { moved = s.advanceMain("outside.txt", "outside\n") }

	// Act
	retest := s.step(engine, started.ID)

	// Assert: nothing merged, and the run counts toward the moved-base limit.
	if retest.Moved != 1 || !slices.Equal(runLog(retest), []string{"1 #11 #12 moved", "2 #11 #12 open"}) || retest.BaseSHA != moved || len(gh.mergeCalls()) != 0 {
		t.Fatalf("train moved %d times, runs %q, base %s, merges %v: want the run counted as moved and tested again on the new main", retest.Moved, runLog(retest), short(retest.BaseSHA), gh.mergeCalls())
	}
}
