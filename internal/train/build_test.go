package train

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
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
	for _, want := range []string{
		"#11 from Goblin11 (g11), #12 from Goblin12 (g12)",
		"merged onto main at " + short(main),
		"this pull request merges only the pinned combination CI tested",
		"after repository policy, exact head and base, successful check identities and current rider eligibility are verified",
		"main takes the commit CI tested",
		"GitHub marks each of those pull requests merged, since main then holds its head",
		"A policy or identity hold keeps this pull request open and visible without repeated CI runs",
		"Before any failed-check retry or split, the train rechecks current rider eligibility",
		"A run without green proof whose checks fail gets one retry of its failed checks",
		"Every green half lands only after the same checks",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("train pull request body = %q, want %q", body, want)
		}
	}
	// The body is prose of several lines, which reaches gh in a file and
	// never on its command line.
	for _, commandLine := range gh.commandLines {
		if slices.Contains(commandLine, "--body") || slices.Contains(commandLine, body) {
			t.Fatalf("gh was started with a body on its command line: %q", commandLine[:min(4, len(commandLine))])
		}
	}
	if started := gh.commandLines[slices.IndexFunc(gh.commandLines, func(args []string) bool { return args[1] == "create" })]; !slices.Contains(started, "--body-file") {
		t.Fatalf("gh pr create was started without --body-file: %q", started)
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

func TestNewPullRequestGetsItsCIWindowAfterPublicationRecovery(t *testing.T) {
	for _, isCreationAcknowledgementLost := range []bool{false, true} {
		t.Run(map[bool]string{false: "creation succeeds", true: "creation acknowledgement lost"}[isCreationAcknowledgementLost], func(t *testing.T) {
			s := newScratch(t)
			github := newFakeGitHub(s)
			pullRequest := github.open(5, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			riders, _ := Riders([]PullRequest{pullRequest}, "main", fleetAccount, goblinsFor(pullRequest), nil)
			github.unanswered["create"] = 1
			started, startError := s.engine(github).Start(context.Background(), s.repository(), riders)
			if startError == nil || started.Runs != 1 || started.PR != "" || github.created != 0 || len(github.pushes) != 1 {
				t.Fatalf("want one published head with interrupted PR creation: %+v, %v", started, startError)
			}
			s.now = s.now.Add(30 * time.Minute)
			publishedAt := s.now
			github.noChecks = true
			if isCreationAcknowledgementLost {
				github.lost["create"] = true
				if _, err := s.engine(github).Advance(context.Background(), started.ID); err == nil {
					t.Fatal("want the creation acknowledgement to be lost")
				}
			}

			recovered := s.step(s.engine(github), started.ID)
			s.now = s.now.Add(time.Minute)
			polled := s.step(s.engine(github), started.ID)

			for _, kept := range []Train{recovered, polled} {
				if kept.IsFinished() || kept.Cars[0].State != CarRiding || kept.Runs != 1 || kept.Head != started.Head || kept.BaseSHA != started.BaseSHA || kept.PR != github.trainURL || !kept.Pushed.Equal(publishedAt) || !kept.History[0].Pushed.Equal(publishedAt) {
					t.Fatalf("new PR lost its CI window or recorded source: %+v", kept)
				}
			}
			if github.created != 1 || len(github.pushes) != 1 || len(github.mergeCalls()) != 0 || len(github.rerunCalls()) != 0 || len(github.closed) != 0 || s.fileOn(started.Head, "a.txt") != "a\n" {
				t.Fatalf("publication recovery repeated work or discarded source: created %d, pushes %d, merges %v, reruns %v, closed %v", github.created, len(github.pushes), github.mergeCalls(), github.rerunCalls(), github.closed)
			}
		})
	}
}

func TestRecoveredPullRequestDoesNotRenewItsExistingCIWindow(t *testing.T) {
	for _, test := range []struct {
		name      string
		isPending bool
		delay     time.Duration
	}{
		{name: "missing checks", delay: noCIWithin + time.Minute},
		{name: "pending checks", isPending: true, delay: RunDeadline + time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newScratch(t)
			github := newFakeGitHub(s)
			pullRequest := github.open(5, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			riders, _ := Riders([]PullRequest{pullRequest}, "main", fleetAccount, goblinsFor(pullRequest), nil)
			github.lost["create"] = true
			started, startError := s.engine(github).Start(context.Background(), s.repository(), riders)
			if startError == nil || github.created != 1 || started.PR != "" {
				t.Fatalf("want an existing PR whose creation acknowledgement was lost: %+v, %v", started, startError)
			}
			s.now = s.now.Add(test.delay)
			github.noChecks = !test.isPending
			if test.isPending {
				github.pending = 1
			}

			expired := s.step(s.engine(github), started.ID)

			if expired.State != StateFailed || expired.Runs != 1 || expired.Head != started.Head || !expired.Pushed.Equal(started.Pushed) || !expired.History[0].Pushed.Equal(started.History[0].Pushed) || github.created != 1 || len(github.closed) != 1 || len(github.mergeCalls()) != 0 || len(github.rerunCalls()) != 0 {
				t.Fatalf("recovering an existing PR renewed its deadline or repeated validation: %+v", expired)
			}
		})
	}
}

func TestInterruptedPublicationKeepsItsRecordedRunAndExternalCommits(t *testing.T) {
	for _, hasExternalCommit := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume exact run", true: "preserve external extension"}[hasExternalCommit], func(t *testing.T) {
			s, github, held := newHeldTrain(t, false)
			proof := slices.Clone(held.History[0].Passed)
			failures := slices.Clone(held.History[0].MergeFailures)
			base := s.advanceMain("outside.txt", "outside\n")
			github.shouldLosePush, github.shouldLosePushRead = true, true

			cut, cutError := s.engine(github).Advance(context.Background(), held.ID)
			saved, readError := Read(s.state, held.ID)
			if cutError == nil || readError != nil || saved.Head == "" || saved.Runs != 2 || saved.Head != saved.History[1].Head || saved.BaseSHA != base {
				t.Fatalf("interrupted publication lost its recorded head: %+v, %v, %v", cut, cutError, readError)
			}
			published := s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)
			if published != saved.Head {
				t.Fatalf("push did not reach the remote: %s, want %s", published, saved.Head)
			}
			if hasExternalCommit {
				s.extend(held.Branch, "external.txt", "external\n")
				published = s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)
			}
			pushes := len(github.pushes)
			github.pending = maxErrors + 2
			var kept Train
			for range maxErrors + 2 {
				kept, _ = s.engine(github).Advance(context.Background(), held.ID)
			}

			if kept.IsFinished() || kept.Runs != 2 || kept.Head != saved.Head || hasExternalCommit && kept.Errors != saved.Errors || !slices.Equal(kept.History[0].Passed, proof) || !slices.Equal(kept.History[0].MergeFailures, failures) || kept.History[0].MergeAttempts != 1 || len(github.pushes) != pushes || s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch) != published || len(github.mergeCalls()) != 1 || len(github.rerunCalls()) != 0 || len(s.cfo) != 2 {
				t.Fatalf("recovery duplicated validation or lost branch custody: %+v, pushes %d -> %d, notices %q", kept, pushes, len(github.pushes), s.cfo)
			}
			if hasExternalCommit && s.fileOn(published, "external.txt") != "external\n" {
				t.Fatal("externally added source was overwritten")
			}
		})
	}
}

func TestUnknownBranchIdentityCannotExhaustIntoCleanup(t *testing.T) {
	s, github, held := newHeldTrain(t, false)
	s.advanceMain("outside.txt", "outside\n")
	github.unansweredBranchReads = maxErrors + 2
	pushes := len(github.pushes)
	var kept Train
	for range maxErrors + 2 {
		var err error
		kept, err = s.engine(github).Advance(context.Background(), held.ID)
		if err == nil {
			t.Fatal("want unknown branch identity reported without cleanup")
		}
	}
	if kept.IsFinished() || kept.Head != held.Head || kept.Runs != 1 || kept.Errors != held.Errors || !slices.Equal(kept.History[0].Passed, held.History[0].Passed) || len(github.pushes) != pushes || len(github.closed) != 0 || len(github.mergeCalls()) != 1 || len(s.cfo) != 2 || !s.hasBranch(held.Branch) {
		t.Fatalf("unknown branch identity discarded custody: %+v, notices %q", kept, s.cfo)
	}
}

func TestInitialPublicationCannotReplaceAnUnrecordedBranch(t *testing.T) {
	s := newScratch(t)
	github := newFakeGitHub(s)
	pr := github.open(5, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	var externalHead string
	github.beforePush = func(request execx.Request) {
		_, ref, _ := strings.Cut(request.Args[len(request.Args)-1], ":refs/heads/")
		externalHead = s.branch(ref, "external.txt", "external\n")
	}

	started, err := s.engine(github).Start(context.Background(), s.repository(), riders)
	if err == nil || externalHead == "" {
		t.Fatalf("want initial branch collision to refuse publication: %+v, %v", started, err)
	}
	for range maxErrors + 1 {
		started, _ = s.engine(github).Advance(context.Background(), started.ID)
	}

	if started.IsFinished() || started.Runs != 1 || github.created != 0 || len(github.pushes) != 1 || len(github.mergeCalls()) != 0 || s.git(s.remote, "rev-parse", "refs/heads/"+started.Branch) != externalHead || s.fileOn(externalHead, "external.txt") != "external\n" {
		t.Fatalf("initial publication overwrote or recomputed an unknown branch: %+v", started)
	}
}

func TestCleanupHoldsBeforeClosingAnExternallyExtendedTrain(t *testing.T) {
	s, github, held := newHeldTrain(t, false)
	s.extend(held.Branch, "external.txt", "external\n")
	externalHead := s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)

	err := s.engine(github).finish(context.Background(), &held, StateFailed, "stop requested")
	kept, readError := Read(s.state, held.ID)

	if err == nil || readError != nil || kept.IsFinished() || kept.Head != held.Head || len(github.closed) != 0 || s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch) != externalHead {
		t.Fatalf("cleanup closed or deleted an externally extended train: %+v, %v, %v", kept, err, readError)
	}
}

func TestCleanupRecognizesADeletionWhoseAcknowledgementWasLost(t *testing.T) {
	s, github, held := newHeldTrain(t, false)
	github.shouldLosePush = true

	err := s.engine(github).removeBranch(context.Background(), held)

	if err != nil || s.hasBranch(held.Branch) || s.fileOn("refs/heads/feat/5", "f5.txt") != "feat/5\n" {
		t.Fatalf("cleanup failed to recover its deletion or lost source: %v", err)
	}
}

func TestSuccessfulPushWithLostAcknowledgementDoesNotBuildAgain(t *testing.T) {
	for _, isInitial := range []bool{false, true} {
		t.Run(map[bool]string{false: "rebuild", true: "start"}[isInitial], func(t *testing.T) {
			var s *scratch
			var github *fakeGitHub
			var built Train
			if isInitial {
				s = newScratch(t)
				github = newFakeGitHub(s)
				pr := github.open(5, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
				riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
				github.shouldLosePush = true
				var err error
				built, err = s.engine(github).Start(context.Background(), s.repository(), riders)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				s, github, built = newHeldTrain(t, false)
				s.advanceMain("outside.txt", "outside\n")
				github.shouldLosePush = true
				built = s.step(s.engine(github), built.ID)
			}
			pushes := len(github.pushes)
			github.pending = 3

			var kept Train
			for range 3 {
				kept = s.step(s.engine(github), built.ID)
			}

			if kept.Runs != built.Runs || kept.Head != built.Head || len(github.pushes) != pushes || github.created != 1 || s.git(s.remote, "rev-parse", "refs/heads/"+built.Branch) != built.Head {
				t.Fatalf("acknowledgement recovery repeated a build: %+v, pushes %d -> %d", kept, pushes, len(github.pushes))
			}
		})
	}
}

func TestBranchWritesRejectAnExtensionAfterTheirIdentityRead(t *testing.T) {
	for _, isCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "delete"}[isCleanup], func(t *testing.T) {
			s, github, held := newHeldTrain(t, false)
			var externalHead string
			github.beforePush = func(execx.Request) {
				s.extend(held.Branch, "external.txt", "external\n")
				externalHead = s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch)
			}
			var err error
			if isCleanup {
				err = s.engine(github).removeBranch(context.Background(), held)
			} else {
				s.advanceMain("outside.txt", "outside\n")
				_, err = s.engine(github).Advance(context.Background(), held.ID)
			}

			if err == nil || externalHead == "" || s.git(s.remote, "rev-parse", "refs/heads/"+held.Branch) != externalHead || s.fileOn(externalHead, "external.txt") != "external\n" {
				t.Fatalf("branch mutation overwrote or deleted an external commit: %v, %s", err, externalHead)
			}
		})
	}
}
