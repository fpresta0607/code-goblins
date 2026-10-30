package train

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestRefusedGreenTrainKeepsItsProofAcrossPollsAndRestarts(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun], func(t *testing.T) {
			// Arrange
			s := newScratch(t)
			github := newFakeGitHub(s)
			pr := provenRider(s, github, 5)
			repository := s.repository()
			repository.LandsLoneRider = isOwnRun
			riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
			started, err := s.engine(github).Start(context.Background(), repository, riders)
			if err != nil {
				t.Fatal(err)
			}
			url := started.PR
			if isOwnRun {
				url = pr.URL
			}
			github.refuse[url] = "the base branch policy prohibits the merge"
			github.readiness = "CLEAN"

			// Act: each poll constructs another engine over the same record.
			var kept Train
			for range maxErrors + 3 {
				kept, _ = s.engine(github).Advance(context.Background(), started.ID)
			}

			// Assert
			if kept.IsFinished() || kept.Head != started.Head || kept.BaseSHA != started.BaseSHA || kept.PR != started.PR || kept.Runs != 1 || kept.Cars[0].State != CarRiding {
				t.Fatalf("train %+v: want the same active green source and rider", kept)
			}
			if len(github.closed) != 0 || len(github.mergeCalls()) != 1 || len(github.rerunCalls()) != 0 || !isOwnRun && len(github.opened) != 1 {
				t.Fatalf("closed %v, merges %v, reruns %v, opened %v: want retained proof and bounded merge attempts", github.closed, github.mergeCalls(), github.rerunCalls(), github.opened)
			}
			if !isOwnRun && !s.hasBranch(started.Branch) {
				t.Fatal("the branch holding green proof was removed")
			}
			if len(s.cfo) != 2 || !strings.Contains(s.cfo[1], "hold") {
				t.Fatalf("CFO told %q: want one start and one durable hold", s.cfo)
			}
		})
	}
}

func newHeldTrain(t *testing.T, isOwnRun bool) (*scratch, *fakeGitHub, Train) {
	t.Helper()
	s := newScratch(t)
	github := newFakeGitHub(s)
	pr := provenRider(s, github, 5)
	repository := s.repository()
	repository.LandsLoneRider = isOwnRun
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	started, err := s.engine(github).Start(context.Background(), repository, riders)
	if err != nil {
		t.Fatal(err)
	}
	github.refuse[started.History[0].PR] = "the base branch policy prohibits the merge"
	held := s.step(s.engine(github), started.ID)
	return s, github, held
}

func TestHeldTrainConsumesOnlyOneAttemptPerPositiveReadinessTransition(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun], func(t *testing.T) {
			// Arrange
			s, github, held := newHeldTrain(t, isOwnRun)
			proof := slices.Clone(held.History[0].Passed)
			failures := len(held.History[0].MergeFailures)

			// Act: unknown and unchanged observations grant no attempts.
			for _, readiness := range []string{"CLEAN", "UNKNOWN", "UNKNOWN", "BLOCKED", "BLOCKED"} {
				github.readiness = readiness
				held = s.step(s.engine(github), held.ID)
			}
			if len(github.mergeCalls()) != 1 || held.Errors != 1 || held.History[0].MergeAttempts != 1 {
				t.Fatalf("merges %v, errors %d, attempts %d: polling renewed the budget", github.mergeCalls(), held.Errors, held.History[0].MergeAttempts)
			}
			github.readiness = "CLEAN"
			held = s.step(s.engine(github), held.ID)
			for range 3 {
				held = s.step(s.engine(github), held.ID)
			}

			// Assert: the refused recovery attempt is spent across restarts.
			if len(github.mergeCalls()) != 2 || held.Errors != 2 || held.History[0].MergeAttempts != 2 || len(held.History[0].MergeFailures) != failures+1 || !slices.Equal(proof, held.History[0].Passed) || len(s.cfo) != 2 {
				t.Fatalf("train %+v, merges %v, notices %q: want one additional attempt with the original proof and failures", held, github.mergeCalls(), s.cfo)
			}

			// Act: another positive transition can land the existing proof.
			github.readiness = "BLOCKED"
			s.step(s.engine(github), held.ID)
			delete(github.refuse, held.History[0].PR)
			github.readiness = "CLEAN"
			landed := s.step(s.engine(github), held.ID)

			// Assert
			if landed.State != StateLanded || landed.Runs != 1 || landed.Head != held.Head || len(github.mergeCalls()) != 3 || !slices.Equal(proof, landed.History[0].Passed) || len(github.rerunCalls()) != 0 {
				t.Fatalf("train %+v, merges %v: want the same green source landed without CI", landed, github.mergeCalls())
			}
		})
	}
}

func TestIneligibleRidersLeaveBeforeRebuildingOnANewBase(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun], func(t *testing.T) {
			// Arrange
			s, github, held := newHeldTrain(t, isOwnRun)
			github.pulls[held.Cars[0].URL].isHeld = true
			s.advanceMain("outside.txt", "outside\n")

			// Act
			stopped := s.step(s.engine(github), held.ID)

			// Assert
			if stopped.Runs != 1 || stopped.Cars[0].State != CarReturned || len(github.rerunCalls()) != 0 || len(s.told) != 0 || len(github.mergeCalls()) != 1 {
				t.Fatalf("train %+v, retries %v, notices %q: want no new validation or blame for the held source", stopped, github.rerunCalls(), s.told)
			}
		})
	}
}

func TestRedTrainRechecksRidersBeforeSplitting(t *testing.T) {
	// Arrange
	s := newScratch(t)
	github := newFakeGitHub(s)
	first := github.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := github.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	github.breaks = []string{second.HeadRefOid}
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(github)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	s.step(engine, started.ID)
	github.pulls[second.URL].isHeld = true

	// Act
	rebuilt := s.step(engine, started.ID)

	// Assert
	if rebuilt.Runs != 2 || rebuilt.Cars[1].State != CarReturned || len(s.told) != 0 || len(github.rerunCalls()) != 1 || s.fileOn(rebuilt.Head, "b.txt") != "" {
		t.Fatalf("train %+v, notices %q, retries %v: want only the eligible source rebuilt without blame", rebuilt, s.told, github.rerunCalls())
	}
}

func TestHeldWaitingRiderDoesNotInvalidateTheGreenHalf(t *testing.T) {
	// Arrange
	s := newScratch(t)
	github := newFakeGitHub(s)
	var pulls []PullRequest
	for number := 11; number <= 14; number++ {
		pulls = append(pulls, provenRider(s, github, number))
	}
	github.breaks = []string{pulls[2].HeadRefOid}
	riders, _ := Riders(pulls, "main", fleetAccount, goblinsFor(pulls...), nil)
	engine := s.engine(github)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	s.step(engine, started.ID)
	halved := s.step(engine, started.ID)
	github.pulls[pulls[3].URL].isHeld = true

	// Act
	next := s.step(engine, halved.ID)

	// Assert
	if next.Runs != 3 || next.History[1].Result != RunLanded || !slices.Equal(github.merged, []int{11, 12}) {
		t.Fatalf("train %+v, merged %v: a waiting rider changed no source of the green half", next, github.merged)
	}
}

func TestHeldTrainReconcilesAnOperatorMergeWithoutAnotherAttempt(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun], func(t *testing.T) {
			// Arrange: another operator performs the normal merge on the kept PR.
			s, github, held := newHeldTrain(t, isOwnRun)
			delete(github.refuse, held.History[0].PR)
			var err error
			if isOwnRun {
				_, err = github.merge(held.Cars[0].URL, held.Cars[0].Head, true)
			} else {
				_, err = github.mergeTrain([]string{"pr", "merge", held.PR, "--merge", "--match-head-commit", held.Head})
			}
			if err != nil {
				t.Fatal(err)
			}

			// Act
			landed := s.step(s.engine(github), held.ID)

			// Assert
			if landed.State != StateLanded || landed.History[0].Result != RunLanded || landed.History[0].MergeAttempts != 1 || len(github.mergeCalls()) != 1 || len(s.landed) != 1 || len(github.rerunCalls()) != 0 {
				t.Fatalf("train %+v, attempts %v, landed %q: want reconciliation without another merge or CI", landed, github.mergeCalls(), s.landed)
			}
		})
	}
}

func TestHeldTrainNeverReusesChangedCheckOrMergeIdentity(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		for _, change := range []string{"check", "merge ref", "unknown"} {
			t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun]+"/"+change, func(t *testing.T) {
				// Arrange
				s, github, held := newHeldTrain(t, isOwnRun)
				github.readiness = "BLOCKED"
				s.step(s.engine(github), held.ID)
				delete(github.refuse, held.History[0].PR)
				github.readiness = "CLEAN"
				switch change {
				case "check":
					if isOwnRun {
						github.ownRuns[held.Cars[0].URL].run++
					} else {
						github.workflowOf(held.Head).id++
					}
				case "merge ref":
					if !isOwnRun {
						github.readiness = "UNKNOWN"
					} else {
						github.ownRuns[held.Cars[0].URL].merge = held.BaseSHA
					}
				case "unknown":
					github.readiness = "UNKNOWN"
				}

				// Act
				for range 3 {
					s.step(s.engine(github), held.ID)
				}

				// Assert
				if len(github.mergeCalls()) != 1 || len(github.rerunCalls()) != 0 || len(github.merged) != 0 || len(s.cfo) != 2 {
					t.Fatalf("merges %v, retries %v, landed %v, notices %q: changed or unknown proof must hold", github.mergeCalls(), github.rerunCalls(), github.merged, s.cfo)
				}
			})
		}
	}
}

func TestHeldTrainValidatesANewBaseWithoutRenewingTheOldProof(t *testing.T) {
	for _, isOwnRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "train run", true: "own run"}[isOwnRun], func(t *testing.T) {
			// Arrange
			s, github, held := newHeldTrain(t, isOwnRun)
			base := s.advanceMain("outside.txt", "outside\n")

			// Act
			rebuilt := s.step(s.engine(github), held.ID)

			// Assert
			if rebuilt.Runs != 2 || rebuilt.BaseSHA != base || rebuilt.Head == held.Head || rebuilt.History[0].Result != RunMoved || rebuilt.History[0].MergeAttempts != 1 || len(rebuilt.History[0].Passed) == 0 || len(github.mergeCalls()) != 1 {
				t.Fatalf("train %+v: want new validation and retained old proof, no untested merge", rebuilt)
			}
		})
	}
}

func TestHeldTrainDeliversAHoldWhoseFirstNotificationFailed(t *testing.T) {
	// Arrange
	s := newScratch(t)
	github := newFakeGitHub(s)
	pr := provenRider(s, github, 5)
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	started, err := s.engine(github).Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	github.refuse[started.PR] = "the base branch policy prohibits the merge"
	engine := s.engine(github)
	engine.TellCFO = func(context.Context, string) error { return errors.New("wake queue unavailable") }

	// Act
	_, notificationError := engine.Advance(context.Background(), started.ID)
	held := s.step(s.engine(github), started.ID)
	s.step(s.engine(github), started.ID)

	// Assert
	if notificationError == nil || len(s.cfo) != 2 || len(github.mergeCalls()) != 1 || held.Errors != 1 || !held.History[0].IsMergeNotified {
		t.Fatalf("notices %q, merges %v: want the unacknowledged hold delivered once after recovery", s.cfo, github.mergeCalls())
	}
}

func TestChangedTrainHeadKeepsThePriorProofWithoutOverwritingTheBranch(t *testing.T) {
	// Arrange
	s, github, held := newHeldTrain(t, false)
	s.extend(held.Branch, "external.txt", "external\n")
	changedHead := s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)
	s.now = s.now.Add(noCIWithin)

	// Act
	for range 3 {
		s.step(s.engine(github), held.ID)
	}
	kept, err := Read(s.state, held.ID)

	// Assert
	if err != nil || kept.Runs != 1 || kept.Head != held.Head || kept.History[0].Head != held.Head || s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch) != changedHead || len(github.mergeCalls()) != 1 {
		t.Fatalf("train %+v (%v), merges %v: want the changed branch held without overwriting either source", kept, err, github.mergeCalls())
	}
}

func TestGreenTrainWithHooksStillUsesANormalPinnedMerge(t *testing.T) {
	// Arrange
	s, github, held := newHeldTrain(t, false)
	delete(github.refuse, held.PR)
	github.readiness = "HAS_HOOKS"

	// Act
	landed := s.step(s.engine(github), held.ID)

	// Assert
	if landed.State != StateLanded || landed.Runs != 1 || !slices.Equal(github.mergeCalls(), []string{held.PR + "@" + held.Head, held.PR + "@" + held.Head}) {
		t.Fatalf("train %+v, calls %v: want only normal pinned merges of the same tested source", landed, github.mergeCalls())
	}
}

func TestIneligibleCancelledRiderIsNeverRetriedOrBlamed(t *testing.T) {
	for _, change := range []string{"held", "closed", "draft", "moved"} {
		t.Run(change, func(t *testing.T) {
			// Arrange
			s := newScratch(t)
			github := newFakeGitHub(s)
			pr := github.open(5, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
			engine := s.engine(github)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			github.workflowOf(started.Head).conclusion = "cancelled"
			switch change {
			case "held":
				github.pulls[pr.URL].isHeld = true
			case "closed":
				github.pulls[pr.URL].isClosed = true
			case "draft":
				github.pulls[pr.URL].isDraft = true
			case "moved":
				s.extend(pr.HeadRefName, "more.txt", "more\n")
			}

			// Act
			for range 3 {
				_, _ = engine.Advance(context.Background(), started.ID)
			}
			kept, err := Read(s.state, started.ID)

			// Assert
			if err != nil || len(github.rerunCalls()) != 0 || len(s.told) != 0 || kept.Cars[0].State == CarCulprit || kept.Runs != 1 {
				t.Fatalf("train %+v (%v), reruns %v, blame %q: want no retry, rebuild or blame", kept, err, github.rerunCalls(), s.told)
			}
		})
	}
}
