package train

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestARunRedOnceThenGreenLandsAndNamesTheCheckThatFailedOnce(t *testing.T) {
	t.Parallel()
	// Arrange: the train's workflow run fails its first try by chance.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(12, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	gh.chance = 1
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	failedOnce := []FailedCheck{{Name: "test", Link: "https://github.com/o/r/actions/runs/101/job/1011"}}

	// Act: the run is red, so its failed jobs run again.
	retrying := s.step(engine, started.ID)

	// Assert
	if got := carStates(retrying); retrying.State != StateTesting || !slices.Equal(got, []string{"#11=riding", "#12=riding"}) || retrying.Runs != 1 || len(gh.merged) != 0 {
		t.Fatalf("after one red run: train %s, cars %v, runs %d, merged %v: want the same run still testing", retrying.State, got, retrying.Runs, gh.merged)
	}
	if !slices.Equal(gh.rerunCalls(), []string{"101"}) || !slices.Equal(gh.tries(), []int{2}) {
		t.Fatalf("gh run rerun named %v and the workflow runs were tried %v times: want the red run's failed jobs run again once", gh.rerunCalls(), gh.tries())
	}
	if run := retrying.History[0]; run.Result != "" || !slices.Equal(run.FailedOnce, failedOnce) || !strings.Contains(retrying.Note, "test") || !strings.Contains(retrying.Note, "again") {
		t.Fatalf("run %+v, note %q: want the run still open, with the check that failed once and that it runs again", run, retrying.Note)
	}

	// Act: the second try is green, so the train lands.
	landed := s.step(engine, started.ID)

	// Assert
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) || !slices.Equal(gh.merged, []int{11, 12}) || landed.Runs != 1 {
		t.Fatalf("train %s after %d runs: cars %v, merged %v", landed.State, landed.Runs, got, gh.merged)
	}
	if got := runLog(landed); !slices.Equal(got, []string{"1 #11 #12 landed"}) || !slices.Equal(landed.History[0].FailedOnce, failedOnce) || len(landed.History[0].Failed) != 0 {
		t.Fatalf("runs %q (%+v), want the one run landed, keeping the check that failed once", got, landed.History)
	}
	last := s.cfo[len(s.cfo)-1]
	for _, want := range []string{"landed #11, #12", "failed once and passed on the second try", "test (" + failedOnce[0].Link + ")"} {
		if !strings.Contains(last, want) {
			t.Fatalf("CFO told %q, want %q", last, want)
		}
	}
	if len(s.told) != 0 || !slices.Equal(gh.tries(), []int{2}) {
		t.Fatalf("goblins told %q, workflow runs tried %v times: want nobody blamed and no third try", s.told, gh.tries())
	}
}

func TestALoneRiderIsNotMarkedTheCulpritOnOneRedRun(t *testing.T) {
	t.Parallel()
	// Arrange: the lone rider breaks CI on every try.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	lone := gh.open(21, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	gh.breaks = []string{lone.HeadRefOid}
	riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act: one red run.
	once := s.step(engine, started.ID)

	// Assert
	if got := carStates(once); once.State != StateTesting || !slices.Equal(got, []string{"#21=riding"}) || len(s.told) != 0 {
		t.Fatalf("after one red run: train %s, cars %v, goblins told %q: want nobody blamed yet", once.State, got, s.told)
	}

	// Act: the run is red a second time.
	stopped := s.step(engine, started.ID)

	// Assert
	firstTry, secondTry := "https://github.com/o/r/actions/runs/101/job/1011", "https://github.com/o/r/actions/runs/101/job/1012"
	if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#21=culprit"}) || stopped.Runs != 1 {
		t.Fatalf("train %s after %d runs: cars %v, want the rider blamed once its run was red twice", stopped.State, stopped.Runs, got)
	}
	if told := s.goblinTold("g21"); len(told) != 1 || !strings.Contains(told[0], "test ("+secondTry+")") {
		t.Fatalf("g21 told %q, want the check that failed on the second try with its link", told)
	}
	run := stopped.History[0]
	if run.Result != RunFailed || run.Link != secondTry || !slices.Equal(run.FailedOnce, []FailedCheck{{Name: "test", Link: firstTry}}) || !slices.Equal(run.Failed, []FailedCheck{{Name: "test", Link: secondTry}}) {
		t.Fatalf("run %+v, want it failed, with the check red on each try", run)
	}
	if last := s.cfo[len(s.cfo)-1]; strings.Contains(last, "passed on the second try") {
		t.Fatalf("CFO told %q, want no check named as passing when it was red twice", last)
	}
	if !slices.Equal(gh.rerunCalls(), []string{"101"}) || !slices.Equal(gh.tries(), []int{2}) {
		t.Fatalf("gh run rerun named %v, workflow runs tried %v times: want one second try and no third", gh.rerunCalls(), gh.tries())
	}
}

func TestNoRunIsTriedAThirdTime(t *testing.T) {
	t.Parallel()
	for name, arrange := range map[string]func(gh *fakeGitHub){
		"every answer came":                           func(*fakeGitHub) {},
		"the ask reached GitHub, its answer was lost": func(gh *fakeGitHub) { gh.lost["run rerun"] = true },
		"the ask never reached GitHub":                func(gh *fakeGitHub) { gh.unanswered["run rerun"] = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			lone := gh.open(31, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			gh.breaks = []string{lone.HeadRefOid}
			riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			arrange(gh)

			// Act: the train takes its steps, a failed one included, until it is over.
			stopped := started
			for range maxErrors {
				if stopped.IsFinished() {
					break
				}
				stopped, _ = engine.Advance(context.Background(), started.ID)
			}

			// Assert
			if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#31=culprit"}) {
				t.Fatalf("train %s: cars %v, note %q: want the rider blamed", stopped.State, got, stopped.Note)
			}
			if !slices.Equal(gh.tries(), []int{2}) {
				t.Fatalf("workflow runs tried %v times after gh run rerun named %v: want exactly a second try", gh.tries(), gh.rerunCalls())
			}
		})
	}
}

func TestARunIsNotRunAgainWhenGitHubDoesNotSayWhichTryItIsAt(t *testing.T) {
	t.Parallel()
	// Arrange: a red run, and a read of its workflow run that names no attempt.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	lone := gh.open(61, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	gh.breaks, gh.noAttempt = []string{lone.HeadRefOid}, true
	riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	kept, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "attempt") {
		t.Fatalf("step error = %v, want the step to fail on a workflow run whose attempt it cannot read", err)
	}
	if got := carStates(kept); kept.State != StateTesting || !slices.Equal(got, []string{"#61=riding"}) || len(gh.rerunCalls()) != 0 || len(s.told) != 0 {
		t.Fatalf("train %s: cars %v, gh run rerun named %v, goblins told %q: want nothing run again and nobody blamed", kept.State, got, gh.rerunCalls(), s.told)
	}
}

func TestATrainWaitsWhileGitHubStillShowsTheTryBefore(t *testing.T) {
	t.Parallel()
	for name, pending := range map[string]int{"the second try still runs": 1, "the second try passed": 0} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange: the run failed once by chance and its failed jobs run again.
			s := newScratch(t)
			gh := newFakeGitHub(s)
			lone := gh.open(41, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			gh.chance = 1
			riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), s.repository(), riders)
			if err != nil {
				t.Fatal(err)
			}
			s.step(engine, started.ID)
			gh.lag, gh.pending = 1, pending

			// Act: the pull request's checks still show the first try's red.
			waiting := s.step(engine, started.ID)

			// Assert
			if got := carStates(waiting); waiting.State != StateTesting || !slices.Equal(got, []string{"#41=riding"}) || len(gh.merged) != 0 || len(s.told) != 0 {
				t.Fatalf("train %s: cars %v, merged %v, goblins told %q: want it to wait for the second try", waiting.State, got, gh.merged, s.told)
			}
			if !slices.Equal(gh.tries(), []int{2}) {
				t.Fatalf("workflow runs tried %v times, want no third try for a first try GitHub still shows", gh.tries())
			}

			// Act: GitHub shows the second try.
			landed := waiting
			for range 3 {
				if landed.IsFinished() {
					break
				}
				landed = s.step(engine, started.ID)
			}

			// Assert
			if landed.State != StateLanded || !slices.Equal(gh.merged, []int{41}) || !slices.Equal(gh.tries(), []int{2}) {
				t.Fatalf("train %s, merged %v, workflow runs tried %v times: want it landed on its second try", landed.State, gh.merged, gh.tries())
			}
		})
	}
}

func TestACheckNoWorkflowRunStandsBehindIsActedOnAsItStands(t *testing.T) {
	t.Parallel()
	// Arrange: the red check is another service's, which gh cannot run again.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	lone := gh.open(51, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	gh.breaks, gh.foreign = []string{lone.HeadRefOid}, true
	riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	stopped := s.step(engine, started.ID)

	// Assert
	if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#51=culprit"}) || len(gh.rerunCalls()) != 0 {
		t.Fatalf("train %s: cars %v, gh run rerun named %v: want the failure acted on without a second try", stopped.State, got, gh.rerunCalls())
	}
	if run := stopped.History[0]; len(run.FailedOnce) != 0 || !slices.Equal(run.Failed, []FailedCheck{{Name: "scan", Link: "https://scan.example/report"}}) {
		t.Fatalf("run %+v, want it failed on the one check, never tried again", run)
	}
}

func TestAFinishedTrainNamesEachCheckThatFailedOnceAndPassedWhenItRanAgain(t *testing.T) {
	t.Parallel()
	// Arrange: run 1 was red twice, on fewer checks the second time; run 2
	// landed on its second try; run 3 ended before its second try did.
	check := func(name string) FailedCheck {
		return FailedCheck{Name: name, Link: "https://github.com/o/r/actions/runs/7/job/" + name}
	}
	stopped := Train{
		State: StateStopped, Runs: 3, PR: "https://github.com/o/r/pull/900",
		Cars: []Car{{Number: 1, State: CarLanded}, {Number: 2, State: CarCulprit}},
		History: []Run{
			{Number: 1, Riders: []int{1, 2}, Result: RunFailed, FailedOnce: []FailedCheck{check("conpty"), check("rest"), check("all")}, Failed: []FailedCheck{check("rest"), check("all")}},
			{Number: 2, Riders: []int{1}, Result: RunLanded, FailedOnce: []FailedCheck{check("spawn")}},
			{Number: 3, Riders: []int{2}, Result: RunStopped, FailedOnce: []FailedCheck{check("cmd")}},
			{Number: 4, Riders: []int{2}, Result: RunFailed, Failed: []FailedCheck{check("rest")}},
		},
	}

	// Act
	summary := Engine{}.summary(stopped)

	// Assert
	want := "failed once and passed on the second try: conpty (" + check("conpty").Link + ") in run 1, spawn (" + check("spawn").Link + ") in run 2"
	if !strings.Contains(summary, want) {
		t.Fatalf("summary %q, want %q", summary, want)
	}
	for _, never := range []string{"rest (", "all (", "cmd ("} {
		if strings.Contains(summary, never) {
			t.Fatalf("summary %q names %q, which was red twice or never finished its second try", summary, never)
		}
	}
}
