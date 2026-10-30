package train

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// pushRun is a push run of workflow on main at commit, as gh run list reports
// it, whose page names id.
func pushRun(id int, workflow, commit, status, conclusion string) string {
	return fmt.Sprintf(`{"workflowName":%q,"status":%q,"conclusion":%q,"headSha":%q,"url":"https://github.com/o/r/actions/runs/%d"}`, workflow, status, conclusion, commit, id)
}

func TestDeployHoldRepeatsAfterHalfHour(t *testing.T) {
	t.Parallel()
	// Arrange
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	var notices []string
	engine := Engine{StateDir: t.TempDir(), Now: func() time.Time { return now }, TellCFO: func(_ context.Context, text string) error {
		notices = append(notices, text)
		return nil
	}}
	held := Train{ID: "r-held", Repository: "o/r", Base: "main", State: StateTesting}
	deploy := deployRun{Workflow: "Deploy site", Status: "completed", Conclusion: "failure", URL: "https://github.com/o/r/actions/runs/31"}
	if err := engine.holdForDeploy(context.Background(), &held, deploy); err != nil {
		t.Fatal(err)
	}

	// Act
	now = now.Add(30 * time.Minute)
	if err := engine.holdForDeploy(context.Background(), &held, deploy); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(notices) != 2 {
		t.Fatalf("notices %q: want the unchanged hold repeated after half an hour", notices)
	}
}

func TestDeployHoldChangeWakesImmediately(t *testing.T) {
	t.Parallel()
	// Arrange
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	var notices []string
	engine := Engine{StateDir: t.TempDir(), Now: func() time.Time { return now }, TellCFO: func(_ context.Context, text string) error {
		notices = append(notices, text)
		return nil
	}}
	held := Train{ID: "r-held", Repository: "o/r", Base: "main", State: StateTesting}
	deploy := deployRun{Workflow: "Deploy site", Status: "in_progress", URL: "https://github.com/o/r/actions/runs/31"}
	if err := engine.holdForDeploy(context.Background(), &held, deploy); err != nil {
		t.Fatal(err)
	}

	// Act
	now = now.Add(time.Minute)
	deploy.Status, deploy.Conclusion = "completed", "failure"
	if err := engine.holdForDeploy(context.Background(), &held, deploy); err != nil {
		t.Fatal(err)
	}

	// Assert
	if len(notices) != 2 || !strings.Contains(notices[1], "concluded failure") || held.DeployHold.Status != "completed" || !held.DeployHold.Notified.Equal(now) {
		t.Fatalf("notices %q, hold %+v: want the changed hold told at once", notices, held.DeployHold)
	}
}

func TestDeployHoldEndsWhenTheTrainResumesTestingOrCloses(t *testing.T) {
	t.Parallel()
	for _, ending := range []string{"base moved", "CI runs again", "train closed"} {
		t.Run(ending, func(t *testing.T) {
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
			gh.mainRuns = "[" + pushRun(31, "Deploy site", started.BaseSHA, "completed", "failure") + "]"
			s.step(engine, started.ID)
			switch ending {
			case "base moved":
				s.advanceMain("fix.txt", "fix\n")
			case "CI runs again":
				gh.pending = 1
			case "train closed":
				gh.state[started.PR] = "CLOSED"
			}

			// Act
			resumed := s.step(engine, started.ID)
			notices := len(s.cfo)
			s.now = s.now.Add(31 * time.Minute)
			gh.pending = 1
			s.step(engine, started.ID)

			// Assert
			if resumed.DeployHold != nil || strings.Contains(resumed.Note, "holds its merge") || len(s.cfo) != notices {
				t.Fatalf("train %+v, CFO told %q: want the old hold and its reminders ended", resumed, s.cfo)
			}
		})
	}
}

func TestDeployHoldPersistsAndRemindsUntilItEnds(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"in_progress", "completed"} {
		t.Run(status, func(t *testing.T) {
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
			conclusion := ""
			if status == "completed" {
				conclusion = "failure"
			}
			gh.mainRuns = "[" + pushRun(31, "Deploy site", started.BaseSHA, status, conclusion) + "]"

			// Act
			held := s.step(engine, started.ID)
			encoded, err := json.Marshal(held)
			if err != nil {
				t.Fatal(err)
			}
			var record struct {
				Hold *struct {
					Workflow, Status, Conclusion, URL string
				} `json:"deploy_hold"`
			}
			if err := json.Unmarshal(encoded, &record); err != nil {
				t.Fatal(err)
			}

			// Assert
			if record.Hold == nil || record.Hold.Workflow != "Deploy site" || record.Hold.Status != status || record.Hold.Conclusion != conclusion || record.Hold.URL != "https://github.com/o/r/actions/runs/31" {
				t.Fatalf("train record %s: want the deploy hold exposed to the board", encoded)
			}
			s.now = s.now.Add(30*time.Minute - time.Second)
			unchanged := s.step(s.engine(gh), started.ID)
			if len(s.cfo) != 2 || !unchanged.Updated.Equal(held.Updated) {
				t.Fatalf("CFO told %q, updated %s: want no idle wake or record write", s.cfo, unchanged.Updated)
			}

			// Act: a new engine reads the persisted reminder time at its boundary.
			s.now = s.now.Add(time.Second)
			s.step(s.engine(gh), started.ID)

			// Assert
			if len(s.cfo) != 3 || s.cfo[2] != s.cfo[1] || !strings.Contains(s.cfo[2], "finished successfully") || !strings.Contains(s.cfo[2], "#11") {
				t.Fatalf("CFO told %q: want the hold and what clears it repeated after 30 minutes", s.cfo)
			}
			if len(gh.mergeCalls()) != 0 || len(gh.rerunCalls()) != 0 {
				t.Fatalf("merges %v, reruns %v: want no merge or production rerun", gh.mergeCalls(), gh.rerunCalls())
			}

			// Act: a successful deploy clears the hold and the train lands.
			gh.mainRuns = "[" + pushRun(31, "Deploy site", started.BaseSHA, "completed", "success") + "]"
			landed := s.step(engine, started.ID)
			notices := len(s.cfo)
			s.now = s.now.Add(time.Hour)
			s.step(s.engine(gh), started.ID)
			encoded, err = json.Marshal(landed)
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			if landed.State != StateLanded || strings.Contains(string(encoded), "deploy_hold") || len(s.cfo) != notices {
				t.Fatalf("train %s, CFO told %q: want the hold cleared and its reminders ended", encoded, s.cfo)
			}
		})
	}
}

func TestDeployHoldClearsEvenWhenReadingRidersFails(t *testing.T) {
	t.Parallel()
	// Arrange
	repository := newScratch(t)
	github := newFakeGitHub(repository)
	pullRequest := github.open(11, "feat/a", repository.branch("feat/a", "a.txt", "a\n"))
	riders, _ := Riders([]PullRequest{pullRequest}, "main", fleetAccount, goblinsFor(pullRequest), nil)
	engine := repository.engine(github)
	started, err := engine.Start(context.Background(), repository.repository(), riders)
	if err != nil {
		t.Fatal(err)
	}
	github.mainRuns = "[" + pushRun(31, "Deploy site", started.BaseSHA, "completed", "failure") + "]"
	repository.step(engine, started.ID)
	github.mainRuns = "[" + pushRun(31, "Deploy site", started.BaseSHA, "completed", "success") + "]"
	github.unanswered["list"] = 1

	// Act
	_, advanceError := engine.Advance(context.Background(), started.ID)
	saved, readError := Read(repository.state, started.ID)

	// Assert
	if advanceError == nil || readError != nil || saved.Errors != 1 || len(github.mergeCalls()) != 0 {
		t.Fatalf("advance error %v, read error %v, train %+v: want the rider read failure kept without merging", advanceError, readError, saved)
	}
	if saved.DeployHold != nil || strings.Contains(saved.Note, "holds its merge") {
		t.Fatalf("train %+v: want the successful deploy to clear the board's hold before reading riders", saved)
	}
	landed := repository.step(engine, started.ID)
	if landed.State != StateLanded || !slices.Equal(github.merged, []int{11}) {
		t.Fatalf("train %+v, merged %v: want the next successful rider read to land the train", landed, github.merged)
	}
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
	if stillWaiting.Note != says || len(s.cfo) != 2 || s.cfo[1] != "merge_train: o/r train "+started.ID+" "+says+"; held pull requests: #11" {
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
			if stepped.Note != says || len(s.cfo) != 2 || s.cfo[1] != "merge_train: o/r train "+started.ID+" "+says+"; held pull requests: #11" {
				t.Fatalf("note %q, CFO told %q: want %q said", stepped.Note, s.cfo, says)
			}
		})
	}
}
