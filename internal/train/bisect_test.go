package train

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// carStates reads the train's cars as "#number=state".
func carStates(t Train) []string {
	var states []string
	for _, car := range t.Cars {
		states = append(states, "#"+strings.TrimPrefix(car.URL, "https://github.com/o/r/pull/")+"="+car.State)
	}
	return states
}

// runLog says each run of t, in order, as "1 #51 #52 failed", and a run CI
// still tests as open.
func runLog(t Train) []string {
	var runs []string
	for _, run := range t.History {
		line := fmt.Sprint(run.Number)
		for _, number := range run.Riders {
			line += fmt.Sprintf(" #%d", number)
		}
		runs = append(runs, line+" "+cmp.Or(run.Result, "open"))
	}
	return runs
}

func TestARedTrainIsHalvedLandsTheGreenHalfAndBlamesTheCulprit(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	var prs []PullRequest
	for i, name := range []string{"a", "b", "c", "d"} {
		prs = append(prs, gh.open(51+i, "feat/"+name, s.branch("feat/"+name, name+".txt", name+"\n")))
	}
	gh.breaks = []string{prs[2].HeadRefOid}
	riders, _ := Riders(prs, "main", fleetAccount, goblinsFor(prs...), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act: the whole train is red on both tries, so its first half rides alone.
	s.step(engine, started.ID)
	halved, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := carStates(halved); !slices.Equal(got, []string{"#51=riding", "#52=riding", "#53=waiting", "#54=waiting"}) || halved.Runs != 2 || len(gh.merged) != 0 {
		t.Fatalf("after the red run: cars %v, runs %d, merged %v", got, halved.Runs, gh.merged)
	}

	// Act: the first half is green and lands; the waiting half is halved.
	next, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := carStates(next); !slices.Equal(got, []string{"#51=landed", "#52=landed", "#53=riding", "#54=waiting"}) || !slices.Equal(gh.merged, []int{51, 52}) {
		t.Fatalf("after the green half: cars %v, merged %v", got, gh.merged)
	}
	if s.tree("refs/heads/main") != s.tree(halved.Head) {
		t.Fatal("main's tree differs from the green half's tree")
	}
	if next.BaseSHA != s.main() || s.fileOn(next.Head, "c.txt") != "c\n" || s.fileOn(next.Head, "d.txt") != "" {
		t.Fatal("the next run is not #53 alone on the new main")
	}

	// Act: #53 is red alone on both tries, so it is the culprit; #54 waits for
	// the next train.
	s.step(engine, started.ID)
	stopped, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#51=landed", "#52=landed", "#53=culprit", "#54=returned"}) || stopped.Runs != 3 {
		t.Fatalf("train %s after %d runs: cars %v", stopped.State, stopped.Runs, got)
	}
	if !slices.Equal(gh.tries(), []int{2, 1, 2}) {
		t.Fatalf("workflow runs tried %v times, want each red run tried again once and the green one never", gh.tries())
	}
	failing, alone := "https://github.com/o/r/actions/runs/101/job/1012", "https://github.com/o/r/actions/runs/103/job/1032"
	told := s.goblinTold("g53")
	if len(told) != 1 || !strings.Contains(told[0], prs[2].URL) || !strings.Contains(told[0], "test") || !strings.Contains(told[0], alone) {
		t.Fatalf("g53 told %q, want its pull request and the failing check with its link", told)
	}
	for _, task := range []string{"g51", "g52", "g54"} {
		if len(s.goblinTold(task)) != 0 {
			t.Fatalf("%s told %q, want nothing", task, s.goblinTold(task))
		}
	}
	if last := s.cfo[len(s.cfo)-1]; !strings.Contains(last, "#53") || !strings.Contains(last, "landed #51, #52") {
		t.Fatalf("CFO told %q, want the culprit and what landed", last)
	}
	if !slices.Equal(gh.closed, []string{gh.trainURL}) || s.hasBranch(started.Branch) {
		t.Fatal("the train pull request was not closed or its branch was kept")
	}
	if got := runLog(stopped); !slices.Equal(got, []string{"1 #51 #52 #53 #54 failed", "2 #51 #52 landed", "3 #53 failed"}) {
		t.Fatalf("runs %q, want each run with its riders and how it ended", got)
	}
	if runs := stopped.History; runs[0].Link != failing || runs[1].Link != "" || runs[2].Link != alone || runs[2].Base != next.BaseSHA || runs[2].Head != next.Head || runs[1].Base != halved.BaseSHA {
		t.Fatalf("runs %+v, want each red run linking its failed check, on the base and head it tested", runs)
	}
	firstTry := []FailedCheck{{Name: "test", Link: "https://github.com/o/r/actions/runs/101/job/1011"}}
	if run := stopped.History[0]; !slices.Equal(run.FailedOnce, firstTry) || !slices.Equal(run.Failed, []FailedCheck{{Name: "test", Link: failing}}) || len(stopped.History[1].FailedOnce) != 0 {
		t.Fatalf("runs %+v, want a red run to keep the check red on each try and the green run none", stopped.History)
	}
	if last := s.cfo[len(s.cfo)-1]; strings.Contains(last, "passed on the second try") {
		t.Fatalf("CFO told %q, want no check named as passing when each was red twice", last)
	}
}

func TestATrainWhoseFirstRiderBreaksItLandsNothingAndReturnsTheRest(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	broken := gh.open(61, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	fine := gh.open(62, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	gh.breaks = []string{broken.HeadRefOid}
	riders, _ := Riders([]PullRequest{broken, fine}, "main", fleetAccount, goblinsFor(broken, fine), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act: both riders are red twice, then the first alone is.
	for range 3 {
		s.step(engine, started.ID)
	}
	stopped, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#61=culprit", "#62=returned"}) || len(gh.merged) != 0 {
		t.Fatalf("train %s: cars %v, merged %v", stopped.State, got, gh.merged)
	}
}

func TestALoneRiderThatFailsAfterItsHalfLandedIsTestedBeforeItIsBlamed(t *testing.T) {
	t.Parallel()
	// Arrange: the first run is red on both tries for no rider's fault, as a
	// fault of the runner's that lasts is, so #72 is tested alone before it
	// is blamed and then lands.
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(71, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	second := gh.open(72, "feat/b", s.branch("feat/b", "b.txt", "b\n"))
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)
	engine := s.engine(gh)
	gh.breaks = []string{second.HeadRefOid}
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	s.step(engine, started.ID)
	s.step(engine, started.ID)
	gh.breaks = nil

	// Act
	if _, err := engine.Advance(context.Background(), started.ID); err != nil {
		t.Fatal(err)
	}
	landed, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#71=landed", "#72=landed"}) || landed.Runs != 3 {
		t.Fatalf("train %s after %d runs: cars %v", landed.State, landed.Runs, got)
	}
	if len(s.told) != 0 {
		t.Fatalf("goblins told %q, want no blame for a pull request that passed alone", s.told)
	}
}

func TestATrainTellsAConflictingRidersGoblinToMergeMainOnceItLands(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first := gh.open(81, "feat/a", s.branch("feat/a", "shared.txt", "a\n"))
	clash := gh.open(82, "feat/clash", s.branch("feat/clash", "shared.txt", "clash\n"))
	riders, _ := Riders([]PullRequest{first, clash}, "main", fleetAccount, goblinsFor(first, clash), nil)
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
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#81=landed", "#82=conflict"}) {
		t.Fatalf("train %s: cars %v", landed.State, got)
	}
	told := s.goblinTold("g82")
	if len(told) != 1 || !strings.Contains(told[0], "Merge origin/main into your branch") || !strings.Contains(told[0], "shared.txt") || !strings.Contains(told[0], clash.URL) {
		t.Fatalf("g82 told %q, want to merge main over shared.txt", told)
	}

	// Act: the next train finds #82 still in conflict at the same head.
	s.now = s.now.Add(10 * time.Minute)
	past, _ := List(s.state)
	again, _ := Riders([]PullRequest{clash}, "main", fleetAccount, goblinsFor(clash), past)
	stoppedAgain, err := engine.Start(context.Background(), s.repository(), again)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if stoppedAgain.State != StateStopped || len(s.goblinTold("g82")) != 1 {
		t.Fatalf("second train %s, g82 told %q: want it told once per head", stoppedAgain.State, s.goblinTold("g82"))
	}
}

func TestNoPullRequestIsBlamedWhileMainItselfIsRed(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	lone := gh.open(91, "feat/a", s.branch("feat/a", "a.txt", "a"))
	gh.breaks = []string{lone.HeadRefOid}
	gh.mainRuns = `[{"workflowName":"go","status":"completed","conclusion":"failure"},{"workflowName":"go","status":"completed","conclusion":"success"},{"workflowName":"frontend","status":"completed","conclusion":"success"}]`
	riders, _ := Riders([]PullRequest{lone}, "main", fleetAccount, goblinsFor(lone), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act: the run is red on both tries.
	s.step(engine, started.ID)
	stopped, err := engine.Advance(context.Background(), started.ID)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != StateFailed || stopped.Cars[0].State != CarReturned || len(s.told) != 0 {
		t.Fatalf("train %s, cars %v, goblins told %q: want nobody blamed while main is red", stopped.State, carStates(stopped), s.told)
	}
	if !strings.Contains(stopped.Note, "push CI is red") || !strings.Contains(stopped.Note, "go") || strings.Contains(stopped.Note, "frontend") {
		t.Fatalf("note = %q, want main's newest red workflow named and no other", stopped.Note)
	}
	if run := gh.calls[slices.IndexFunc(gh.calls, func(args []string) bool { return args[0] == "run" && args[1] == "list" })]; flagValue(run, "--commit") != started.BaseSHA || flagValue(run, "--branch") != "main" {
		t.Fatalf("gh run list = %v, want main's runs at the commit the run was built on", run)
	}
}
