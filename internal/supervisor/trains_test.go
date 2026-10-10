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
	remote   string
	created  int
	// pulls are the pull requests pull listed, branch the one the train's
	// pull request was opened from, landed the head it merged at, subject its
	// merge commit's, and closed whether it was closed.
	pulls   []train.PullRequest
	branch  string
	landed  string
	subject string
	closed  bool
	// labels are the labels the repository has, and wears the ones on the
	// train's pull request.
	labels []string
	wears  []string
	// viewerReads counts the reads of the account gh works as, and
	// viewerTimeouts is how many more of them time out.
	viewerReads    int
	viewerTimeouts int
	// originTimeouts is how many more reads of the checkout's origin time
	// out, and baseTimeouts how many more reads of the base's head on it.
	originTimeouts int
	baseTimeouts   int
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
	remote := filepath.Join(root, "origin.git")
	p := &trainProject{t: t, checkout: filepath.Join(root, "app"), pusher: filepath.Join(root, "pusher"), remote: remote}
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
	p.pulls = append(p.pulls, pr)
	return pr
}

// merged names the pull requests GitHub shows merged, in the order they were
// opened: those whose head main holds.
func (p *trainProject) merged() []string {
	var merged []string
	for _, pr := range p.pulls {
		if exec.Command("git", "-C", p.remote, "merge-base", "--is-ancestor", pr.HeadRefOid, "refs/heads/main").Run() == nil {
			merged = append(merged, pr.URL)
		}
	}
	return merged
}

// Run runs git for real, answers gh api user with the fleet's account and gh
// pr create with the train's pull request, whose CI it reads as green and
// which it labels with a label the repository has, merges into main with a
// merge commit and closes, makes a label, and lists the pull requests main
// does not hold yet as open; any other gh call is unexpected.
func (p *trainProject) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" {
		if slices.Equal(request.Args, []string{"config", "--get", "remote.origin.url"}) && p.originTimeouts > 0 {
			p.originTimeouts--
			return execx.Result{}, context.DeadlineExceeded
		}
		if len(request.Args) > 0 && request.Args[0] == "ls-remote" && p.baseTimeouts > 0 {
			p.baseTimeouts--
			return execx.Result{}, context.DeadlineExceeded
		}
		return execx.OSRunner{}.Run(ctx, request)
	}
	if args := request.Args; request.Name == "gh" && len(args) > 2 && args[0] == "pr" && p.created > 0 {
		switch {
		case args[1] == "view":
			view, err := json.Marshal(map[string]any{"state": "OPEN", "headRefOid": p.git(p.remote, "rev-parse", "refs/heads/"+p.branch), "statusCheckRollup": []train.Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}})
			return execx.Result{Stdout: view}, err
		case args[1] == "list":
			merged := p.merged()
			open, err := json.Marshal(slices.DeleteFunc(slices.Clone(p.pulls), func(pr train.PullRequest) bool { return slices.Contains(merged, pr.URL) }))
			return execx.Result{Stdout: open}, err
		case args[1] == "merge" && args[2] == "https://github.com/o/r/pull/900":
			p.landed, p.subject = args[slices.Index(args, "--match-head-commit")+1], args[slices.Index(args, "--subject")+1]
			p.git(p.pusher, "fetch", "-q", "origin")
			p.git(p.pusher, "checkout", "-q", "-B", "main", "origin/main")
			p.git(p.pusher, "merge", "-q", "--no-ff", "-m", p.subject, p.landed)
			p.git(p.pusher, "push", "-q", "origin", "main")
			return execx.Result{}, nil
		case args[1] == "close":
			p.closed = true
			return execx.Result{}, nil
		case args[1] == "edit" && slices.Contains(p.labels, args[slices.Index(args, "--add-label")+1]):
			p.wears = append(p.wears, args[slices.Index(args, "--add-label")+1])
			return execx.Result{}, nil
		}
	}
	if args := request.Args; request.Name == "gh" && len(args) > 2 && args[0] == "label" && args[1] == "create" {
		p.labels = append(p.labels, args[2])
		return execx.Result{}, nil
	}
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "api" && request.Args[1] == "user" {
		p.viewerReads++
		if p.viewerTimeouts > 0 {
			p.viewerTimeouts--
			return execx.Result{}, context.DeadlineExceeded
		}
		return execx.Result{Stdout: []byte("fleet\n")}, nil
	}
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "pr" && request.Args[1] == "create" {
		p.created++
		p.branch = request.Args[slices.Index(request.Args, "--head")+1]
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
	err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, []train.PullRequest{first, second})

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

// The supervisor's own train and cfo pr train are one engine, and leave the
// same on GitHub: the poll after a green run merges the train's own pull
// request at the head CI tested, the pull requests that rode read merged by
// it, nothing is closed without merging, and the train's branch is removed.
// Its pull request wears the train's label, which a release's generated
// notes leave out.
func TestTheSupervisorsTrainLandsByMergingItsOwnPullRequest(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(11), project.pull(12)
	reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
	open := []train.PullRequest{first, second}
	if err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, open); err != nil {
		t.Fatal(err)
	}

	// Act
	err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, open)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	trains, err := train.List(h.State)
	if err != nil || len(trains) != 1 || trains[0].State != train.StateLanded {
		t.Fatalf("trains = %+v, %v, want the one train landed", trains, err)
	}
	if !slices.Equal(project.merged(), []string{first.URL, second.URL}) || project.closed || project.created != 1 || project.subject != "Merge train "+trains[0].ID+": #11, #12" {
		t.Fatalf("merged %v, the train pull request closed %v of %d opened, merged as %q: want #11 and #12 merged by the train's own pull request, which is never closed", project.merged(), project.closed, project.created, project.subject)
	}
	if project.git(project.remote, "rev-parse", "refs/heads/main^{tree}") != project.git(project.remote, "rev-parse", project.landed+"^{tree}") || exec.Command("git", "-C", project.remote, "rev-parse", "--verify", "--quiet", "refs/heads/"+project.branch).Run() == nil {
		t.Fatal("main's tree is not the tree CI tested, or the train's branch is kept")
	}
	if !slices.Contains(project.wears, train.OwnLabel) {
		t.Fatalf("the train's pull request wears %q, want %q", project.wears, train.OwnLabel)
	}
	if wakes := prWakes(t, h, "merge_train"); len(wakes) != 2 || !strings.Contains(wakes[1].Detail, "landed #11, #12 in 1 CI run(s)") {
		t.Fatalf("pr wakes = %+v, want the train's start and its landing", wakes)
	}
}

// The account gh works as, read before a train starts, is read again on the
// next poll when the read times out, and its failure is returned only once
// it failed on three polls in a row; the poll that reads it starts the train.
func TestATrainsAccountReadIsReportedOnlyOnTheThirdFailingPollInARow(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(11), project.pull(12)
	reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
	project.viewerTimeouts = 3
	watched := &fleetWakes{}

	for poll, shouldReport := range []bool{false, false, true, false} {
		// Act
		err := service.runTrain(context.Background(), project, watched, project.checkout, []train.PullRequest{first, second})

		// Assert
		if (err != nil) != shouldReport {
			t.Fatalf("poll %d returned %v, want an error only on the third failing poll in a row", poll+1, err)
		}
	}
	if trains, err := train.List(h.State); err != nil || len(trains) != 1 || len(watched.Failing) != 0 {
		t.Fatalf("trains = %+v, %v, failing reads %v, want the train started and nothing left failing", trains, err, watched.Failing)
	}
}

// A train's cars name the goblins whose pull requests ride, by the names
// their records hold.
func TestTrainGoblinsCarryEachGoblinsNameAndTitle(t *testing.T) {
	// Arrange
	h := home.Home{State: t.TempDir()}
	checkout := t.TempDir()
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g11", Project: checkout, Harness: "claude", Backend: "native", SpawnGen: "s1", GoblinName: "Jerry", GoblinTitle: "Code Designer"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, "g11", "done: PR https://github.com/o/r/pull/11"); err != nil {
		t.Fatal(err)
	}

	// Act
	goblins := TrainGoblins(h.State, checkout)

	// Assert
	if len(goblins) != 1 || goblins[0].Task != "g11" || goblins[0].Name != "Jerry" || goblins[0].Title != "Code Designer" {
		t.Fatalf("goblins = %+v, want g11 named Jerry the Code Designer", goblins)
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
	err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, []train.PullRequest{first, second})

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
	err := service.runTrain(context.Background(), project, &fleetWakes{}, project.checkout, []train.PullRequest{first, working})

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

// trainCIRunner answers the CI of a train's pull request as still running,
// after timeouts reads of it time out, and refuses every other call,
// recording each.
type trainCIRunner struct {
	calls    []string
	timeouts int
}

func (r *trainCIRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, request.Name+" "+strings.Join(request.Args, " "))
	if request.Name == "gh" && len(request.Args) > 1 && request.Args[0] == "pr" && request.Args[1] == "view" {
		if r.timeouts > 0 {
			r.timeouts--
			return execx.Result{}, context.DeadlineExceeded
		}
		return execx.Result{Stdout: []byte(`{"state":"OPEN","headRefOid":"abc1234","statusCheckRollup":[{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS"}]}`)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected")}, nil
}

// runningTrain writes a train testing its pull request in a checkout no
// goblin works in.
func runningTrain(t *testing.T, h home.Home) train.Train {
	t.Helper()
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
	return running
}

// A train's CI read that times out under the fleet's load is read again on
// the next poll, as the train's own count of failed steps allows, and reaches
// the CFO only once the step failed on three polls in a row.
func TestATrainsCIReadThatTimesOutIsReportedOnlyOnTheThirdFailingPollInARow(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	runner := &trainCIRunner{timeouts: 3}
	service.Options.CI = runner
	running := runningTrain(t, h)
	now := time.Now().UTC()

	for poll, shouldReport := range []bool{false, false, true, false} {
		// Act
		err := service.checkFleet(context.Background(), now.Add(time.Duration(poll)*ciPollEvery))

		// Assert
		if (err != nil) != shouldReport {
			t.Fatalf("poll %d returned %v, want an error only on the third failing poll in a row", poll+1, err)
		}
	}
	kept, err := train.Read(h.State, running.ID)
	if err != nil || kept.State != train.StateTesting || kept.Errors != 0 {
		t.Fatalf("train = %+v, %v, want it testing again with no error once its CI read", kept, err)
	}
}

func TestATrainKeepsMovingAfterTheGoblinsOfItsRepositoryLeft(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	runner := &trainCIRunner{}
	service.Options.CI = runner
	running := runningTrain(t, h)

	// Act
	err := service.checkFleet(context.Background(), time.Now())

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

// batchTrain is a finished train of o/r that started at minute, with cars
// as "number=state".
func batchTrain(minute int, state string, runs int, cars ...string) train.Train {
	started := time.Date(2026, 10, 8, 19, 0, 0, 0, time.UTC).Add(time.Duration(minute) * time.Minute)
	t := train.Train{Schema: train.Schema, ID: fmt.Sprintf("r-%d", minute), Repository: "o/r", Base: "main", PR: fmt.Sprintf("https://github.com/o/r/pull/%d", 900+minute), State: state, Runs: runs, Started: started}
	if state != train.StateTesting {
		t.Finished = started.Add(20 * time.Minute)
	}
	for _, car := range cars {
		number, carState, _ := strings.Cut(car, "=")
		t.Cars = append(t.Cars, train.Car{Number: len(t.Cars) + 1, URL: "https://github.com/o/r/pull/" + number, State: carState})
	}
	return t
}

// batchIDs says each shown train as its id and the ids it folds in.
func batchIDs(shown []MergeTrainView) []string {
	var ids []string
	for _, view := range shown {
		id := view.ID
		for _, earlier := range view.Earlier {
			id += " <" + earlier.ID
		}
		ids = append(ids, id)
	}
	return ids
}

// The Overlord, 2026-10-08: "merge train failed and landed of the same merge
// train...should not duplicate". A train that landed nothing is folded into
// the later train that took its pull requests on, so each batch shows once.
func TestTheBoardShowsOneTrainForEachBatchOfPullRequests(t *testing.T) {
	for name, test := range map[string]struct {
		trains []train.Train
		want   []string
	}{
		"a failed train folds into the train that landed its pull requests": {
			trains: []train.Train{batchTrain(60, train.StateLanded, 1, "517=landed", "519=landed", "520=landed", "524=landed", "523=conflict"), batchTrain(0, train.StateFailed, 3, "517=returned", "519=returned")},
			want:   []string{"r-60 <r-0"},
		},
		"a failed train folds into the running train that retries it": {
			trains: []train.Train{batchTrain(60, train.StateTesting, 1, "517=riding", "519=riding"), batchTrain(0, train.StateFailed, 3, "517=returned", "519=returned")},
			want:   []string{"r-60 <r-0"},
		},
		"trains that landed nothing fold oldest first into the one that landed": {
			trains: []train.Train{batchTrain(120, train.StateLanded, 1, "517=landed"), batchTrain(60, train.StateStopped, 2, "517=culprit"), batchTrain(0, train.StateFailed, 3, "517=returned")},
			want:   []string{"r-120 <r-0 <r-60"},
		},
		"a train that landed nothing and was not retried shows once": {
			trains: []train.Train{batchTrain(60, train.StateLanded, 1, "520=landed", "524=landed"), batchTrain(0, train.StateFailed, 3, "517=returned", "519=returned")},
			want:   []string{"r-60", "r-0"},
		},
		"a train that landed some shows on its own beside the train that landed the rest": {
			trains: []train.Train{batchTrain(60, train.StateLanded, 1, "519=landed"), batchTrain(0, train.StateStopped, 3, "517=landed", "519=culprit")},
			want:   []string{"r-60", "r-0"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			shown := trainBatches(test.trains)

			// Assert
			if got := batchIDs(shown); !slices.Equal(got, test.want) {
				t.Fatalf("shown = %q, want %q", got, test.want)
			}
		})
	}
}

// A batch shows while its last train runs or for six hours after it
// finished, with every train folded into it, however long before those
// finished.
func TestTheBoardKeepsABatchByItsLastTrain(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	failed, landed := batchTrain(0, train.StateFailed, 3, "517=returned"), batchTrain(60, train.StateLanded, 1, "517=landed")
	for _, record := range []train.Train{failed, landed} {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(train.Dir(h.State), record.ID+".json"), string(data))
	}
	soon, late := landed.Finished.Add(trainsShownFor-time.Minute), landed.Finished.Add(trainsShownFor)

	// Act
	shownSoon := service.keepTrains(soon)
	soonShown := slices.Clone(service.trains)
	shownLate := service.keepTrains(late)

	// Assert
	if shownSoon != nil || shownLate != nil {
		t.Fatal(shownSoon, shownLate)
	}
	if got := batchIDs(soonShown); !slices.Equal(got, []string{"r-60 <r-0"}) || soonShown[0].Earlier[0].Runs != 3 {
		t.Fatalf("shown %q, want the landed train with the failed one, which finished %s before, folded in", got, trainsShownFor)
	}
	if len(service.trains) != 0 {
		t.Fatalf("shown %q, want nothing once the landed train is past its time", batchIDs(service.trains))
	}
}

// On 2026-10-10 at 17:15Z the daily snapshot stalled drive C, the merge
// train's read of a checkout's origin ran out of time once, and that one
// timeout woke the CFO: "supervisor_error: merge train: read the origin of
// C:\dev\PrecisionDocs-AI: context deadline exceeded" (wake 1000685). The
// next poll makes the read again. The read of the repository a train runs
// on, its origin and its default branch, is returned only once it failed on
// three polls in a row, and the poll that reads it starts the train.
func TestATrainsRepositoryReadIsReportedOnlyOnTheThirdFailingPollInARow(t *testing.T) {
	// Arrange
	service, h := fleetService(t)
	project := newTrainProject(t)
	first, second := project.pull(11), project.pull(12)
	reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
	reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
	project.originTimeouts = 3
	watched := &fleetWakes{}

	for poll, shouldReport := range []bool{false, false, true, false} {
		// Act
		err := service.runTrain(context.Background(), project, watched, project.checkout, []train.PullRequest{first, second})

		// Assert
		if (err != nil) != shouldReport {
			t.Fatalf("poll %d returned %v, want an error only on the third failing poll in a row", poll+1, err)
		}
	}
	if trains, err := train.List(h.State); err != nil || len(trains) != 1 || len(watched.Failing) != 0 {
		t.Fatalf("trains = %+v, %v, failing reads %v, want the train started and nothing left failing", trains, err, watched.Failing)
	}
}

// A train's start that is cut short is kept, and the train's next step builds
// it. What cut it short is the train's first failed step, counted with every
// later one, so a read that ran out of time once as a train started wakes
// nobody and the next poll builds the train. One that keeps running out of
// time is told on the third poll in a row.
func TestATrainsStartCutShortByATimedOutReadIsReportedOnlyOnTheThirdFailingPollInARow(t *testing.T) {
	for _, test := range []struct {
		name     string
		timeouts int
		reports  []bool
		want     string
	}{
		{"one read that ran out of time", 1, []bool{false, false}, train.StateTesting},
		{"a read that keeps running out of time", 3, []bool{false, false, true}, train.StateTesting},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			service, h := fleetService(t)
			project := newTrainProject(t)
			first, second := project.pull(11), project.pull(12)
			reportDone(t, h, "g11", project.checkout, "done: PR "+first.URL)
			reportDone(t, h, "g12", project.checkout, "done: PR "+second.URL)
			project.baseTimeouts = test.timeouts
			watched := &fleetWakes{}

			for poll, shouldReport := range test.reports {
				// Act
				err := service.runTrain(context.Background(), project, watched, project.checkout, []train.PullRequest{first, second})

				// Assert
				if (err != nil) != shouldReport {
					t.Fatalf("poll %d returned %v, want an error only on the third failing poll in a row", poll+1, err)
				}
			}
			trains, err := train.List(h.State)
			if err != nil || len(trains) != 1 || trains[0].State != test.want {
				t.Fatalf("trains = %+v, %v, want the one train it started, %s", trains, err, test.want)
			}
			if isBuilt := trains[0].Head != ""; isBuilt != (test.timeouts < len(test.reports)) {
				t.Errorf("train built = %v after %d polls with %d reads timed out", isBuilt, len(test.reports), test.timeouts)
			}
		})
	}
}
