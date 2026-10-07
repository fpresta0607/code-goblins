package train

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

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
	want := []string{first.URL + "@" + first.HeadRefOid, second.URL + "@" + second.HeadRefOid, third.URL + "@" + third.HeadRefOid}
	if got := gh.mergeCalls(); !slices.Equal(got, want) {
		t.Fatalf("merges = %v, want each pinned to its head in train order %v", got, want)
	}
	if s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatal("main's tree differs from the train's tree after landing")
	}
	evidence := " by merge train " + started.ID + ": CI passed on " + gh.trainURL + " at " + started.Head[:7] + ", built on main at " + started.BaseSHA[:7]
	if want := []string{first.URL + evidence, second.URL + evidence, third.URL + evidence}; !slices.Equal(s.landed, want) {
		t.Fatalf("landed = %q, want each pull request heard of in train order with the run that proved it", s.landed)
	}
	if !slices.Equal(gh.closed, []string{gh.trainURL}) || s.hasBranch(started.Branch) {
		t.Fatalf("closed %v, branch kept %v: want the train pull request closed and its branch deleted", gh.closed, s.hasBranch(started.Branch))
	}
	if len(s.cfo) != 2 || !strings.Contains(s.cfo[1], "landed #11, #12, #13") {
		t.Fatalf("CFO told %q, want the landing", s.cfo)
	}
	if len(s.told) != 0 {
		t.Fatalf("goblins told %q, want nothing for a train that landed", s.told)
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
}

func TestAdvanceRetriesAMergeGitHubRefusesBecauseTheBaseMovedUnderIt(t *testing.T) {
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
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if landed.State != StateLanded || !slices.Equal(gh.merged, []int{11, 12}) {
		t.Fatalf("train = %s, merged %v: want both landed after one retry", landed.State, gh.merged)
	}
}

func TestAMergeGitHubKeepsRefusingStopsTheTrainOnceItsErrorBudgetIsSpent(t *testing.T) {
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
	gh.refuse[second.URL] = "Pull request is not mergeable: a required review is missing"

	// Act
	retried, retryErr := engine.Advance(context.Background(), started.ID)
	var failed Train
	for range maxErrors - 1 {
		failed, err = engine.Advance(context.Background(), started.ID)
	}

	// Assert
	if retryErr == nil || retried.State != StateTesting || !retried.Landing || retried.Errors != 1 {
		t.Fatalf("first refusal: train %+v, error %v, want it kept landing with one error counted", retried, retryErr)
	}
	if err != nil || failed.State != StateFailed || !slices.Equal(gh.merged, []int{11}) {
		t.Fatalf("train = %s (%v), merged %v: want it failed after #11 with #13 never merged", failed.State, err, gh.merged)
	}
	if got := carStates(failed); !slices.Equal(got, []string{"#11=landed", "#12=returned", "#13=returned"}) {
		t.Fatalf("car states = %v", got)
	}
	for _, want := range []string{"failed 5 times in a row", "#12 did not merge", "a required review is missing", "stopped while landing"} {
		if !strings.Contains(failed.Note, want) {
			t.Fatalf("note = %q, want %q", failed.Note, want)
		}
	}
	if len(s.cfo) != 2 || !strings.Contains(s.cfo[1], "failed") {
		t.Fatalf("CFO told %q, want the failure", s.cfo)
	}
}

func TestALandingCutShortMergesTheRestOnTheNextStep(t *testing.T) {
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
	gh.unanswered["merge "+second.URL] = 1

	// Act
	cut, cutErr := engine.Advance(context.Background(), started.ID)
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if cutErr == nil || cut.State != StateTesting || !cut.Landing || !slices.Equal(carStates(cut), []string{"#11=landed", "#12=riding", "#13=riding"}) {
		t.Fatalf("cut short: train %s landing %v, cars %v, error %v", cut.State, cut.Landing, carStates(cut), cutErr)
	}
	if err != nil || landed.State != StateLanded || !slices.Equal(gh.merged, []int{11, 12, 13}) {
		t.Fatalf("next step: train %s (%v), merged %v, want the rest merged with no new CI run", landed.State, err, gh.merged)
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
	gh.lost["merge "+first.URL] = true

	// Act
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil || landed.State != StateLanded || len(gh.mergeCalls()) != 2 || !slices.Equal(gh.merged, []int{11, 12}) {
		t.Fatalf("train %s (%v), merge calls %v, merged %v: want #11 found on main and never merged twice", landed.State, err, gh.mergeCalls(), gh.merged)
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
	s.now = s.now.Add(runDeadline - time.Minute)
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

func TestAdvanceFailsATrainNoCIRanOn(t *testing.T) {
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
	gh.noChecks = true

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
