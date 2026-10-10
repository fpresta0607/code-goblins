package train

import (
	"cmp"
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
	// waits counts the pauses the engine took.
	waits int
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
		Wait:     func(time.Duration) { s.waits++ },
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

// step takes the train id one step, which must not fail.
func (s *scratch) step(engine Engine, id string) Train {
	s.t.Helper()
	stepped, err := engine.Advance(context.Background(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	return stepped
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
// opens, finds, edits, merges and closes a train's pull requests, reports
// their CI from what their head holds, runs a workflow run's failed jobs
// again and says how that run stands, lists the open pull requests as the
// remote has them, reports main's push runs, and merges a pull request into
// main with a merge commit. As GitHub does, it marks a pull request merged
// once main holds its head, however that head got there.
type fakeGitHub struct {
	s     *scratch
	pulls map[string]*pull
	// breaks are heads whose presence on a train's head turns its CI red, on
	// every try.
	breaks []string
	// chance is how many workflow runs fail their first try whatever their
	// head holds, as a test that fails by chance does, and pass when they
	// run again.
	chance int
	// foreign makes the train's one check another service's, which no
	// workflow run stands behind.
	foreign bool
	// workflows are the workflow runs CI started, one for each head it
	// tested, in order.
	workflows []*workflow
	// lag is how many reads of the train's checks, after a workflow run ran
	// again, still answer with the try before.
	lag int
	// noAttempt answers a read of a workflow run without the try it is at.
	noAttempt bool
	// pending is how many CI reads answer that the checks still run. While
	// any is left, a workflow run reads as still running too.
	pending int
	// noChecks answers every CI read with no checks at all.
	noChecks bool
	// modified is how many merges are refused as GitHub refuses one whose
	// base moved during it, before one goes through.
	modified int
	// marksLate is how many reads of the open pull requests still list a pull
	// request main took by another's merge, as GitHub marks it merged a
	// moment after the push.
	marksLate int
	// whileLanding runs once as a train's pull request merges, after the
	// train last read its riders.
	whileLanding func()
	// deletesBranches removes a merged pull request's branch, as a repository
	// that deletes head branches by itself does.
	deletesBranches bool
	// refuse refuses the merge of a pull request URL with this text.
	refuse map[string]string
	// unanswered is how many of a call, "merge <url>", "view" or "create",
	// get no answer at all, as when the network drops, before one does; lost
	// does the call and then loses its answer, once.
	unanswered map[string]int
	lost       map[string]bool
	// mainRuns is gh run list's answer for main's push runs.
	mainRuns string
	// once is GitHub's answer to the read of the tests that failed once,
	// onceFailure what gh says when that read fails, and onceReads how
	// often it was asked.
	once        string
	onceFailure string
	onceReads   int
	// moveMain lands a commit of its own on main after each merge, as a
	// merge outside the train during the landing would.
	moveMain bool
	// trainURL and trainBranch are the newest pull request a train opened,
	// opened every one in order, and state and titles how each stands, OPEN,
	// MERGED or CLOSED, and what it is called.
	trainURL    string
	trainBranch string
	opened      []string
	state       map[string]string
	titles      map[string]string
	created     int
	edited      int
	// closed are the train's pull requests closed without merging, and
	// comments what each was closed with.
	closed   []string
	comments []string
	// merged are the goblins' pull requests GitHub shows merged, in the order
	// main took them.
	merged []int
	calls  [][]string
}

// workflow is one workflow run of the train's CI: the head it tests, how
// often it was tried, and whether its first try fails by chance.
type workflow struct {
	id, tries int
	head      string
	isFlaky   bool
}

// pull is a pull request as the fake GitHub holds it.
type pull struct {
	number                    int
	branch                    string
	isDraft, isHeld, isClosed bool
	// isMerged says main holds its head, and late how many reads of the open
	// pull requests still list it.
	isMerged bool
	late     int
}

func newFakeGitHub(s *scratch) *fakeGitHub {
	return &fakeGitHub{s: s, pulls: map[string]*pull{}, refuse: map[string]string{}, unanswered: map[string]int{}, lost: map[string]bool{}, mainRuns: "[]", state: map[string]string{}, titles: map[string]string{}}
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
	if args[0] == "run" {
		call = "run " + call
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
		f.trainURL = fmt.Sprintf("https://github.com/o/r/pull/%d", 899+f.created)
		f.opened = append(f.opened, f.trainURL)
		f.state[f.trainURL], f.titles[f.trainURL] = "OPEN", flagValue(args, "--title")
		return execx.Result{Stdout: []byte("Creating pull request\n" + f.trainURL + "\n")}, nil
	case args[0] == "pr" && args[1] == "list" && slices.Contains(args, "--head"):
		if f.state[f.trainURL] == "OPEN" && flagValue(args, "--head") == f.trainBranch {
			return execx.Result{Stdout: []byte(f.trainURL + "\n")}, nil
		}
		return execx.Result{Stdout: []byte("\n")}, nil
	case args[0] == "pr" && args[1] == "list":
		return f.list()
	case args[0] == "pr" && args[1] == "edit" && f.state[args[2]] == "OPEN":
		f.edited++
		f.titles[args[2]] = flagValue(args, "--title")
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "view" && args[2] == f.trainURL:
		return f.view()
	case args[0] == "pr" && args[1] == "merge" && f.state[args[2]] != "":
		return f.mergeTrain(args)
	case args[0] == "pr" && args[1] == "merge":
		return f.merge(args[2], flagValue(args, "--match-head-commit"), slices.Contains(args, "--merge"))
	case args[0] == "pr" && args[1] == "close" && f.state[args[2]] == "OPEN":
		f.closed = append(f.closed, args[2])
		f.comments = append(f.comments, flagValue(args, "--comment"))
		f.state[args[2]] = "CLOSED"
		return execx.Result{}, nil
	case args[0] == "pr" && args[1] == "close" && f.state[args[2]] == "MERGED":
		return execx.Result{ExitCode: 1, Stderr: []byte("Pull request " + args[2] + " can't be closed because it was already merged")}, nil
	case args[0] == "api" && args[1] == "graphql":
		// The read of the tests that failed once: what a test gave, or one
		// workflow check that carries no warning.
		f.onceReads++
		if f.onceFailure != "" {
			return execx.Result{ExitCode: 1, Stderr: []byte(f.onceFailure)}, nil
		}
		return execx.Result{Stdout: []byte(cmp.Or(f.once, onceAnswer()))}, nil
	case args[0] == "run" && args[1] == "list":
		return execx.Result{Stdout: []byte(f.mainRuns)}, nil
	case args[0] == "run" && args[1] == "rerun":
		return f.rerun(args[2], slices.Contains(args, "--failed"))
	case args[0] == "run" && args[1] == "view":
		return f.runView(args[2])
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
// branches hold on the remote. One main took by another's merge is still
// listed while GitHub marks it late.
func (f *fakeGitHub) list() (execx.Result, error) {
	var open []PullRequest
	for url, p := range f.pulls {
		if p.isClosed || p.isMerged && p.late == 0 {
			continue
		}
		if p.isMerged {
			p.late--
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
// remote's train branch holds, and its one check is the job of that head's
// workflow run at its newest try, which fails when that try does. While the
// answer lags a try that ran again, it is the try before's.
func (f *fakeGitHub) view() (execx.Result, error) {
	head := f.s.git(f.s.remote, "rev-parse", "refs/heads/"+f.trainBranch)
	w := f.workflowOf(head)
	try := w.tries
	isLagging := f.lag > 0 && try > 1
	if isLagging {
		f.lag--
		try--
	}
	check := Check{Kind: "CheckRun", Name: "test", Status: "COMPLETED", Conclusion: "SUCCESS", DetailsURL: jobLink(w, try)}
	if f.isRed(w, try) {
		check.Conclusion = "FAILURE"
	}
	if f.pending > 0 && !isLagging {
		f.pending--
		check.Status, check.Conclusion = "IN_PROGRESS", ""
	}
	if f.foreign {
		check = Check{Kind: "StatusContext", Context: "scan", State: check.Conclusion, TargetURL: "https://scan.example/report"}
	}
	checks := []Check{check}
	if f.noChecks {
		checks = []Check{}
	}
	data, err := json.Marshal(map[string]any{"state": f.state[f.trainURL], "headRefOid": head, "statusCheckRollup": checks})
	return execx.Result{Stdout: data}, err
}

// workflowOf is the workflow run that tests head, started the first time CI
// is read at that head.
func (f *fakeGitHub) workflowOf(head string) *workflow {
	for _, w := range f.workflows {
		if w.head == head {
			return w
		}
	}
	w := &workflow{id: 101 + len(f.workflows), tries: 1, head: head, isFlaky: f.chance > 0}
	if w.isFlaky {
		f.chance--
	}
	f.workflows = append(f.workflows, w)
	return w
}

// isRed says whether w's try fails: its first by chance, or any while its
// head holds one of the breaking heads.
func (f *fakeGitHub) isRed(w *workflow, try int) bool {
	if w.isFlaky && try == 1 {
		return true
	}
	return slices.ContainsFunc(f.breaks, func(broken string) bool {
		return gitCommand(f.s.remote, "merge-base", "--is-ancestor", broken, w.head).Run() == nil
	})
}

// jobLink is the page of w's one job at try. Each try has a job of its own,
// as on GitHub.
func jobLink(w *workflow, try int) string {
	return fmt.Sprintf("https://github.com/o/r/actions/runs/%d/job/%d", w.id, w.id*10+try)
}

// workflowByID is the workflow run gh names id.
func (f *fakeGitHub) workflowByID(id string) *workflow {
	for _, w := range f.workflows {
		if fmt.Sprint(w.id) == id {
			return w
		}
	}
	return nil
}

// rerun answers gh run rerun: the workflow run's failed jobs run again, as
// its next try.
func (f *fakeGitHub) rerun(id string, isFailedOnly bool) (execx.Result, error) {
	w := f.workflowByID(id)
	switch {
	case w == nil:
		return execx.Result{ExitCode: 1, Stderr: []byte("no workflow run " + id)}, nil
	case !isFailedOnly:
		return execx.Result{ExitCode: 1, Stderr: []byte("every job was asked to run again, not the failed ones")}, nil
	}
	w.tries++
	return execx.Result{}, nil
}

// runView answers gh run view: which try the workflow run is at and how
// that try stands.
func (f *fakeGitHub) runView(id string) (execx.Result, error) {
	w := f.workflowByID(id)
	if w == nil {
		return execx.Result{ExitCode: 1, Stderr: []byte("no workflow run " + id)}, nil
	}
	view := map[string]any{"attempt": w.tries, "status": "completed", "conclusion": "success"}
	switch {
	case f.pending > 0:
		view["status"], view["conclusion"] = "in_progress", ""
	case f.isRed(w, w.tries):
		view["conclusion"] = "failure"
	}
	if f.noAttempt {
		delete(view, "attempt")
	}
	data, err := json.Marshal(view)
	return execx.Result{Stdout: data}, err
}

// rerunCalls returns the workflow run each gh run rerun call named.
func (f *fakeGitHub) rerunCalls() []string {
	var calls []string
	for _, args := range f.calls {
		if len(args) > 2 && args[0] == "run" && args[1] == "rerun" {
			calls = append(calls, args[2])
		}
	}
	return calls
}

// tries says how often each workflow run was tried, in the order CI started
// them.
func (f *fakeGitHub) tries() []int {
	var tries []int
	for _, w := range f.workflows {
		tries = append(tries, w.tries)
	}
	return tries
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
	p.isMerged = true
	f.merged = append(f.merged, p.number)
	if f.moveMain {
		f.s.advanceMain(fmt.Sprintf("outside-%d.txt", p.number), "outside\n")
	}
	return execx.Result{Stdout: []byte(fmt.Sprintf("Merged pull request #%d\n", p.number))}, nil
}

// mergeTrain merges a train's own pull request into main as GitHub does:
// only while it is open and its head is still the pinned one, with a merge
// commit of the subject and body asked for. Main then holds the head of
// every pull request that rode, so GitHub marks each of those merged too.
func (f *fakeGitHub) mergeTrain(args []string) (execx.Result, error) {
	url, head := args[2], flagValue(args, "--match-head-commit")
	switch {
	case f.state[url] == "MERGED":
		return execx.Result{Stdout: []byte("! Pull request " + url + " was already merged\n")}, nil
	case f.state[url] != "OPEN" || url != f.trainURL:
		return execx.Result{ExitCode: 1, Stderr: []byte("no open pull request " + url)}, nil
	case f.refuse[url] != "":
		return execx.Result{ExitCode: 1, Stderr: []byte(f.refuse[url])}, nil
	case !slices.Contains(args, "--merge"):
		return execx.Result{ExitCode: 1, Stderr: []byte("merge method is not --merge")}, nil
	case f.s.git(f.s.remote, "rev-parse", "refs/heads/"+f.trainBranch) != head:
		return execx.Result{ExitCode: 1, Stderr: []byte("GraphQL: Head branch was modified. Review and try the merge again. (mergePullRequest)")}, nil
	case f.modified > 0:
		f.modified--
		return execx.Result{ExitCode: 1, Stderr: []byte("GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)")}, nil
	}
	if during := f.whileLanding; during != nil {
		f.whileLanding = nil
		during()
	}
	subject := cmp.Or(flagValue(args, "--subject"), "Merge pull request #"+filepath.Base(url)+" from o/"+f.trainBranch)
	f.s.git(f.s.github, "fetch", "-q", "origin")
	f.s.git(f.s.github, "checkout", "-q", "-B", "main", "origin/main")
	if out, err := gitCommand(f.s.github, "merge", "--no-ff", "-q", "-m", subject, "-m", flagValue(args, "--body"), head).CombinedOutput(); err != nil {
		f.s.git(f.s.github, "merge", "--abort")
		return execx.Result{ExitCode: 1, Stderr: []byte("Pull request is not mergeable: " + string(out))}, nil
	}
	f.s.git(f.s.github, "push", "-q", "origin", "main")
	f.state[url] = "MERGED"
	if f.deletesBranches {
		f.s.git(f.s.github, "push", "-q", "origin", "--delete", f.trainBranch)
	}
	f.markMerged()
	if f.moveMain {
		f.s.advanceMain("outside-"+filepath.Base(url)+".txt", "outside\n")
	}
	return execx.Result{Stdout: []byte("Merged pull request " + url + "\n")}, nil
}

// markMerged marks merged each open pull request whose head main now holds
// as the second parent of a merge commit, oldest first: the order main took
// them in.
func (f *fakeGitHub) markMerged() {
	for _, parents := range strings.Split(f.s.git(f.s.remote, "log", "--reverse", "--merges", "--format=%P", "refs/heads/main"), "\n") {
		taken := strings.Fields(parents)
		for _, p := range f.pulls {
			if p.isMerged || p.isClosed || len(taken) < 2 || f.s.git(f.s.remote, "rev-parse", "refs/heads/"+p.branch) != taken[1] {
				continue
			}
			p.isMerged, p.late = true, f.marksLate
			f.merged = append(f.merged, p.number)
		}
	}
}

// reads says how GitHub shows the pull request at url: OPEN, MERGED or
// CLOSED.
func (f *fakeGitHub) reads(url string) string {
	p, isGoblins := f.pulls[url]
	switch {
	case !isGoblins:
		return f.state[url]
	case p.isMerged:
		return "MERGED"
	case p.isClosed:
		return "CLOSED"
	}
	return "OPEN"
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
