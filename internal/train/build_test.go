package train

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStartMergesTheRidersOntoMainInQueueOrderAndOpensTheTrainPullRequest(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	main := s.main()
	riders, left := Riders([]PullRequest{second, first}, "main", fleetAccount, goblinsFor(first, second), nil)

	// Act
	started, err := s.engine(gh).Start(context.Background(), s.repository(), riders)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("left = %v, want every pull request riding", left)
	}
	if started.State != StateTesting || started.PR != gh.trainURL || started.Runs != 1 || started.BaseSHA != main {
		t.Fatalf("train = %+v, want testing on main %s with its pull request", started, main)
	}
	if !strings.HasPrefix(started.Branch, BranchPrefix) || gh.trainBranch != started.Branch {
		t.Fatalf("branch = %q, pull request opened from %q", started.Branch, gh.trainBranch)
	}
	if head := s.git(s.remote, "rev-parse", "refs/heads/"+started.Branch); head != started.Head {
		t.Fatalf("remote train branch = %s, want the train's head %s", head, started.Head)
	}
	if s.fileOn(started.Head, "a.txt") != "a\n" || s.fileOn(started.Head, "b.txt") != "b\n" {
		t.Fatal("the train's head does not hold both pull requests' changes")
	}
	// Queue order: #11 was reported done first, so it is merged first: the
	// train's first parent chain reads main, then #11, then #12, each as
	// GitHub's own merge of it would read, title and all, since these are the
	// commits main takes.
	if got := s.git(s.remote, "log", "--format=%s: %b", "--first-parent", main+".."+started.Head); got != "Merge pull request #12 from o/feat/b: change 12\n\nMerge pull request #11 from o/feat/a: change 11" {
		t.Fatalf("train commits =\n%s", got)
	}
	if numbers := []int{started.Cars[0].Number, started.Cars[1].Number}; !slices.Equal(numbers, []int{11, 12}) {
		t.Fatalf("cars = %v, want queue order 11, 12", numbers)
	}
	create := gh.calls[slices.IndexFunc(gh.calls, func(args []string) bool { return args[1] == "create" })]
	if flagValue(create, "--base") != "main" || flagValue(create, "--repo") != "o/r" || flagValue(create, "--title") != "chore(cfo): merge train for PRs #11, #12" {
		t.Fatalf("gh pr create = %v", create)
	}
	body := flagValue(create, "--body")
	for _, want := range []string{"#11 from Goblin11 (g11), #12 from Goblin12 (g12)", "this pull request merges", "GitHub marks each of those pull requests merged"} {
		if !strings.Contains(body, want) {
			t.Fatalf("train pull request body = %q, want %q", body, want)
		}
	}
	if strings.ContainsAny(body, ";\u2014") || started.History[0].PR != gh.trainURL {
		t.Fatalf("body %q, run %+v: want no semicolon or long dash in what he reads, and the run to name its pull request", body, started.History[0])
	}
	saved, err := Read(s.state, started.ID)
	if err != nil || saved.Head != started.Head || saved.State != StateTesting {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	if len(s.cfo) != 1 || !strings.Contains(s.cfo[0], "merge_train: ") || !strings.Contains(s.cfo[0], "#11, #12") {
		t.Fatalf("CFO told %q, want the start of the train", s.cfo)
	}
}

func TestStartSkipsAPullRequestThatConflictsWithTheOnesAheadOfIt(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(21, "feat/a", s.branch("feat/a", "shared.txt", "a\n"))
	clash := gh.open(22, "feat/clash", s.branch("feat/clash", "shared.txt", "clash\n"))
	third := gh.open(23, "feat/c", s.branch("feat/c", "c.txt", "c\n"))
	riders, _ := Riders([]PullRequest{first, clash, third}, "main", fleetAccount, goblinsFor(first, clash, third), nil)

	// Act
	started, err := s.engine(gh).Start(context.Background(), s.repository(), riders)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if states := []string{started.Cars[0].State, started.Cars[1].State, started.Cars[2].State}; !slices.Equal(states, []string{CarRiding, CarConflict, CarRiding}) {
		t.Fatalf("car states = %v", states)
	}
	if !strings.Contains(started.Cars[1].Note, "shared.txt") {
		t.Fatalf("conflict note = %q, want the conflicting file", started.Cars[1].Note)
	}
	if s.fileOn(started.Head, "shared.txt") != "a\n" || s.fileOn(started.Head, "c.txt") != "c\n" {
		t.Fatal("the train's head does not hold #21 and #23")
	}
	if len(s.goblinTold("g22")) != 0 {
		t.Fatalf("g22 told %q before the train landed what it conflicts with", s.goblinTold("g22"))
	}
}

func TestStartRefusesASecondTrainWhileOneRunsOnTheRepository(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(31, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{first}, "main", fleetAccount, goblinsFor(first), nil)
	engine := s.engine(gh)
	running, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	s.now = s.now.Add(10 * time.Second)

	// Act
	_, err = engine.Start(context.Background(), s.repository(), riders)

	// Assert
	if !errors.Is(err, ErrRunning) || !strings.Contains(err.Error(), running.ID) {
		t.Fatalf("second start = %v, want ErrRunning naming %s", err, running.ID)
	}
	if gh.created != 1 {
		t.Fatalf("train pull requests opened = %d, want 1", gh.created)
	}
}

func TestStartWithEveryRiderInConflictStopsWithoutCI(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	clash := gh.open(41, "feat/clash", s.branch("feat/clash", "README.md", "clash\n"))
	s.advanceMain("README.md", "moved\n")
	riders, _ := Riders([]PullRequest{clash}, "main", fleetAccount, goblinsFor(clash), nil)

	// Act
	stopped, err := s.engine(gh).Start(context.Background(), s.repository(), riders)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != StateStopped || stopped.Runs != 0 || gh.created != 0 {
		t.Fatalf("train = %+v with %d pull requests opened, want stopped before any CI", stopped, gh.created)
	}
	if told := s.goblinTold("g41"); len(told) != 1 || !strings.Contains(told[0], "Merge origin/main into your branch") {
		t.Fatalf("g41 told %q, want to merge main", told)
	}
}
