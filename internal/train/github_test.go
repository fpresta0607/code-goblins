package train

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// merges says each merge commit in revision's history, oldest first, as its
// subject and the commit it took: "Merge pull request #1 from o/a @<head>".
func (s *scratch) merges(revision string) []string {
	s.t.Helper()
	var merges []string
	for _, line := range strings.Split(s.git(s.remote, "log", "--reverse", "--merges", "--format=%P %s", revision), "\n") {
		if fields := strings.SplitN(line, " ", 3); len(fields) == 3 {
			merges = append(merges, fields[2]+" @"+fields[1])
		}
	}
	return merges
}

// holds says whether revision's history holds commit.
func (s *scratch) holds(revision, commit string) bool {
	return gitCommand(s.remote, "merge-base", "--is-ancestor", commit, revision).Run() == nil
}

// The Overlord, 2026-10-10: "why is my code goblins pr ci alwyas failing".
// One closed pull request in four was a train that had done its job, closed
// without merging, which GitHub draws in red. A train that lands merges its
// own pull request: main takes the commit CI tested, and GitHub marks each
// pull request that rode merged, since main then holds its head.
func TestATrainThatLandsLeavesNoClosedUnmergedPullRequest(t *testing.T) {
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
	if err != nil || landed.State != StateLanded {
		t.Fatalf("train %s (%v), want it landed", landed.State, err)
	}
	if len(gh.closed) != 0 || len(gh.opened) != 1 || gh.reads(gh.trainURL) != "MERGED" {
		t.Fatalf("the train opened %v and closed %v without merging, and its pull request reads %s: want its one pull request merged", gh.opened, gh.closed, gh.reads(gh.trainURL))
	}
	for _, rider := range []PullRequest{first, second, third} {
		if got := gh.reads(rider.URL); got != "MERGED" {
			t.Errorf("#%d reads %s, want it merged", rider.Number, got)
		}
		if !s.hasBranch(rider.HeadRefName) {
			t.Errorf("the branch of #%d was removed, and a train removes no branch but its own", rider.Number)
		}
	}
	if !slices.Equal(gh.merged, []int{11, 12, 13}) {
		t.Fatalf("merged %v, want each in train order", gh.merged)
	}
	if !s.holds("refs/heads/main", started.Head) || s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatal("main does not hold the commit CI tested with exactly its tree")
	}
	want := []string{
		"Merge pull request #11 from o/feat/a @" + first.HeadRefOid,
		"Merge pull request #12 from o/feat/b @" + second.HeadRefOid,
		"Merge pull request #13 from o/feat/c @" + third.HeadRefOid,
		"Merge train " + started.ID + ": #11, #12, #13 @" + started.Head,
	}
	if got := s.merges("refs/heads/main"); !slices.Equal(got, want) {
		t.Fatalf("main's merges = %q, want each pull request by its own number, then the train's: %q", got, want)
	}
	if got := LandedIn(s.git(s.remote, "log", "-1", "--format=%s", "refs/heads/main")); !slices.Equal(got, []string{"11", "12", "13"}) {
		t.Fatalf("the train's merge commit names %q, want each pull request it landed", got)
	}
	if s.hasBranch(started.Branch) || strings.Contains(gh.titles[gh.trainURL], "do not merge") {
		t.Fatalf("the train's branch is kept (%v) or its pull request is titled %q", s.hasBranch(started.Branch), gh.titles[gh.trainURL])
	}
}

// A train that went red says so in one place: the pull request of the run
// that found what breaks it, closed with the failure. The half that passed
// on the way merged, so the halving leaves no second red pull request.
func TestARedTrainSaysSoOnOnePullRequestHoweverOftenItHalves(t *testing.T) {
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

	// Act: all four are red twice, #51 and #52 pass and land, #53 is red twice
	// alone.
	s.step(engine, started.ID)
	halved := s.step(engine, started.ID)
	s.step(engine, started.ID)
	s.step(engine, started.ID)
	stopped := s.step(engine, started.ID)

	// Assert
	if got := carStates(stopped); stopped.State != StateStopped || !slices.Equal(got, []string{"#51=landed", "#52=landed", "#53=culprit", "#54=returned"}) {
		t.Fatalf("train %s: cars %v", stopped.State, got)
	}
	if len(gh.opened) != 2 || gh.reads(gh.opened[0]) != "MERGED" || !slices.Equal(gh.closed, gh.opened[1:]) {
		t.Fatalf("the train opened %v and closed %v: want the green half's pull request merged and the culprit's alone closed", gh.opened, gh.closed)
	}
	if said := gh.comments[0]; !strings.Contains(said, "#53 breaks CI on main") || !strings.Contains(said, "https://github.com/o/r/actions/runs/103/job/1032") || !strings.Contains(said, "landed #51, #52") {
		t.Fatalf("closed saying %q, want the pull request that breaks it, its failed check and what landed", said)
	}
	if got := []string{gh.reads(prs[0].URL), gh.reads(prs[1].URL), gh.reads(prs[2].URL), gh.reads(prs[3].URL)}; !slices.Equal(got, []string{"MERGED", "MERGED", "OPEN", "OPEN"}) {
		t.Fatalf("the pull requests read %v, want the green half merged and the rest left open", got)
	}
	if s.tree("refs/heads/main") != s.tree(halved.Head) || s.hasBranch(started.Branch) {
		t.Fatalf("main's tree is not the green half's, or the train's branch is kept (%v)", s.hasBranch(started.Branch))
	}
}

// A red run can be nobody's fault, and then each half passes alone: both
// merge, and nothing of the train is left red.
func TestATrainWhoseHalvesBothPassLeavesNothingClosedUnmerged(t *testing.T) {
	t.Parallel()
	// Arrange
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
	s.step(engine, started.ID)
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || landed.Runs != 3 {
		t.Fatalf("train %s after %d runs, want it landed on its third", landed.State, landed.Runs)
	}
	if len(gh.closed) != 0 || len(gh.opened) != 2 || gh.reads(gh.opened[0]) != "MERGED" || gh.reads(gh.opened[1]) != "MERGED" {
		t.Fatalf("the train opened %v and closed %v: want a pull request for each half, both merged", gh.opened, gh.closed)
	}
	if gh.reads(first.URL) != "MERGED" || gh.reads(second.URL) != "MERGED" || s.tree("refs/heads/main") != s.tree(landed.Head) || s.hasBranch(started.Branch) {
		t.Fatalf("#71 reads %s and #72 %s, branch kept %v: want both merged, main at the last half's tree and the branch removed", gh.reads(first.URL), gh.reads(second.URL), s.hasBranch(started.Branch))
	}
	if runs := landed.History; runs[0].PR != gh.opened[0] || runs[1].PR != gh.opened[0] || runs[2].PR != gh.opened[1] || landed.PR != gh.opened[1] {
		t.Fatalf("runs %+v, want each run to name the pull request it was tested on", runs)
	}
}

// A train whose one run is red closes its one pull request with the failure,
// and never touches a goblin's pull request or branch.
func TestATrainThatLandsNothingClosesItsPullRequestAndLeavesTheGoblinsAlone(t *testing.T) {
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
	main := s.main()

	// Act: both are red twice, then #61 alone is.
	for range 3 {
		s.step(engine, started.ID)
	}
	stopped := s.step(engine, started.ID)

	// Assert
	if stopped.State != StateStopped || s.main() != main {
		t.Fatalf("train %s, main moved %v: want it stopped with main untouched", stopped.State, s.main() != main)
	}
	if len(gh.opened) != 1 || !slices.Equal(gh.closed, gh.opened) || !strings.Contains(gh.comments[0], "#61 breaks CI on main") || s.hasBranch(started.Branch) {
		t.Fatalf("the train opened %v and closed %v saying %q, branch kept %v: want its one pull request closed with the failure and its branch removed", gh.opened, gh.closed, gh.comments, s.hasBranch(started.Branch))
	}
	for _, rider := range []PullRequest{broken, fine} {
		if gh.reads(rider.URL) != "OPEN" || !s.hasBranch(rider.HeadRefName) {
			t.Errorf("#%d reads %s, branch kept %v: want it open with its branch", rider.Number, gh.reads(rider.URL), s.hasBranch(rider.HeadRefName))
		}
	}
}

// GitHub marks a pull request merged a moment after the push that took its
// head to main, so a train reads again before it says one stayed open.
func TestALandingReadsAgainUntilGitHubShowsEachRiderMerged(t *testing.T) {
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
	gh.marksLate = 1

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || s.waits != 1 || landed.Cars[0].Note != "" || landed.Cars[1].Note != "" {
		t.Fatalf("train %s after %d waits, cars %+v: want it landed after one more read, with nothing to say of a rider", landed.State, s.waits, landed.Cars)
	}
	if len(s.cfo) != 2 || strings.Contains(s.cfo[1], "still shows") {
		t.Fatalf("CFO told %q, want the start and the landing alone", s.cfo)
	}
}

// A rider GitHub still lists open after its train landed is named to the
// CFO with why and what happens to it, and the train is landed all the same:
// main holds what CI tested.
func TestARiderGitHubStillShowsOpenAfterItsTrainLandedIsNamedToTheCFO(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		arrange func(s *scratch, gh *fakeGitHub)
		says    []string
	}{
		"its goblin pushed to it while the train landed": {
			arrange: func(s *scratch, gh *fakeGitHub) {
				gh.whileLanding = func() { s.extend("feat/b", "late.txt", "late\n") }
			},
			says: []string{"main holds the head that rode", "stays open with what was pushed after", "It rides the next train once its checks pass on that head."},
		},
		"GitHub did not mark it merged": {
			arrange: func(_ *scratch, gh *fakeGitHub) { gh.marksLate = mergedReads },
			says:    []string{"GitHub has not marked its pull request merged", "close it by hand if it stays open"},
		},
	} {
		t.Run(name, func(t *testing.T) {
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
			test.arrange(s, gh)

			// Act
			landed := s.step(engine, started.ID)

			// Assert
			if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) || s.tree("refs/heads/main") != s.tree(started.Head) || len(gh.closed) != 0 {
				t.Fatalf("train %s, cars %v, closed %v: want it landed with main at the tree CI tested", landed.State, got, gh.closed)
			}
			if s.waits != mergedReads-1 || len(s.cfo) != 3 {
				t.Fatalf("%d waits, CFO told %q: want every read taken, then the rider named and the landing", s.waits, s.cfo)
			}
			for _, want := range append(test.says, "#12: ") {
				if !strings.Contains(s.cfo[1], want) {
					t.Errorf("CFO told %q, want %q", s.cfo[1], want)
				}
			}
			if !strings.Contains(landed.Cars[1].Note, test.says[0]) || strings.ContainsAny(s.cfo[1], ";\u2014") {
				t.Errorf("#12 keeps %q, CFO told %q: want why on its car and no semicolon or long dash in what he reads", landed.Cars[1].Note, s.cfo[1])
			}
		})
	}
}

// Where GitHub deletes a merged pull request's branch itself, the train's
// branch is already gone when the train ends, which is what it wants.
func TestATrainWhoseBranchGitHubRemovedAtTheMergeEndsWithNothingLeftOver(t *testing.T) {
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
	gh.deletesBranches = true

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || landed.Note != "" || s.hasBranch(started.Branch) {
		t.Fatalf("train %s saying %q, branch kept %v: want it landed with nothing left over", landed.State, landed.Note, s.hasBranch(started.Branch))
	}
}

// olderBuild leaves the running train id as the build before this one kept
// it in flight: no run names a pull request, and its pull request is titled
// as one that is never merged and wears no label.
func olderBuild(t *testing.T, s *scratch, gh *fakeGitHub, id string, change func(*Train)) {
	t.Helper()
	kept, err := Read(s.state, id)
	if err != nil {
		t.Fatal(err)
	}
	for i := range kept.History {
		kept.History[i].PR = ""
	}
	if change != nil {
		change(&kept)
	}
	if err := write(s.state, kept); err != nil {
		t.Fatal(err)
	}
	gh.titles[gh.trainURL] += " (do not merge)"
	delete(gh.wears, gh.trainURL)
}

// A train an older build started is in flight when this build is installed.
// Its next step lands it this build's way: its pull request is told what it
// is before it merges, so nothing merged reads "do not merge".
func TestATrainAnOlderBuildStartedLandsByMergingItsPullRequest(t *testing.T) {
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
	olderBuild(t, s, gh, started.ID, nil)

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || len(gh.closed) != 0 || gh.reads(gh.trainURL) != "MERGED" || gh.titles[gh.trainURL] != "chore(cfo): merge train for PRs #11, #12" {
		t.Fatalf("train %s, closed %v, its pull request reads %s titled %q: want it merged under a title that does not say do not merge", landed.State, gh.closed, gh.reads(gh.trainURL), gh.titles[gh.trainURL])
	}
	if gh.reads(first.URL) != "MERGED" || gh.reads(second.URL) != "MERGED" || s.tree("refs/heads/main") != s.tree(started.Head) || s.hasBranch(started.Branch) || landed.History[0].PR != gh.trainURL {
		t.Fatalf("#11 reads %s and #12 %s, branch kept %v, run %+v: want both merged, main at the tested tree, the branch removed and the run naming its pull request", gh.reads(first.URL), gh.reads(second.URL), s.hasBranch(started.Branch), landed.History[0])
	}
}

// An older build merged each rider by itself, so an install can find a
// landing half done: #11 merged, #12 not. Merging the train's pull request
// lands the rest, and main ends at the tree CI tested.
func TestALandingAnOlderBuildLeftHalfDoneIsFinishedByMergingTheTrain(t *testing.T) {
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
	if result, err := gh.merge(first.URL, first.HeadRefOid, true); err != nil || result.ExitCode != 0 {
		t.Fatalf("the older build's merge of #11: %s, %v", result.Stderr, err)
	}
	olderBuild(t, s, gh, started.ID, func(kept *Train) {
		kept.Landing, kept.Cars[0].State = true, CarLanded
	})

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) || landed.Runs != 1 {
		t.Fatalf("train %s after %d runs: cars %v, want the rest landed with no new CI run", landed.State, landed.Runs, got)
	}
	if !slices.Equal(gh.merged, []int{11, 12}) || len(gh.closed) != 0 || gh.reads(gh.trainURL) != "MERGED" || s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatalf("merged %v, closed %v, its pull request reads %s: want #12 merged by the train's pull request and main at the tested tree", gh.merged, gh.closed, gh.reads(gh.trainURL))
	}
	if !slices.Equal(s.landed, []string{second.URL + " by " + landed.Evidence()}) {
		t.Fatalf("landed = %q, want #12 alone heard of, since the older build told of #11", s.landed)
	}
}

// A push run on main is titled by its commit, so the subject of a train's
// merge commit tells which pull requests that run deploys.
func TestLandedInReadsThePullRequestsOfATrainsMergeCommit(t *testing.T) {
	t.Parallel()
	for subject, want := range map[string][]string{
		"Merge train code-goblins-20261010-124036: #618, #622, #623": {"618", "622", "623"},
		"Merge train r-20261007-160000: #11":                         {"11"},
		"Merge pull request #618 from o/feat/credentials":            nil,
		"Merge train r-20261007-160000: #618, #62...":                nil,
		"fix: the merge train (#12)":                                 nil,
	} {
		// Act
		got := LandedIn(subject)

		// Assert
		if !slices.Equal(got, want) {
			t.Errorf("LandedIn(%q) = %q, want %q", subject, got, want)
		}
	}
}
