package train

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
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
	if len(s.ceo) != 2 || strings.Contains(s.ceo[1], "still shows") {
		t.Fatalf("CEO told %q, want the start and the landing alone", s.ceo)
	}
}

// A rider GitHub still lists open after its train landed is named to the
// CEO with why and what happens to it, and the train is landed all the same:
// main holds what CI tested.
func TestARiderGitHubStillShowsOpenAfterItsTrainLandedIsNamedToTheCEO(t *testing.T) {
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
			if s.waits != mergedReads-1 || len(s.ceo) != 3 {
				t.Fatalf("%d waits, CEO told %q: want every read taken, then the rider named and the landing", s.waits, s.ceo)
			}
			for _, want := range append(test.says, "#12: ") {
				if !strings.Contains(s.ceo[1], want) {
					t.Errorf("CEO told %q, want %q", s.ceo[1], want)
				}
			}
			if !strings.Contains(landed.Cars[1].Note, test.says[0]) || strings.ContainsAny(s.ceo[1], ";\u2014") {
				t.Errorf("#12 keeps %q, CEO told %q: want why on its car and no semicolon or long dash in what he reads", landed.Cars[1].Note, s.ceo[1])
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
	if landed.State != StateLanded || len(gh.closed) != 0 || gh.reads(gh.trainURL) != "MERGED" || gh.titles[gh.trainURL] != "chore(ceo): merge train for PRs #11, #12" {
		t.Fatalf("train %s, closed %v, its pull request reads %s titled %q: want it merged under a title that does not say do not merge", landed.State, gh.closed, gh.reads(gh.trainURL), gh.titles[gh.trainURL])
	}
	if gh.reads(first.URL) != "MERGED" || gh.reads(second.URL) != "MERGED" || s.tree("refs/heads/main") != s.tree(started.Head) || s.hasBranch(started.Branch) || landed.History[0].PR != gh.trainURL {
		t.Fatalf("#11 reads %s and #12 %s, branch kept %v, run %+v: want both merged, main at the tested tree, the branch removed and the run naming its pull request", gh.reads(first.URL), gh.reads(second.URL), s.hasBranch(started.Branch), landed.History[0])
	}
}

// A partially completed landing has a different base, so the remaining
// source is validated there before it merges.
func TestAPartialLandingValidatesTheNewBaseBeforeMergingTheRest(t *testing.T) {
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
	rebuilt := s.step(engine, started.ID)
	if rebuilt.Runs != 2 || rebuilt.State != StateTesting || !slices.Equal(gh.merged, []int{11}) {
		t.Fatalf("train %+v, merged %v: want the new base validated before the rest lands", rebuilt, gh.merged)
	}
	landed := s.step(engine, started.ID)

	// Assert
	if got := carStates(landed); landed.State != StateLanded || !slices.Equal(got, []string{"#11=landed", "#12=landed"}) || landed.Runs != 2 {
		t.Fatalf("train %s after %d runs: cars %v, want the rest landed after validation on its actual base", landed.State, landed.Runs, got)
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

func TestNativeCheckProjectionRetainsIdentitiesAndRefusesIncompleteRollup(t *testing.T) {
	for _, completeness := range []string{"complete", "truncated", "missing page", "missing flag"} {
		t.Run(completeness, func(t *testing.T) {
			// Arrange
			data, err := os.ReadFile("testdata/native-attestation-pr.json")
			if err != nil {
				t.Fatal(err)
			}
			var raw GraphPullRequest
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			contexts := &raw.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts
			switch completeness {
			case "truncated":
				*contexts.PageInfo.HasNextPage = true
			case "missing page":
				contexts.PageInfo = nil
			case "missing flag":
				contexts.PageInfo.HasNextPage = nil
			}
			client := githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				query, _, err := githubtest.Query(r)
				if err != nil {
					t.Fatal(err)
				}
				repository := map[string]any{}
				if strings.Contains(query, "pullRequests(") {
					repository["pullRequests"] = map[string]any{"nodes": []GraphPullRequest{raw}}
				} else {
					repository["pullRequest"] = raw
				}
				githubtest.Answer(w, map[string]any{"repository": repository})
			}))
			// Act
			listed, listErr := ListPullRequests(context.Background(), client, "fpresta0607/no-mistakes", "main", "")
			viewed, viewErr := viewPullRequest(context.Background(), client, raw.URL)
			// Assert
			if completeness != "complete" {
				if listErr != nil || viewErr != nil || len(listed) != 1 {
					t.Fatalf("lost lifecycle metadata: list %v, view %v", listErr, viewErr)
				}
				pr := listed[0].PullRequest()
				riders, _ := Riders([]PullRequest{pr}, "main", pr.Author.Login, goblinsFor(pr), nil)
				if !pr.IsCheckRollupIncomplete || len(riders) != 0 {
					t.Fatal("incomplete rollup admitted a rider")
				}
				_, _, _, _, ciErr := (Engine{GitHub: client}).ci(context.Background(), Train{PR: raw.URL, Head: raw.HeadRefOid})
				if ciErr == nil || !strings.Contains(ciErr.Error(), "incomplete check rollup") {
					t.Fatalf("incomplete CI proof: %v", ciErr)
				}
				proof, merge, checks := (Engine{GitHub: client}).ownRun(context.Background(), Train{}, Car{URL: raw.URL, Head: raw.HeadRefOid}, raw.HeadRefOid)
				if proof != "" || merge != "" || len(checks) != 0 {
					t.Fatal("incomplete rollup authorized own-run proof")
				}
				return
			}
			if listErr != nil || viewErr != nil || len(listed) != 1 {
				t.Fatalf("list %v, view %v", listErr, viewErr)
			}
			for _, projected := range []GraphPullRequest{listed[0], viewed} {
				pr := projected.PullRequest()
				if len(pr.Checks) != 23 {
					t.Fatalf("lost check multiplicity: %+v", pr.Checks)
				}
				proof, _ := nativeRiderAttestation(pr, nil)
				if proof == nil || proof.DatabaseID != 127956358474 || proof.SuiteID != 114442038361 || proof.RunID != 42236780731 || proof.WorkflowID != 377882636 || proof.AppID != 15368 || proof.RunAttempt != 1 {
					t.Fatalf("lost provenance: %+v", proof)
				}
			}
		})
	}
}

func TestNativeAttestationBindsActualRunAndWorkflowIdentity(t *testing.T) {
	for _, test := range []struct {
		name, target, key string
		value             any
		shouldPass        bool
	}{
		{name: "dynamic names and absent event action", shouldPass: true},
		{"run ID", "run", "id", int64(42237139244), false},
		{"run node", "run", "node_id", "foreign", false},
		{"suite ID", "run", "check_suite_id", int64(114443026635), false},
		{"suite node", "run", "check_suite_node_id", "foreign", false},
		{"run workflow ID", "run", "workflow_id", 1, false},
		{"run attempt", "run", "run_attempt", 2, false},
		{"run file", "run", "path", ".github/workflows/test.yml", false},
		{"event", "run", "event", "push", false},
		{"head", "run", "head_sha", "other", false},
		{"repository", "run", "repository", map[string]string{"full_name": "other/repo"}, false},
		{"workflow ID", "workflow", "id", 1, false},
		{"workflow node", "workflow", "node_id", "foreign", false},
		{"workflow name", "workflow", "name", "Other workflow", false},
		{"workflow path", "workflow", "path", ".github/workflows/test.yml", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			pr := capturedNativePull(t)
			check, _ := nativeRiderAttestation(pr, nil)
			if check == nil {
				t.Fatal("missing native witness")
			}
			responses := map[string]map[string]json.RawMessage{}
			for _, kind := range []string{"run", "workflow"} {
				data, err := os.ReadFile("testdata/native-attestation-" + kind + ".json")
				if err != nil {
					t.Fatal(err)
				}
				var response map[string]json.RawMessage
				if err := json.Unmarshal(data, &response); err != nil {
					t.Fatal(err)
				}
				responses[kind] = response
			}
			if test.target != "" {
				value, err := json.Marshal(test.value)
				if err != nil {
					t.Fatal(err)
				}
				responses[test.target][test.key] = value
			}
			engine := Engine{GitHub: githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/fpresta0607/no-mistakes/actions/runs/42236780731":
					githubtest.Write(w, http.StatusOK, responses["run"])
				case "/repos/fpresta0607/no-mistakes/actions/workflows/377882636":
					githubtest.Write(w, http.StatusOK, responses["workflow"])
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					githubtest.Refuse(w, http.StatusNotFound, "unexpected request")
				}
			}))}
			// Act
			matched, err := engine.isNativePRCheck(context.Background(), Train{Repository: "fpresta0607/no-mistakes"}, *check, pr.HeadRefOid)
			// Assert
			if err != nil || matched != test.shouldPass {
				t.Fatalf("matched %v, error %v", matched, err)
			}
		})
	}
}

func TestNativeCheckProjectionPreservesOrdinarySavedProof(t *testing.T) {
	// Arrange: old source proof had no native-only identity fields.
	data, err := os.ReadFile("testdata/native-attestation-pr.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw GraphPullRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	entry := raw.Rollup()[2]
	entry.Name = "ordinary source check"
	expected := Check{Kind: entry.Kind, Name: entry.Name, Status: entry.Status, Conclusion: entry.Conclusion, DetailsURL: entry.DetailsURL, WorkflowName: entry.WorkflowName(), StartedAt: entry.StartedAt, CompletedAt: entry.CompletedAt}
	// Act / Assert
	if got := entry.Check(); got != expected {
		t.Fatalf("native identity extension changed unrelated saved source proof: %+v", got)
	}
}

func TestNativeCheckProjectionReconcilesClosedPRBeforeIncompleteChecks(t *testing.T) {
	// Arrange: closure ends the run even when its old rollup cannot prove CI.
	data, err := os.ReadFile("testdata/native-attestation-pr.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw GraphPullRequest
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw.State = "CLOSED"
	*raw.Commits.Nodes[0].Commit.StatusCheckRollup.Contexts.PageInfo.HasNextPage = true
	client := githubtest.Client(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubtest.Answer(w, map[string]any{"repository": map[string]any{"pullRequest": raw}})
	}))
	// Act
	viewed, err := viewPullRequest(context.Background(), client, raw.URL)
	// Assert
	if err != nil || viewed.State != "CLOSED" {
		t.Fatalf("closed PR was held by obsolete checks: %+v, %v", viewed, err)
	}
	engine := Engine{GitHub: client}
	outcome, _, _, _, err := engine.ci(context.Background(), Train{PR: raw.URL, Head: raw.HeadRefOid})
	if err != nil || outcome != "closed" {
		t.Fatalf("closure outcome %q, %v", outcome, err)
	}
	raw.State = "OPEN"
	outcome, _, _, _, err = engine.ci(context.Background(), Train{PR: raw.URL, Head: "different-head"})
	if err != nil || outcome != "stale" {
		t.Fatalf("head transition outcome %q, %v", outcome, err)
	}
}
