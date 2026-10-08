package train

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// scratch is a repository on a scratch remote: a bare origin, the checkout a
// train is built in, and a clone in which the fake GitHub merges pull
// requests the way GitHub does, with a merge commit pushed to main.
type scratch struct {
	t        *testing.T
	remote   string
	checkout string
	github   string
	state    string
	now      time.Time
	told     []string
	cfo      []string
	landed   []string
}

// gitEnv is the environment every git in these tests runs in, so they can run
// side by side: no system or user configuration, an author, and no gc or
// maintenance started in the background, whose child keeps its parent's
// output pipe open on Windows, so a read of the parent's output waits for it.
var gitEnv = append(os.Environ(),
	"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
	"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=gc.auto", "GIT_CONFIG_VALUE_0=0", "GIT_CONFIG_KEY_1=maintenance.auto", "GIT_CONFIG_VALUE_1=false",
	"GIT_AUTHOR_NAME=Train Test", "GIT_AUTHOR_EMAIL=train@example.test", "GIT_COMMITTER_NAME=Train Test", "GIT_COMMITTER_EMAIL=train@example.test",
)

// gitCommand is git with args in dir, in gitEnv.
func gitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = dir, gitEnv
	return cmd
}

func newScratch(t *testing.T) *scratch {
	t.Helper()
	root, err := fsx.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &scratch{
		t:        t,
		remote:   filepath.Join(root, "origin.git"),
		checkout: filepath.Join(root, "checkout"),
		github:   filepath.Join(root, "github"),
		state:    filepath.Join(root, "state"),
		now:      time.Date(2026, 10, 7, 16, 0, 0, 0, time.UTC),
	}
	s.git(root, "init", "-q", "--bare", "--initial-branch=main", s.remote)
	s.git(root, "clone", "-q", s.remote, s.github)
	s.commit("README.md", "train\n", "initial")
	s.git(s.github, "push", "-q", "origin", "HEAD:main")
	s.git(root, "clone", "-q", s.remote, s.checkout)
	return s
}

func (s *scratch) git(dir string, args ...string) string {
	s.t.Helper()
	out, err := gitCommand(dir, args...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (s *scratch) commit(file, content, message string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.github, file), []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
	s.git(s.github, "add", file)
	s.git(s.github, "commit", "-q", "-m", message)
}

// branch commits file on a new branch off main, pushes it and returns its
// head.
func (s *scratch) branch(name, file, content string) string {
	s.t.Helper()
	s.git(s.github, "fetch", "-q", "origin")
	s.git(s.github, "checkout", "-q", "-B", name, "origin/main")
	s.commit(file, content, name)
	s.git(s.github, "push", "-q", "origin", name)
	return s.git(s.github, "rev-parse", "HEAD")
}

// extend commits file on top of branch and pushes it, as a goblin pushing
// more work does.
func (s *scratch) extend(branch, file, content string) {
	s.t.Helper()
	s.git(s.github, "fetch", "-q", "origin")
	s.git(s.github, "checkout", "-q", "-B", branch, "origin/"+branch)
	s.commit(file, content, "more on "+branch)
	s.git(s.github, "push", "-q", "origin", branch)
}

// advanceMain lands a commit straight on main, as a merge outside the train
// would.
func (s *scratch) advanceMain(file, content string) string {
	s.t.Helper()
	s.git(s.github, "fetch", "-q", "origin")
	s.git(s.github, "checkout", "-q", "-B", "main", "origin/main")
	s.commit(file, content, "outside the train")
	s.git(s.github, "push", "-q", "origin", "main")
	return s.git(s.github, "rev-parse", "HEAD")
}

func (s *scratch) main() string {
	return s.git(s.remote, "rev-parse", "refs/heads/main")
}

func (s *scratch) tree(revision string) string {
	return s.git(s.remote, "rev-parse", revision+"^{tree}")
}

// hasBranch says whether the remote has branch.
func (s *scratch) hasBranch(branch string) bool {
	return gitCommand(s.remote, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch).Run() == nil
}

// fileOn reads file as revision has it on the remote, or "" without it.
func (s *scratch) fileOn(revision, file string) string {
	out, err := gitCommand(s.remote, "show", revision+":"+file).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (s *scratch) repository() Repository {
	return Repository{Slug: "o/r", Checkout: s.checkout, Base: "main"}
}

func (s *scratch) engine(gh *fakeGitHub) Engine {
	return Engine{
		Commands: gh,
		StateDir: s.state,
		Now:      func() time.Time { return s.now },
		Wait:     func(time.Duration) {},
		TellGoblin: func(_ context.Context, task, text string) error {
			s.told = append(s.told, task+": "+text)
			return nil
		},
		TellCFO: func(_ context.Context, text string) error {
			s.cfo = append(s.cfo, text)
			return nil
		},
		Landed: func(_ context.Context, t Train, car Car) {
			s.landed = append(s.landed, car.URL+" by "+t.Evidence())
		},
	}
}

// goblinTold returns what task was told, one line per message.
func (s *scratch) goblinTold(task string) []string {
	var lines []string
	for _, line := range s.told {
		if text, ok := strings.CutPrefix(line, task+": "); ok {
			lines = append(lines, text)
		}
	}
	return lines
}

// fakeGitHub plays GitHub over the scratch remote: git runs for real, and gh
// opens, finds, edits and closes the train's pull request, reports its CI
// from what its head holds, lists the open pull requests as the remote has
// them, reports main's push runs, and merges a pull request into main with a
// merge commit.
type fakeGitHub struct {
	s     *scratch
	pulls map[string]*pull
	// breaks are heads whose presence on a train's head turns its CI red.
	breaks []string
	// pending is how many CI reads answer that the checks still run.
	pending int
	// noChecks answers every CI read with no checks at all.
	noChecks bool
	// modified is how many merges are refused as GitHub refuses one whose
	// base moved during it, before one goes through.
	modified int
	// refuse refuses the merge of a pull request URL with this text.
	refuse map[string]string
	// unanswered is how many of a call, "merge <url>", "view" or "create",
	// get no answer at all, as when the network drops, before one does; lost
	// does the call and then loses its answer, once.
	unanswered map[string]int
	lost       map[string]bool
	// mainRuns is gh run list's answer for main's push runs.
	mainRuns string
	// moveMain lands a commit of its own on main after each merge, as a
	// merge outside the train during the landing would.
	moveMain    bool
	trainURL    string
	trainBranch string
	trainState  string
	created     int
	edited      int
	closed      []string
	merged      []int
	calls       [][]string
}

// pull is a pull request as the fake GitHub holds it.
type pull struct {
	number                    int
	branch                    string
	isDraft, isHeld, isClosed bool
}

func newFakeGitHub(s *scratch) *fakeGitHub {
	return &fakeGitHub{s: s, pulls: map[string]*pull{}, refuse: map[string]string{}, unanswered: map[string]int{}, lost: map[string]bool{}, mainRuns: "[]"}
}

// open lists a green, mergeable pull request of branch at head.
func (f *fakeGitHub) open(number int, branch, head string) PullRequest {
	url := fmt.Sprintf("https://github.com/o/r/pull/%d", number)
	f.pulls[url] = &pull{number: number, branch: branch}
	pr := PullRequest{
		Number: number, URL: url, Title: fmt.Sprintf("change %d", number),
		HeadRefName: branch, HeadRefOid: head, BaseRefName: "main", Mergeable: "MERGEABLE",
		Checks: []Check{{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS"}},
	}
	pr.Author.Login = fleetAccount
	return pr
}

func (f *fakeGitHub) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == "git" {
		request.Env = gitEnv
		return execx.OSRunner{}.Run(ctx, request)
	}
	if request.Name != "gh" {
		return execx.Result{}, fmt.Errorf("unexpected command %s", request.Name)
	}
	args := request.Args
	f.calls = append(f.calls, slices.Clone(args))
	call := args[1]
	if call == "merge" {
		call += " " + args[2]
	}
	if f.unanswered[call] > 0 {
		f.unanswered[call]--
		return execx.Result{}, errors.New("the network dropped")
	}
	result, err := f.answer(args)
	if f.lost[call] {
		delete(f.lost, call)
		return execx.Result{}, errors.New("the answer was lost")
	}
	return result, err
}

func (f *fakeGitHub) answer(args []string) (execx.Result, error) {
	switch {
	case args[0] == "pr" && args[1] == "create":
		f.created++
		f.trainBranch = flagValue(args, "--head")
		f.trainURL = "https://github.com/o/r/pull/900"
		f.trainState = "OPEN"
		return execx.Result{Stdout: []byte("Creating pull request\n" + f.trainURL + "\n")}, nil
	case args[0] == "pr" && args[1] == "list" && slices.Contains(args, "--head"):
		if f.trainState == "OPEN" && flagValue(args, "--head") == f.trainBranch {
			return execx.Result{Stdout: []byte(f.trainURL + "\n")}, nil
		}
		return execx.Result{Stdout: []byte("\n")}, nil
	case args[0] == "pr" && args[1] == "list":
		return f.list()
	case args[0] == "pr" && args[1] == "edit" && args[2] == f.trainURL:
		f.edited++
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "view" && args[2] == f.trainURL:
		return f.view()
	case args[0] == "pr" && args[1] == "merge":
		return f.merge(args[2], flagValue(args, "--match-head-commit"), slices.Contains(args, "--merge"))
	case args[0] == "pr" && args[1] == "close" && args[2] == f.trainURL:
		f.closed = append(f.closed, args[2])
		f.trainState = "CLOSED"
		return execx.Result{}, nil
	case args[0] == "run" && args[1] == "list":
		return execx.Result{Stdout: []byte(f.mainRuns)}, nil
	}
	return execx.Result{ExitCode: 1, Stderr: []byte("unexpected gh " + strings.Join(args, " "))}, nil
}

func flagValue(args []string, name string) string {
	if i := slices.Index(args, name); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

// list answers gh pr list with the open pull requests at the heads their
// branches hold on the remote.
func (f *fakeGitHub) list() (execx.Result, error) {
	var open []PullRequest
	for url, p := range f.pulls {
		if p.isClosed {
			continue
		}
		pr := PullRequest{URL: url, Number: p.number, HeadRefOid: f.s.git(f.s.remote, "rev-parse", "refs/heads/"+p.branch), IsDraft: p.isDraft}
		if p.isHeld {
			pr.Labels = []Label{{Name: HoldLabel}}
		}
		open = append(open, pr)
	}
	data, err := json.Marshal(open)
	return execx.Result{Stdout: data}, err
}

// view answers gh pr view of the train's pull request: its head is what the
// remote's train branch holds, and its one check fails when that head holds
// any of the breaking heads.
func (f *fakeGitHub) view() (execx.Result, error) {
	head := f.s.git(f.s.remote, "rev-parse", "refs/heads/"+f.trainBranch)
	check := Check{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", DetailsURL: "https://github.com/o/r/actions/runs/1"}
	for _, broken := range f.breaks {
		if gitCommand(f.s.remote, "merge-base", "--is-ancestor", broken, head).Run() == nil {
			check.Conclusion = "FAILURE"
		}
	}
	if f.pending > 0 {
		f.pending--
		check.Status, check.Conclusion = "IN_PROGRESS", ""
	}
	checks := []Check{check}
	if f.noChecks {
		checks = []Check{}
	}
	data, err := json.Marshal(map[string]any{"state": f.trainState, "headRefOid": head, "statusCheckRollup": checks})
	return execx.Result{Stdout: data}, err
}

// merge merges the pull request at url into main as GitHub does: only while
// it is open and its head is still head, with a merge commit.
func (f *fakeGitHub) merge(url, head string, isMergeCommit bool) (execx.Result, error) {
	p, ok := f.pulls[url]
	switch {
	case !ok || p.isClosed:
		return execx.Result{ExitCode: 1, Stderr: []byte("no open pull request " + url)}, nil
	case f.refuse[url] != "":
		return execx.Result{ExitCode: 1, Stderr: []byte(f.refuse[url])}, nil
	case !isMergeCommit:
		return execx.Result{ExitCode: 1, Stderr: []byte("merge method is not --merge")}, nil
	case f.s.git(f.s.remote, "rev-parse", "refs/heads/"+p.branch) != head:
		return execx.Result{ExitCode: 1, Stderr: []byte("head branch was modified")}, nil
	case f.modified > 0:
		f.modified--
		return execx.Result{ExitCode: 1, Stderr: []byte("GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)")}, nil
	}
	f.s.git(f.s.github, "fetch", "-q", "origin")
	f.s.git(f.s.github, "checkout", "-q", "-B", "main", "origin/main")
	if out, err := gitCommand(f.s.github, "merge", "--no-ff", "-q", "-m", fmt.Sprintf("Merge pull request #%d from o/%s", p.number, p.branch), head).CombinedOutput(); err != nil {
		f.s.git(f.s.github, "merge", "--abort")
		return execx.Result{ExitCode: 1, Stderr: []byte("Pull request is not mergeable: " + string(out))}, nil
	}
	f.s.git(f.s.github, "push", "-q", "origin", "main")
	p.isClosed = true
	f.merged = append(f.merged, p.number)
	if f.moveMain {
		f.s.advanceMain(fmt.Sprintf("outside-%d.txt", p.number), "outside\n")
	}
	return execx.Result{Stdout: []byte(fmt.Sprintf("Merged pull request #%d\n", p.number))}, nil
}

// mergeCalls returns each gh pr merge call's pull request and pinned head.
func (f *fakeGitHub) mergeCalls() []string {
	var calls []string
	for _, args := range f.calls {
		if len(args) > 2 && args[0] == "pr" && args[1] == "merge" {
			calls = append(calls, args[2]+"@"+flagValue(args, "--match-head-commit"))
		}
	}
	return calls
}

// fleetAccount is the account gh works as in these tests, which opens every
// goblin's pull request.
const fleetAccount = "fleet"

// goblinsFor reports each pull request done by its own goblin, g<number>, a
// minute apart in the order given.
func goblinsFor(prs ...PullRequest) []Goblin {
	var goblins []Goblin
	start := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for i, pr := range prs {
		goblins = append(goblins, Goblin{Task: fmt.Sprintf("g%d", pr.Number), Name: fmt.Sprintf("Goblin%d", pr.Number), Title: "Code Designer", Done: map[string]time.Time{pr.URL: start.Add(time.Duration(i) * time.Minute)}})
	}
	return goblins
}
