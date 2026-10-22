package train

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// riderRun is a pull request's own CI run as the fake GitHub shows it: the
// workflow run behind its one check, the merge commit that run's checkout
// fetched for pull request number, how its check concluded, SUCCESS unless
// set, and any check beside it.
type riderRun struct {
	run        int
	merge      string
	number     int
	conclusion string
	extra      []Check
}

// riderView answers gh pr view for a pull request with its own run: its head
// as the remote has it, and its checks.
func (f *fakeGitHub) riderView(url string) (execx.Result, error) {
	own := f.ownRuns[url]
	head := f.s.git(f.s.remote, "rev-parse", "refs/heads/"+f.pulls[url].branch)
	checks := append([]Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: cmp.Or(own.conclusion, "SUCCESS"), DetailsURL: fmt.Sprintf("https://github.com/o/r/actions/runs/%d/job/%d", own.run, own.run*10)}}, own.extra...)
	data, err := json.Marshal(map[string]any{"headRefOid": head, "statusCheckRollup": checks})
	return execx.Result{Stdout: data}, err
}

// actionsAPI answers the reads of a pull request's own run: its jobs, and the
// log of its one job, whose checkout fetched the run's merge commit.
func (f *fakeGitHub) actionsAPI(path string) (execx.Result, error) {
	for _, own := range f.ownRuns {
		switch path {
		case fmt.Sprintf("repos/o/r/actions/runs/%d/jobs?per_page=100", own.run):
			return execx.Result{Stdout: []byte(fmt.Sprintf(`{"jobs":[{"id":%d,"conclusion":"success"}]}`, own.run*10))}, nil
		case fmt.Sprintf("repos/o/r/actions/jobs/%d/logs", own.run*10):
			log := fmt.Sprintf("2026-10-21T22:33:35.3822820Z [command]/usr/bin/git -c protocol.version=2 fetch --no-tags --prune --no-recurse-submodules origin +refs/heads/*:refs/remotes/origin/* +refs/tags/*:refs/tags/* +%s:refs/remotes/pull/%d/merge\n2026-10-21T22:33:57.1352054Z HEAD is now at %s Merge\n2026-10-21T22:41:38.7372811Z ========= 12 passed in 1.00s =========\n", own.merge, own.number, own.merge[:9])
			return execx.Result{Stdout: []byte(log)}, nil
		}
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("gh: Not Found (HTTP 404)")}, nil
}

// pullMerge makes the merge commit GitHub keeps at refs/pull/<number>/merge:
// head merged onto main as main is now, main its first parent.
func (s *scratch) pullMerge(number int, head string) string {
	s.t.Helper()
	s.git(s.github, "fetch", "-q", "origin")
	s.git(s.github, "checkout", "-q", "--detach", "origin/main")
	s.git(s.github, "merge", "--no-ff", "-q", "-m", fmt.Sprintf("Merge %s into %s", head, s.main()), head)
	s.git(s.github, "push", "-q", "-f", "origin", fmt.Sprintf("HEAD:refs/pull/%d/merge", number))
	return s.git(s.github, "rev-parse", "HEAD")
}

// provenRider opens pull request number, green, with its own CI run that
// tested its head merged onto main as main is now.
func provenRider(s *scratch, gh *fakeGitHub, number int) PullRequest {
	branch := fmt.Sprintf("feat/%d", number)
	head := s.branch(branch, fmt.Sprintf("f%d.txt", number), branch+"\n")
	pr := gh.open(number, branch, head)
	gh.ownRuns[pr.URL] = &riderRun{run: 700 + number, merge: s.pullMerge(number, head), number: number}
	return pr
}

// landsLoneRider is the scratch repository, letting a train of one pull
// request land it on its own run.
func landsLoneRider(s *scratch) Repository {
	repo := s.repository()
	repo.LandsLoneRider = true
	return repo
}

func TestALoneRiderWhoseOwnRunTestedTodaysMainLandsWithNoTrainRun(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := provenRider(s, gh, 5)
	merge := gh.ownRuns[pr.URL].merge
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), landsLoneRider(s), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if gh.created != 0 || s.hasBranch(started.Branch) {
		t.Fatalf("opened %d pull requests, pushed its branch %v: want no train run at all", gh.created, s.hasBranch(started.Branch))
	}
	if landed.State != StateLanded || !slices.Equal(runLog(landed), []string{"1 #5 landed"}) {
		t.Fatalf("train = %s, runs %q: want it landed on its one run", landed.State, runLog(landed))
	}
	ownRun := "https://github.com/o/r/actions/runs/705"
	if run := landed.History[0]; run.OwnRun != ownRun || run.Head != merge || run.PR != pr.URL || run.Base != started.BaseSHA {
		t.Fatalf("run = %+v, want it standing on %s, the merge %s it tested, on the rider's own pull request", run, ownRun, merge)
	}
	if got := gh.mergeCalls(); !slices.Equal(got, []string{pr.URL + "@" + pr.HeadRefOid}) || !slices.Equal(gh.merged, []int{5}) {
		t.Fatalf("merges = %v, merged %v: want the rider's own pull request merged once, pinned to its head", got, gh.merged)
	}
	if s.tree("refs/heads/main") != s.tree(merge) {
		t.Fatal("main's tree differs from the tree the rider's own run tested")
	}
	evidence := "merge train " + started.ID + ": CI passed on " + ownRun + ", which tested " + pr.URL + " at " + pr.HeadRefOid[:7] + " merged onto main at " + started.BaseSHA[:7] + ", the tree that lands, so no train run tested it again"
	if !slices.Equal(s.landed, []string{pr.URL + " by " + evidence}) {
		t.Fatalf("landed = %q, want the rider heard of with its own run as the evidence", s.landed)
	}
	if body := mergeBody(gh, pr.URL); body != evidence {
		t.Fatalf("the merge was asked with body %q, want the evidence", body)
	}
	if len(s.cfo) != 2 || !strings.Contains(s.cfo[0], "lands #5 on its own CI run ("+ownRun+")") || !strings.Contains(s.cfo[1], "#5 landed on its own CI run ("+ownRun+")") {
		t.Fatalf("CFO told %q, want the start and the landing on its own run", s.cfo)
	}
}

// mergeBody is the body gh was asked to merge the pull request at url with.
func mergeBody(gh *fakeGitHub, url string) string {
	for _, args := range gh.calls {
		if len(args) > 2 && args[0] == "pr" && args[1] == "merge" && args[2] == url {
			return flagValue(args, "--body")
		}
	}
	return ""
}

func TestALoneRiderWhoseOwnRunDoesNotProveTheTreeThatWouldLandGetsATrainRun(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		isOptedOut bool
		arrange    func(s *scratch, gh *fakeGitHub, pr PullRequest)
	}{
		"its repository leaves it off": {isOptedOut: true},
		"its merge ref's first parent is an older main": {arrange: func(s *scratch, _ *fakeGitHub, _ PullRequest) {
			s.advanceMain("other.txt", "other\n")
		}},
		"GitHub made its merge ref again on today's main after its run": {arrange: func(s *scratch, _ *fakeGitHub, pr PullRequest) {
			s.advanceMain("other.txt", "other\n")
			s.pullMerge(pr.Number, pr.HeadRefOid)
		}},
		"its run checked out another pull request's merge": {arrange: func(_ *scratch, gh *fakeGitHub, pr PullRequest) {
			gh.ownRuns[pr.URL].number = 6
		}},
		"its run's check failed": {arrange: func(_ *scratch, gh *fakeGitHub, pr PullRequest) {
			gh.ownRuns[pr.URL].conclusion = "FAILURE"
		}},
		"a check no workflow run stands behind sits beside it": {arrange: func(_ *scratch, gh *fakeGitHub, pr PullRequest) {
			gh.ownRuns[pr.URL].extra = []Check{{Kind: "StatusContext", Context: "scan", State: "SUCCESS", TargetURL: "https://scan.example/report"}}
		}},
		"its head moved since its run": {arrange: func(s *scratch, _ *fakeGitHub, pr PullRequest) {
			s.extend(pr.HeadRefName, "more.txt", "more\n")
		}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			pr := provenRider(s, gh, 5)
			if c.arrange != nil {
				c.arrange(s, gh, pr)
			}
			repo := landsLoneRider(s)
			repo.LandsLoneRider = !c.isOptedOut
			riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)

			// Act
			started, err := s.engine(gh).Start(context.Background(), repo, riders)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if gh.created != 1 || started.PR != gh.trainURL || !s.hasBranch(started.Branch) || started.History[0].OwnRun != "" {
				t.Fatalf("opened %d pull requests, run %+v: want a train run pushed and opened, standing on nothing else", gh.created, started.History)
			}
			if len(gh.merged) != 0 || slices.ContainsFunc(gh.mergeCalls(), func(call string) bool { return strings.HasPrefix(call, pr.URL) }) {
				t.Fatalf("merged %v, merge calls %v: want nothing landed before the train's run", gh.merged, gh.mergeCalls())
			}
		})
	}
}

func TestALoneRiderNeverLandsOnItsOwnRunOnceMainMovedPastItsMergeRef(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := provenRider(s, gh, 5)
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), landsLoneRider(s), riders)
	if err != nil {
		t.Fatal(err)
	}
	moved := s.advanceMain("other.txt", "other\n")

	// Act
	tested := s.step(engine, started.ID)

	// Assert
	if !slices.Equal(runLog(tested), []string{"1 #5 moved", "2 #5 open"}) || tested.History[1].OwnRun != "" || tested.BaseSHA != moved {
		t.Fatalf("runs %q (%+v): want the run on its own CI run ended as moved, and a train run on the new main", runLog(tested), tested.History)
	}
	if gh.created != 1 || len(gh.merged) != 0 {
		t.Fatalf("opened %d, merged %v: want the train's own run opened and nothing landed", gh.created, gh.merged)
	}
}

func TestTwoRidersGetATrainRunEvenWhenEachOwnRunIsProven(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	first, second := provenRider(s, gh, 5), provenRider(s, gh, 6)
	riders, _ := Riders([]PullRequest{first, second}, "main", fleetAccount, goblinsFor(first, second), nil)

	// Act
	started, err := s.engine(gh).Start(context.Background(), landsLoneRider(s), riders)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if gh.created != 1 || !slices.Equal(runLog(started), []string{"1 #5 #6 open"}) || started.History[0].OwnRun != "" {
		t.Fatalf("opened %d, runs %q: want one train run testing both", gh.created, runLog(started))
	}
}

func TestALoneRiderLandsOnItsOwnRunWhenTheMergesAnswerIsLost(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := provenRider(s, gh, 5)
	gh.lost["merge "+pr.URL] = true
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), landsLoneRider(s), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || !slices.Equal(gh.merged, []int{5}) || len(gh.mergeCalls()) != 1 {
		t.Fatalf("train = %s, merged %v, merge calls %v: want the merge found on main and taken as landed, asked once", landed.State, gh.merged, gh.mergeCalls())
	}
}

func TestALoneRiderGitHubRefusesToMergeLandsNothingAndIsTriedAgain(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := provenRider(s, gh, 5)
	gh.refuse[pr.URL] = "Pull request is not mergeable"
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), landsLoneRider(s), riders)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	refused, refusal := engine.Advance(context.Background(), started.ID)
	delete(gh.refuse, pr.URL)
	landed := s.step(engine, started.ID)

	// Assert
	if refusal == nil || refused.Landing || refused.IsFinished() || refused.Errors != 1 {
		t.Fatalf("train = %+v, error %v: want the refusal counted, nothing landing and the train running", refused, refusal)
	}
	if landed.State != StateLanded || !slices.Equal(gh.merged, []int{5}) {
		t.Fatalf("train = %s, merged %v: want it landed on the next step", landed.State, gh.merged)
	}
}
