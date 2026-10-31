package train

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Train code-goblins-20261014-142332 landed PR 821 "in 0 CI runs" because an
// earlier train had tested it, and wrote no merge line to AFK mode's log:
// a rider the train finds already on its base is marked landed without a
// word. Every landing is heard of, whichever run tested it.
func TestARiderTheTrainFindsAlreadyOnItsBaseIsHeardOfAsLanded(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	early := gh.open(21, "feat/early", s.branch("feat/early", "early.txt", "early\n"))
	later := gh.open(22, "feat/later", s.branch("feat/later", "later.txt", "later\n"))
	s.git(s.github, "fetch", "-q", "origin")
	s.git(s.github, "checkout", "-q", "-B", "main", "origin/main")
	s.git(s.github, "merge", "-q", "--no-ff", "-m", "Merge pull request #21 from o/feat/early", "origin/feat/early")
	s.git(s.github, "push", "-q", "origin", "main")
	riders, _ := Riders([]PullRequest{early, later}, "main", fleetAccount, goblinsFor(early, later), nil)

	// Act
	started, err := s.engine(gh).Start(context.Background(), s.repository(), riders)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if started.Cars[0].State != CarLanded || !strings.HasPrefix(started.Cars[0].Note, "already on ") {
		t.Fatalf("car #21 = %+v, want it found already on main", started.Cars[0])
	}
	if len(s.landed) != 1 || !strings.HasPrefix(s.landed[0], early.URL+" by merge train "+started.ID+" found "+early.URL+" already on main") {
		t.Errorf("heard of landings %q, want #21 heard of once, with the train finding it already on main", s.landed)
	}
}

func TestSelectedRidersAlreadyOnBaseReconcileAtEveryComputeEntrance(t *testing.T) {
	for _, phase := range []string{"start", "retry", "split", "moved base", "landing recheck", "waiting rider"} {
		t.Run(phase, func(t *testing.T) {
			s := newScratch(t)
			github := newFakeGitHub(s)
			first := github.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			second := github.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
			riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
			mergeRider := func(pr PullRequest) {
				result, err := github.merge(pr.URL, pr.HeadRefOid, true)
				if err != nil || result.ExitCode != 0 {
					t.Fatalf("operator merge: %+v, %v", result, err)
				}
			}
			if phase == "start" {
				mergeRider(first)
			}
			if phase == "retry" || phase == "split" || phase == "waiting rider" {
				github.breaks = []string{second.HeadRefOid}
			}
			started, err := s.engine(github).Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "split" || phase == "waiting rider" {
				s.step(s.engine(github), started.ID)
			}
			if phase == "waiting rider" {
				s.step(s.engine(github), started.ID)
				github.whileLanding = func() { github.beforeList = func() { mergeRider(second) } }
			} else if phase == "landing recheck" {
				github.beforeList = func() { mergeRider(first) }
			} else if phase != "start" {
				if phase == "moved base" {
					github.refuse[started.PR] = "the base branch policy prohibits the merge"
					s.step(s.engine(github), started.ID)
					delete(github.refuse, started.PR)
				}
				mergeRider(first)
			}
			retries := len(github.rerunCalls())

			kept := started
			if phase != "start" {
				kept = s.step(s.engine(github), started.ID)
			}
			found := 0
			if phase == "waiting rider" {
				found = 1
			}
			if kept.Cars[found].State != CarLanded || kept.Cars[found].Note != "already on main" || len(github.rerunCalls()) != retries || len(s.goblinTold(kept.Cars[found].Task)) != 0 {
				t.Fatalf("already-merged selected source was retried, returned, or blamed: %+v", kept)
			}
			for range 5 {
				if kept.IsFinished() {
					break
				}
				kept = s.step(s.engine(github), started.ID)
			}
			kept = s.step(s.engine(github), started.ID)
			var reported []string
			for _, landing := range s.landed {
				if strings.HasPrefix(landing, kept.Cars[found].URL+" by ") {
					reported = append(reported, landing)
				}
			}
			if !kept.IsFinished() || len(reported) != 1 || !strings.Contains(reported[0], "already on main") || strings.Contains(reported[0], "CI passed") || !strings.Contains(s.cfo[len(s.cfo)-1], "landed #11") || strings.Contains(s.cfo[len(s.cfo)-1], "#11 waits") {
				t.Fatalf("already-merged rider reporting was lost or fabricated: %+v, callbacks %q, notices %q", kept, s.landed, s.cfo)
			}
			if phase == "retry" || phase == "split" {
				if kept.Cars[1].State != CarCulprit || len(s.goblinTold("g12")) != 1 || len(kept.History[0].Failed) == 0 {
					t.Fatalf("reconciliation lost the real red behavior or failure: %+v", kept)
				}
			} else if !slices.Equal(carStates(kept), []string{"#11=landed", "#12=landed"}) || s.fileOn("refs/heads/main", "a.txt") != "a\n" || s.fileOn("refs/heads/main", "b.txt") != "b\n" {
				t.Fatalf("reconciliation lost landed source: %+v", kept)
			}
		})
	}
}

func TestALandingTheTrainMergedStandsOnTheRunCITested(t *testing.T) {
	// Arrange
	train := Train{ID: "r-1", PR: "https://github.com/o/r/pull/900", Head: "abc1234def", BaseSHA: "1234567890", Base: "main"}

	// Act
	says := train.LandingEvidence(Car{State: CarLanded})

	// Assert
	if says != train.Evidence() {
		t.Errorf("evidence = %q, want the run CI tested: %q", says, train.Evidence())
	}
}
