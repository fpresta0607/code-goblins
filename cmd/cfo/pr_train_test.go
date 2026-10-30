package main

import (
	"bytes"
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
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainForge is a GitHub repository o/r on a scratch remote: the project's
// origin names GitHub and git reaches the scratch remote in its place, and
// gh lists the open pull requests, opens and reads the train's pull request
// with green CI, and merges it into main with a merge commit. As GitHub
// does, it shows a pull request merged once main holds its head.
type trainForge struct {
	t        *testing.T
	remote   string
	project  string
	github   string
	open     []train.PullRequest
	trainURL string
	branch   string
	// landed is the head the train's pull request merged at, and subject its
	// merge commit's.
	landed  string
	subject string
	closed  bool
	// labels are the labels the repository has, and wears the ones on the
	// train's pull request.
	labels   []string
	wears    []string
	calls    []string
	onceTest string
}

// merged names the pull requests GitHub shows merged, in the order they were
// opened: those whose head main holds.
func (f *trainForge) merged() []string {
	var merged []string
	for _, pr := range f.open {
		if exec.Command("git", "-C", f.remote, "merge-base", "--is-ancestor", pr.HeadRefOid, "refs/heads/main").Run() == nil {
			merged = append(merged, pr.URL)
		}
	}
	return merged
}

func newTrainForge(t *testing.T) *trainForge {
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
	f := &trainForge{t: t, remote: filepath.Join(root, "origin.git"), project: filepath.Join(root, "app"), github: filepath.Join(root, "github")}
	gitIn(t, root, "init", "-q", "--bare", "--initial-branch=main", f.remote)
	gitIn(t, root, "clone", "-q", f.remote, f.github)
	gitIn(t, f.github, "commit", "-q", "--allow-empty", "-m", "seed")
	gitIn(t, f.github, "push", "-q", "origin", "HEAD:main")
	gitIn(t, root, "clone", "-q", f.remote, f.project)
	gitIn(t, f.project, "remote", "set-url", "origin", "https://github.com/o/r.git")
	gitIn(t, f.project, "config", "url."+filepath.ToSlash(f.remote)+".insteadOf", "https://github.com/o/r.git")
	return f
}

// pull pushes branch with file and lists it as green pull request number.
func (f *trainForge) pull(number int, branch, file string) train.PullRequest {
	gitIn(f.t, f.github, "fetch", "-q", "origin")
	gitIn(f.t, f.github, "checkout", "-q", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(f.github, file), []byte(branch+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	gitIn(f.t, f.github, "add", file)
	gitIn(f.t, f.github, "commit", "-q", "-m", branch)
	gitIn(f.t, f.github, "push", "-q", "origin", branch)
	pr := train.PullRequest{
		Number: number, URL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), Title: branch,
		HeadRefName: branch, HeadRefOid: gitIn(f.t, f.github, "rev-parse", "HEAD"), BaseRefName: "main", Mergeable: "MERGEABLE",
		Checks: []train.Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}
	pr.Author.Login = "fleet"
	f.open = append(f.open, pr)
	return pr
}

func (f *trainForge) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" {
		return execx.OSRunner{}.Run(ctx, request)
	}
	args := request.Args
	f.calls = append(f.calls, strings.Join(args, " "))
	answer := func(value any) (execx.Result, error) {
		data, err := json.Marshal(value)
		return execx.Result{Stdout: data}, err
	}
	switch {
	case request.Name != "gh":
	case args[0] == "api" && args[1] == "graphql" && f.onceTest != "":
		return execx.Result{Stdout: []byte(fmt.Sprintf(`{"data":{"repository":{"object":{"checkSuites":{"nodes":[{"checkRuns":{"nodes":[{"annotations":{"nodes":[{"title":"Failed once","message":%q}]}}]}}]}}}}}`, f.onceTest))}, nil
	case args[0] == "pr" && args[1] == "list":
		merged := f.merged()
		return answer(slices.DeleteFunc(slices.Clone(f.open), func(pr train.PullRequest) bool { return slices.Contains(merged, pr.URL) }))
	case args[0] == "api" && args[1] == "user":
		return execx.Result{Stdout: []byte("fleet\n")}, nil
	case args[0] == "run" && args[1] == "list":
		// Main has no deploy run for a green train to wait on.
		return execx.Result{Stdout: []byte("[]")}, nil
	case args[0] == "pr" && args[1] == "create":
		f.branch, f.trainURL = args[slices.Index(args, "--head")+1], "https://github.com/o/r/pull/900"
		return execx.Result{Stdout: []byte(f.trainURL + "\n")}, nil
	case args[0] == "label" && args[1] == "create":
		f.labels = append(f.labels, args[2])
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "edit" && args[2] == f.trainURL && slices.Contains(f.labels, args[slices.Index(args, "--add-label")+1]):
		f.wears = append(f.wears, args[slices.Index(args, "--add-label")+1])
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "view" && args[2] == f.trainURL:
		head := gitIn(f.t, f.remote, "rev-parse", "refs/heads/"+f.branch)
		return answer(map[string]any{"state": "OPEN", "headRefOid": head, "statusCheckRollup": []train.Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}, "mergeStateStatus": "CLEAN"})
	case args[0] == "pr" && args[1] == "merge" && args[2] == f.trainURL:
		f.landed, f.subject = args[slices.Index(args, "--match-head-commit")+1], args[slices.Index(args, "--subject")+1]
		gitIn(f.t, f.github, "fetch", "-q", "origin")
		gitIn(f.t, f.github, "checkout", "-q", "-B", "main", "origin/main")
		gitIn(f.t, f.github, "merge", "-q", "--no-ff", "-m", f.subject, f.landed)
		gitIn(f.t, f.github, "push", "-q", "origin", "main")
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "close" && args[2] == f.trainURL:
		f.closed = true
		return execx.Result{}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected " + request.Name + " " + strings.Join(args, " "))}, nil
}

// doneGoblin records a live goblin in project that reported pr done.
func doneGoblin(t *testing.T, h home.Home, id, project string, pr train.PullRequest) {
	t.Helper()
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: id, Project: project, Harness: "claude", Backend: "native", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, id, "done: PR "+pr.URL); err != nil {
		t.Fatal(err)
	}
}

func TestPRTrainFilesRootsOnTheProjectIssueRepository(t *testing.T) {
	for _, configuration := range []struct{ name, manifest, repository string }{
		{"unset", `{"project":"app"}`, "o/r"},
		{"explicit", `{"project":"app","issue_repository":"o/ops"}`, "o/ops"},
		{"invalid", `{"project":"app","issue_repository":"o/ops/extra"}`, ""},
	} {
		t.Run(configuration.name, func(t *testing.T) {
			// Arrange
			forge := newTrainForge(t)
			forge.onceTest = "go (cmd-cfo): github.com/o/r/cmd/cfo TestNativeHost"
			h := testHome(t)
			writeIssueManifest(t, h, forge.project, configuration.manifest)
			pull := forge.pull(11, "feat/a", "a.txt")
			doneGoblin(t, h, "g11", forge.project, pull)
			gh := &issueGH{account: "o", remotes: map[string]map[string]string{"app": {"origin": "https://github.com/o/r.git"}}}
			runtime := testCommandRuntimeForHome(h)
			runtime.trainEvery = time.Millisecond
			runtime.github = gh
			runtime.repositoryOf = tickets.GitHub{Commands: gh}.RepositoryOf
			var stdout, stderr bytes.Buffer

			// Act
			code := runPRTrain([]string{forge.project}, &stdout, &stderr, forge, runtime)

			// Assert
			if configuration.repository == "" {
				if code != 1 || !strings.Contains(stderr.String(), "issue_repository") || len(gh.writes) != 0 {
					t.Fatalf("exit = %d, stderr = %s, writes = %v, want invalid record refused", code, stderr.String(), gh.writes)
				}
				return
			}
			want := []string{"repos/" + configuration.repository + "/labels", "repos/" + configuration.repository + "/issues"}
			if code != 0 || !slices.Equal(gh.writes, want) {
				t.Fatalf("exit = %d, stderr = %s, writes = %v, want %v", code, stderr.String(), gh.writes, want)
			}
			if !slices.Equal(forge.merged(), []string{pull.URL}) {
				t.Fatalf("merged = %v, want the code repository's pull request", forge.merged())
			}
		})
	}
}

func TestPRTrainLandsTheGoblinsGreenPullRequestsWithOneRun(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	h := testHome(t)
	first, second := forge.pull(11, "feat/a", "a.txt"), forge.pull(12, "feat/b", "b.txt")
	draft := forge.pull(13, "feat/draft", "draft.txt")
	forge.open[2].IsDraft = true
	doneGoblin(t, h, "g11", forge.project, first)
	doneGoblin(t, h, "g12", forge.project, second)
	doneGoblin(t, h, "g13", forge.project, draft)
	runtime := testCommandRuntimeForHome(h)
	runtime.trainEvery = time.Millisecond
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	if code != 0 {
		t.Fatalf("cfo pr train = %d, stderr %s, stdout %s", code, stderr.String(), stdout.String())
	}
	// What it leaves on GitHub is what the supervisor's train leaves: its own
	// pull request merged at the head CI tested, the pull requests that rode
	// merged by it, and nothing closed without merging.
	if !slices.Equal(forge.merged(), []string{first.URL, second.URL}) || forge.closed || !strings.HasPrefix(forge.subject, "Merge train ") || !strings.HasSuffix(forge.subject, ": #11, #12") {
		t.Fatalf("merged %v, the train pull request closed %v after merging as %q: want #11 and #12 merged by the train's own pull request, which is never closed", forge.merged(), forge.closed, forge.subject)
	}
	// Its pull request wears the train's label, which a release's generated
	// notes leave out, and nothing is said of a label that was put on.
	if !slices.Contains(forge.wears, train.OwnLabel) || strings.Contains(stdout.String(), "could not put the label") {
		t.Fatalf("the train's pull request wears %q, stdout %s: want it to wear %q", forge.wears, stdout.String(), train.OwnLabel)
	}
	if gitIn(t, forge.remote, "rev-parse", "refs/heads/main^{tree}") != gitIn(t, forge.remote, "rev-parse", forge.landed+"^{tree}") || exec.Command("git", "-C", forge.remote, "rev-parse", "--verify", "--quiet", "refs/heads/"+forge.branch).Run() == nil {
		t.Fatal("main's tree is not the tree CI tested, or the train's branch is kept")
	}
	for _, want := range []string{"stays off: #13 it is a draft", "run 1 of train", "landed #11, #12 in 1 CI run(s)"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %s, want %q", stdout.String(), want)
		}
	}
	if list := forge.calls[0]; !strings.Contains(list, "pr list --repo o/r --state open --base main") {
		t.Fatalf("first gh call = %q, want the open pull requests into main", list)
	}
	trains, err := train.List(h.State)
	if err != nil || len(trains) != 1 || trains[0].State != train.StateLanded {
		t.Fatalf("trains = %+v, %v, want one landed train kept", trains, err)
	}
}

// cfo pr train reads a goblin's finished pull requests as the supervisor
// does: one it reported done rides although the goblin went on working on its
// next pull request.
func TestPRTrainLandsAPullRequestWhoseGoblinWentOnWorking(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	h := testHome(t)
	first, second := forge.pull(31, "feat/a", "a.txt"), forge.pull(32, "feat/b", "b.txt")
	doneGoblin(t, h, "g31", forge.project, first)
	doneGoblin(t, h, "g32", forge.project, second)
	if err := state.AppendStatus(h.State, "g31", "working: building its next pull request"); err != nil {
		t.Fatal(err)
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.trainEvery = time.Millisecond
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	if code != 0 {
		t.Fatalf("cfo pr train = %d, stderr %s, stdout %s", code, stderr.String(), stdout.String())
	}
	if !slices.Equal(forge.merged(), []string{first.URL, second.URL}) {
		t.Fatalf("merged %v, want #31 and #32:\n%s", forge.merged(), stdout.String())
	}
}

func TestPRTrainStartsNothingWithoutAGoblinsFinishedPullRequest(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	h := testHome(t)
	forge.pull(21, "feat/a", "a.txt")
	runtime := testCommandRuntimeForHome(h)
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	if code != 0 || !strings.Contains(stdout.String(), "stays off: #21 no goblin reported it done") || !strings.Contains(stdout.String(), "no train starts") {
		t.Fatalf("cfo pr train = %d, stdout %s, stderr %s", code, stdout.String(), stderr.String())
	}
	if slices.ContainsFunc(forge.calls, func(call string) bool { return strings.HasPrefix(call, "pr create") }) {
		t.Fatal("a train pull request was opened with nothing to ride")
	}
}

func TestPRTrainRefusesAProjectWithoutAGitHubOrigin(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	gitIn(t, forge.project, "remote", "set-url", "origin", forge.remote)
	runtime := testCommandRuntimeForHome(testHome(t))
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	if code != 1 || !strings.Contains(stderr.String(), "is not on GitHub") || len(forge.calls) != 0 {
		t.Fatalf("cfo pr train = %d, stderr %s, gh calls %v", code, stderr.String(), forge.calls)
	}
}

func TestPRTrainNeedsItsProject(t *testing.T) {
	// Arrange
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", nil, &stdout, &stderr, execx.OSRunner{}, testCommandRuntime(t))

	// Assert
	if code != 2 || !strings.Contains(stderr.String(), "<project> is required") {
		t.Fatalf("cfo pr train = %d, stderr %s", code, stderr.String())
	}
}

// A train the CFO starts detached does not run under the registered CFO, so
// the log of a decision refused its landings and nothing it merged was logged:
// the eight pull requests of 2026-10-13 and #821 of 2026-10-14. The landing is
// asked of the supervisor, which reads the train's record and the pull request.
func TestPRTrainAsksTheSupervisorToLogEachLandingWhileAFKModeIsOn(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	h := testHome(t)
	first, second := forge.pull(51, "feat/a", "a.txt"), forge.pull(52, "feat/b", "b.txt")
	doneGoblin(t, h, "g51", forge.project, first)
	doneGoblin(t, h, "g52", forge.project, second)
	if _, _, err := afk.TurnOn(h.State, "his own terminal (powershell.exe pid 4242)", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	runtime := testCommandRuntimeForHome(h)
	runtime.trainEvery = time.Millisecond
	var asked []string
	runtime.logTrainLanding = func(_ home.Home, trainID, url string) error { asked = append(asked, trainID+" "+url); return nil }
	runtime.logAFK = func(home.Home, afk.Entry) error {
		t.Error("the train wrote the decision log itself: a train that does not run under the registered CFO is refused it")
		return nil
	}
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	trains, err := train.List(h.State)
	if code != 0 || err != nil || len(trains) != 1 {
		t.Fatalf("cfo pr train = %d, stderr %s, trains %+v, %v", code, stderr.String(), trains, err)
	}
	if want := []string{trains[0].ID + " " + first.URL, trains[0].ID + " " + second.URL}; !slices.Equal(asked, want) {
		t.Errorf("the supervisor was asked to log %q, want each landing with its train", asked)
	}
}

func TestPRTrainAsksNothingOfTheSupervisorWhileAFKModeIsOff(t *testing.T) {
	// Arrange
	forge := newTrainForge(t)
	h := testHome(t)
	first, second := forge.pull(61, "feat/a", "a.txt"), forge.pull(62, "feat/b", "b.txt")
	doneGoblin(t, h, "g61", forge.project, first)
	doneGoblin(t, h, "g62", forge.project, second)
	runtime := testCommandRuntimeForHome(h)
	runtime.trainEvery = time.Millisecond
	asked := 0
	runtime.logTrainLanding = func(home.Home, string, string) error { asked++; return nil }
	var stdout, stderr bytes.Buffer

	// Act
	code := runPR("train", []string{forge.project}, &stdout, &stderr, forge, runtime)

	// Assert
	if code != 0 || asked != 0 {
		t.Errorf("cfo pr train = %d, asked %d times, want it to land and ask nothing with AFK mode off", code, asked)
	}
}
