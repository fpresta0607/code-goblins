package train

import (
	"context"
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
