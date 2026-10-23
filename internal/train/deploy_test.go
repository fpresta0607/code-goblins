package train

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// pushRun is a push run of workflow on main at commit, as gh run list reports
// it, whose page names id.
func pushRun(id int, workflow, commit, status, conclusion string) string {
	return fmt.Sprintf(`{"workflowName":%q,"status":%q,"conclusion":%q,"headSha":%q,"url":"https://github.com/o/r/actions/runs/%d"}`, workflow, status, conclusion, commit, id)
}

// On 2026-10-22 a train landed at 01:52Z, its push started main's production
// deploy, and at 02:02Z the supervisor started the next train, which merges,
// and starts a second deploy, as soon as its run passes, whether or not the
// first deploy has finished (#1335). A train merges only once main's deploy
// has finished successfully, and says what it waits on.
func TestATrainWaitsForMainsRunningDeployAndMergesOnceItPassed(t *testing.T) {
	t.Parallel()
	// Arrange
	s := newScratch(t)
	gh := newFakeGitHub(s)
	pr := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
	engine := s.engine(gh)
	started, err := engine.Start(context.Background(), s.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	gh.mainRuns = "[" + pushRun(31, "Deploy", started.BaseSHA, "in_progress", "") + "]"

	// Act: the train's run has passed, and main still deploys on two polls.
	waiting := s.step(engine, started.ID)
	stillWaiting := s.step(engine, started.ID)

	// Assert
	if waiting.State != StateTesting || stillWaiting.State != StateTesting || len(gh.mergeCalls()) != 0 || len(gh.merged) != 0 {
		t.Fatalf("train = %s, then %s, merges %v: want it waiting, nothing merged, while main deploys", waiting.State, stillWaiting.State, gh.mergeCalls())
	}
	says := "waits to merge until main's Deploy run at " + started.BaseSHA[:7] + " has finished successfully (https://github.com/o/r/actions/runs/31)"
	if stillWaiting.Note != says || len(s.cfo) != 2 || s.cfo[1] != "merge_train: o/r train "+started.ID+" "+says {
		t.Fatalf("note %q, CFO told %q: want the deploy it waits on said once", stillWaiting.Note, s.cfo)
	}
	read := gh.calls[slices.IndexFunc(gh.calls, func(args []string) bool { return args[0] == "run" && args[1] == "list" })]
	if flagValue(read, "--repo") != "o/r" || flagValue(read, "--branch") != "main" || flagValue(read, "--event") != "push" {
		t.Fatalf("gh run list = %v, want main's push runs read", read)
	}

	// Act: the deploy passes.
	gh.mainRuns = "[" + pushRun(31, "Deploy", started.BaseSHA, "completed", "success") + "]"
	landed := s.step(engine, started.ID)

	// Assert
	if landed.State != StateLanded || !slices.Equal(gh.merged, []int{11}) || s.tree("refs/heads/main") != s.tree(started.Head) {
		t.Fatalf("train = %s, merged %v: want it landed once main's deploy passed", landed.State, gh.merged)
	}
}

func TestATrainMergesOnlyWhileEveryDeployOnMainHasFinishedSuccessfully(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		// isOwnRun lands a lone rider on its own CI run rather than a train
		// run. runs are main's push runs, newest first, at BASE, the commit
		// the train was built on.
		isOwnRun bool
		runs     []string
		// says is the train's note and what the CFO hears while it holds its
		// merge, and "" for a train that lands.
		says string
	}{
		"a deploy waits for a runner": {
			runs: []string{pushRun(31, "Deploy", "BASE", "queued", "")},
			says: "waits to merge until main's Deploy run at BASE has finished successfully (https://github.com/o/r/actions/runs/31)",
		},
		"a lone rider on its own run while main deploys": {
			isOwnRun: true,
			runs:     []string{pushRun(31, "Deploy", "BASE", "in_progress", "")},
			says:     "waits to merge until main's Deploy run at BASE has finished successfully (https://github.com/o/r/actions/runs/31)",
		},
		"an older deploy still runs under a newer one that passed": {
			runs: []string{pushRun(32, "Deploy", "BASE", "completed", "success"), pushRun(31, "Deploy", "BASE", "in_progress", "")},
			says: "waits to merge until main's Deploy run at BASE has finished successfully (https://github.com/o/r/actions/runs/31)",
		},
		"main's newest deploy failed": {
			runs: []string{pushRun(32, "Deploy", "BASE", "completed", "failure"), pushRun(31, "Deploy", "BASE", "completed", "success")},
			says: "holds its merge, since main's Deploy run at BASE concluded failure (https://github.com/o/r/actions/runs/32): run it again or land its fix, and the train merges once main's deploy has finished successfully",
		},
		"main's newest deploy was cancelled": {
			runs: []string{pushRun(32, "Deploy", "BASE", "completed", "cancelled")},
			says: "holds its merge, since main's Deploy run at BASE concluded cancelled (https://github.com/o/r/actions/runs/32): run it again or land its fix, and the train merges once main's deploy has finished successfully",
		},
		"only a workflow that does not deploy still runs": {
			runs: []string{pushRun(33, "go", "BASE", "in_progress", ""), pushRun(32, "Deploy", "BASE", "completed", "success")},
		},
		"main's newest deploy was skipped and the one before passed": {
			runs: []string{pushRun(32, "Deploy", "BASE", "completed", "skipped"), pushRun(31, "Deploy", "BASE", "completed", "success")},
		},
		"main has no deploy": {},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			s := newScratch(t)
			gh := newFakeGitHub(s)
			pr := gh.open(11, "feat/a", s.branch("feat/a", "a.txt", "a\n"))
			repo := s.repository()
			if c.isOwnRun {
				pr = provenRider(s, gh, 11)
				repo = landsLoneRider(s)
			}
			riders, _ := Riders([]PullRequest{pr}, "main", fleetAccount, goblinsFor(pr), nil)
			engine := s.engine(gh)
			started, err := engine.Start(context.Background(), repo, riders)
			if err != nil {
				t.Fatal(err)
			}
			gh.mainRuns = strings.ReplaceAll("["+strings.Join(c.runs, ",")+"]", "BASE", started.BaseSHA)

			// Act
			stepped := s.step(engine, started.ID)

			// Assert
			if c.isOwnRun && started.History[0].OwnRun == "" {
				t.Fatalf("run = %+v, want it standing on the rider's own CI run", started.History[0])
			}
			if c.says == "" {
				if stepped.State != StateLanded || !slices.Equal(gh.merged, []int{11}) {
					t.Fatalf("train = %s, merged %v, note %q: want it landed", stepped.State, gh.merged, stepped.Note)
				}
				return
			}
			says := strings.ReplaceAll(c.says, "BASE", started.BaseSHA[:7])
			if stepped.State != StateTesting || len(gh.mergeCalls()) != 0 || len(gh.merged) != 0 {
				t.Fatalf("train = %s, merges %v: want its merge held", stepped.State, gh.mergeCalls())
			}
			if stepped.Note != says || len(s.cfo) != 2 || s.cfo[1] != "merge_train: o/r train "+started.ID+" "+says {
				t.Fatalf("note %q, CFO told %q: want %q said", stepped.Note, s.cfo, says)
			}
		})
	}
}
