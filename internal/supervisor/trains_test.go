package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainProject is a checkout whose origin names GitHub's o/r while git
// reaches a scratch remote in its place, with a clone that pushes branches.
type trainProject struct {
	t        *testing.T
	checkout string
	pusher   string
	created  int
	// viewerReads counts the reads of the account gh works as.
	viewerReads int
}

func newTrainProject(t *testing.T) *trainProject {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	// No git here starts gc or maintenance in the background, whose child
	// keeps the parent's output pipe open on Windows.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	t.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(name, "Train Test")
	}
	for _, name := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "train@example.test")
	}
	root, err := fsx.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &trainProject{t: t, checkout: filepath.Join(root, "app"), pusher: filepath.Join(root, "pusher")}
	remote := filepath.Join(root, "origin.git")
	p.git(root, "init", "-q", "--bare", "--initial-branch=main", remote)
	p.git(root, "clone", "-q", remote, p.pusher)
	p.git(p.pusher, "commit", "-q", "--allow-empty", "-m", "seed")
	p.git(p.pusher, "push", "-q", "origin", "HEAD:main")
	p.git(root, "clone", "-q", remote, p.checkout)
	p.git(p.checkout, "remote", "set-url", "origin", "https://github.com/o/r.git")
	p.git(p.checkout, "config", "url."+filepath.ToSlash(remote)+".insteadOf", "https://github.com/o/r.git")
	return p
}

func (p *trainProject) git(dir string, args ...string) string {
	p.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		p.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pull pushes a branch with one file of its own and lists it as green pull
// request number.
func (p *trainProject) pull(number int) train.PullRequest {
	branch := fmt.Sprintf("feat/%d", number)
	p.git(p.pusher, "checkout", "-q", "-B", branch, "origin/main")
	p.git(p.pusher, "commit", "-q", "--allow-empty", "-m", branch)
	p.git(p.pusher, "push", "-q", "origin", branch)
	pr := train.PullRequest{
		Number: number, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), HeadRefName: branch,
		HeadRefOid: p.git(p.pusher, "rev-parse", "HEAD"), BaseRefName: "main", Mergeable: "MERGEABLE",
		Checks: []train.Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}
	pr.Author.Login = "fleet"
	return pr
}

// Run runs git for real, answers gh api user with the fleet's account and gh
// pr create with the train's pull request; any other gh call is unexpected.
func (p *trainProject) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" {
		return execx.OSRunner{}.Run(ctx, request)
	}
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "api" && request.Args[1] == "user" {
		p.viewerReads++
		return execx.Result{Stdout: []byte("fleet\n")}, nil
	}
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "pr" && request.Args[1] == "create" {
		p.created++
		return execx.Result{Stdout: []byte("https://github.com/o/r/pull/900\n")}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected " + request.Name + " " + strings.Join(request.Args, " "))}, nil
}

// reportDone records a live goblin id of project whose status log ends
// with lines.
func reportDone(t *testing.T, h home.Home, id, project string, lines ...string) {
	t.Helper()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Project: project, Harness: "claude", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		if err := state.AppendStatus(h.State, id, line); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheSupervisorStartsATrainWhenTwoGoblinsFinishedPullRequestsWaitGreen(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(11), project.pull(12)
	reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)

	// Act
	err := service.runTrain(context.Background(), project, project.checkout, []train.PullRequest{first, second})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	trains, err := train.List(h.State)
	if err != nil || len(trains) != 1 {
		t.Fatalf("trains = %+v, %v, want one", trains, err)
	}
	if running := trains[0]; running.State != train.StateTesting || running.Repository != "o/r" || running.Base != "main" || len(running.Cars) != 2 || running.Cars[0].Task != "g11" {
		t.Fatalf("train = %+v, want #11 and #12 testing on o/r's main", running)
	}
	if wakes := prWakes(t, h, "merge_train"); len(wakes) != 1 || wakes[0].Key != "train:o/r" || !strings.Contains(wakes[0].Detail, "#11, #12") {
		t.Fatalf("pr wakes = %+v, want the train's start", wakes)
	}
}

// Tonight's goblins: each reported its pull request done and went on to its
// next one, which is what a goblin with more to build does. A pull request a
// goblin reported done in its current run rides whatever it reported after.
func TestTheSupervisorStartsATrainForGoblinsThatWentOnWorkingAfterTheirDoneReport(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(465), project.pull(467)
	reportDone(t, h, "cg-harness-capacity", project.checkout, "done: PR "+first.URL, "working: building its third pull request")
	reportDone(t, h, "cg-dev-drive", project.checkout, "done: PR "+second.URL, "working: building its last pull request", "blocked: which drive letter?")

	// Act
	err := service.runTrain(context.Background(), project, project.checkout, []train.PullRequest{first, second})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	trains, err := train.List(h.State)
	if err != nil || len(trains) != 1 {
		t.Fatalf("trains = %+v, %v, want one", trains, err)
	}
	if cars := trains[0].Cars; len(cars) != 2 || cars[0].Number != 465 || cars[1].Number != 467 {
		t.Fatalf("cars = %+v, want #465 and #467", cars)
	}
}

func TestTheSupervisorLeavesALoneFinishedPullRequestToTheCFO(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, working := project.pull(21), project.pull(22)
	reportDone(t, h, "g21", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g22", project.checkout, "working: building #22, not done with it yet")

	// Act
	err := service.runTrain(context.Background(), project, project.checkout, []train.PullRequest{first, working})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if trains, _ := train.List(h.State); len(trains) != 0 || project.created != 0 || project.viewerReads != 0 {
		t.Fatalf("trains = %+v, train pull requests opened %d, account reads %d: want none for one finished pull request", trains, project.created, project.viewerReads)
	}
}

func TestATrainsMergesReachAFKModesLogOnlyWhileItIsOn(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	engine := service.trainEngine(execx.OSRunner{}, "o/r")
	landed := train.Train{ID: "r-20261007-160000", PR: "https://github.com/o/r/pull/900", Head: "abcdef1234", Base: "main", BaseSHA: "1234567890"}
	off, on := train.Car{Number: 10, URL: "https://github.com/o/r/pull/10"}, train.Car{Number: 11, URL: "https://github.com/o/r/pull/11"}
	engine.Landed(context.Background(), landed, off)
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	engine.Landed(context.Background(), landed, on)

	// Assert
	switched, err := afk.Read(h.State)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := afk.Entries(h.State, switched.Session)
	if err != nil {
		t.Fatal(err)
	}
	merges := afk.Decisions(entries)
	if len(merges) != 1 || merges[0].Kind != afk.KindMerge || merges[0].What != on.URL || merges[0].Outcome != afk.OutcomeMerged || merges[0].Evidence != landed.Evidence() {
		t.Fatalf("AFK decisions = %+v, want #11 merged with the train's evidence and nothing from before it was on", merges)
	}
}

// Every pull request a goblin reported done in its current run is read back
// with when it was first reported done, whatever the goblin reported after:
// working on its next pull request, being blocked, or reporting it again. A
// report from before the run began is the last run's.
func TestDoneReportsReadEveryPullRequestReportedDoneInThisRun(t *testing.T) {
	// Arrange
	spawned := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	line := func(minute int, event string) string {
		return spawned.Add(time.Duration(minute)*time.Minute).Format(time.RFC3339) + " " + event
	}
	lines := []string{
		line(-5, "done: PR https://github.com/o/r/pull/9"),
		line(1, "done: PR https://github.com/o/r/pull/1"),
		line(2, "working: back at it"),
		line(3, "done: PR https://github.com/o/r/pull/2"),
		line(4, "notify-handled: {}"),
		line(5, "blocked: which base?"),
		line(6, "done: PR https://github.com/o/r/pull/3 ready"),
		line(7, "done: PR https://github.com/o/r/pull/2"),
		line(8, "working: building the next pull request"),
	}

	// Act
	done := doneReports(lines, spawned)

	// Assert
	want := map[string]time.Time{
		"https://github.com/o/r/pull/1": spawned.Add(time.Minute),
		"https://github.com/o/r/pull/2": spawned.Add(3 * time.Minute),
		"https://github.com/o/r/pull/3": spawned.Add(6 * time.Minute),
	}
	if len(done) != len(want) {
		t.Fatalf("done = %v, want %v", done, want)
	}
	for url, at := range want {
		if !done[url].Equal(at) {
			t.Errorf("%s done at %v, want %v, its first report of the run", url, done[url], at)
		}
	}
}

// A done report rides however much its goblin reported after it in the same
// run: a long run's status log is read whole, never only its newest lines.
func TestTrainGoblinsReadADoneReportHoweverFarBackInTheRun(t *testing.T) {
	// Arrange
	_, h := fleetService(t)
	checkout := t.TempDir()
	finished := "https://github.com/o/r/pull/31"
	lines := []string{"done: PR " + finished}
	for step := 1; step <= 250; step++ {
		lines = append(lines, fmt.Sprintf("working: step %d of the next pull request", step))
	}
	reportDone(t, h, "g31", checkout, lines...)

	// Act
	goblins := TrainGoblins(h.State, checkout)

	// Assert
	if len(goblins) != 1 || len(goblins[0].Done) != 1 {
		t.Fatalf("goblins = %+v, want g31 with #31 done", goblins)
	}
	if _, isDone := goblins[0].Done[finished]; !isDone {
		t.Fatalf("goblins = %+v, want #31 done", goblins)
	}
}

func TestPRHealthLeavesTrainsAndThePullRequestsTheyCarryAlone(t *testing.T) {
	// Arrange
	_, h, forge, now := healthService(t, false)
	behind := func(number int, branch, head string) string {
		return fmt.Sprintf(`{"number":%d,"url":"https://github.com/o/r/pull/%d","headRefName":%q,"headRefOid":%q,"baseRefName":"main","mergeable":"MERGEABLE","author":{"login":"teammate"},"statusCheckRollup":[]}`, number, number, branch, head)
	}
	forge.pulls = "[" + behind(209, "feat/wakes", "head-one") + "," + behind(900, "cfo/train-20261002-115000", "head-two") + "," + behind(210, "feat/other", "head-three") + "]"
	forge.comparisons = `{"data":{"repository":{"ref":{` +
		`"pr209":{"behindBy":2,"headTarget":{"oid":"head-one"},"baseTarget":{"oid":"base-head"}},` +
		`"pr900":{"behindBy":3,"headTarget":{"oid":"head-two"},"baseTarget":{"oid":"base-head"}},` +
		`"pr210":{"behindBy":4,"headTarget":{"oid":"head-three"},"baseTarget":{"oid":"base-head"}}}}}}`
	running := train.Train{Schema: train.Schema, ID: "r-20261002-115000", Repository: "o/r", State: train.StateTesting, Cars: []train.Car{{Number: 209, URL: "https://github.com/o/r/pull/209", State: train.CarWaiting}}}
	data, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(train.Dir(h.State), running.ID+".json"), string(data))
	w := fleetWakes{}

	// Act
	listed, unreadable, err := pollPullRequests(context.Background(), forge, h.State, &w, forge.repo, nil, &fleetOwners{}, now)

	// Assert
	if err != nil || unreadable != nil {
		t.Fatal(err, unreadable)
	}
	if len(listed) != 3 {
		t.Fatalf("listed = %+v, want every open pull request for the train", listed)
	}
	if wakes := prWakes(t, h, "pr_health"); len(wakes) != 1 || !strings.Contains(wakes[0].Detail, "#210") {
		t.Fatalf("health wakes = %+v, want #210's alone: none for a train or a pull request it carries", wakes)
	}
}

// trainCIRunner answers the CI of a train's pull request as still running
// and refuses every other call, recording each.
type trainCIRunner struct{ calls []string }

func (r *trainCIRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, request.Name+" "+strings.Join(request.Args, " "))
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "pr" && request.Args[1] == "view" {
		return execx.Result{Stdout: []byte(`{"state":"OPEN","headRefOid":"abc1234","statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}]}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected")}, nil
}

func TestATrainKeepsMovingAfterTheGoblinsOfItsRepositoryLeft(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	runner := &trainCIRunner{}
	service.Options.CI = runner
	running := train.Train{
		Schema: train.Schema, ID: "r-20261007-160000", Repository: "o/r", Checkout: t.TempDir(), Base: "main", Branch: train.BranchPrefix + "20261007-160000",
		PR: "https://github.com/o/r/pull/900", State: train.StateTesting, Head: "abc1234", BaseSHA: "1234567", Pushed: time.Now().UTC(), Runs: 1,
		Cars: []train.Car{{Number: 11, URL: "https://github.com/o/r/pull/11", State: train.CarRiding}},
	}
	data, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(train.Dir(h.State), running.ID+".json"), string(data))

	// Act
	err = service.checkFleet(context.Background(), time.Now())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(runner.calls, "gh pr view "+running.PR+" --json state,headRefOid,statusCheckRollup") {
		t.Fatalf("calls = %q, want the train's CI read although no goblin works in its repository", runner.calls)
	}
	kept, err := train.Read(h.State, running.ID)
	if err != nil || kept.State != train.StateTesting || kept.Errors != 0 {
		t.Fatalf("train = %+v, %v, want it still testing with no error", kept, err)
	}
}
