package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// trainForge is a GitHub repository o/r on a scratch remote: the project's
// origin names GitHub and git reaches the scratch remote in its place, and
// gh lists the pull requests, opens and reads the train's pull request with
// green CI, and merges a pull request into main with a merge commit.
type trainForge struct {
	t        *testing.T
	remote   string
	project  string
	github   string
	open     []train.PullRequest
	trainURL string
	branch   string
	merged   []string
	closed   bool
	calls    []string
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
	case args[0] == "pr" && args[1] == "list":
		return answer(f.open)
	case args[0] == "api" && args[1] == "user":
		return execx.Result{Stdout: []byte("fleet\n")}, nil
	case args[0] == "pr" && args[1] == "create":
		f.branch, f.trainURL = args[slices.Index(args, "--head")+1], "https://github.com/o/r/pull/900"
		return execx.Result{Stdout: []byte(f.trainURL + "\n")}, nil
	case args[0] == "pr" && args[1] == "view" && args[2] == f.trainURL:
		head := gitIn(f.t, f.remote, "rev-parse", "refs/heads/"+f.branch)
		return answer(map[string]any{"state": "OPEN", "headRefOid": head, "statusCheckRollup": []train.Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}}})
	case args[0] == "pr" && args[1] == "merge":
		for _, pr := range f.open {
			if pr.URL == args[2] {
				gitIn(f.t, f.github, "fetch", "-q", "origin")
				gitIn(f.t, f.github, "checkout", "-q", "-B", "main", "origin/main")
				gitIn(f.t, f.github, "merge", "-q", "--no-ff", "-m", "Merge pull request", pr.HeadRefOid)
				gitIn(f.t, f.github, "push", "-q", "origin", "main")
				f.merged = append(f.merged, pr.URL)
				return execx.Result{}, nil
			}
		}
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
	if !slices.Equal(forge.merged, []string{first.URL, second.URL}) || !forge.closed {
		t.Fatalf("merged %v, closed %v: want #11 then #12 and the train pull request closed", forge.merged, forge.closed)
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
	if !slices.Equal(forge.merged, []string{first.URL, second.URL}) {
		t.Fatalf("merged %v, want #31 then #32:\n%s", forge.merged, stdout.String())
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
