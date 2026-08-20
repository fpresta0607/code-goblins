package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type scriptedResult struct {
	result execx.Result
	err    error
}

type scriptedRunner struct {
	results []scriptedResult
	calls   []execx.Request
}

func (r *scriptedRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	r.calls = append(r.calls, request)
	if len(r.results) == 0 {
		return execx.Result{}, errors.New("unexpected command")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result.result, result.err
}

// acquireScript is the fetchDefault conversation plus the common-dir probe
// every Acquire makes before `git worktree add`.
func acquireScript() []scriptedResult {
	return []scriptedResult{
		{}, // git fetch --quiet origin
		{}, // git remote set-head origin --auto
		{result: execx.Result{Stdout: []byte("origin/main\n")}},
		{}, // git fetch refspec
		{result: execx.Result{Stdout: []byte("abc123\n")}},
		{result: execx.Result{Stdout: []byte(".git\n")}}, // rev-parse --git-common-dir
		{}, // git worktree add --detach
	}
}

func TestRunnerGitAcquireCreatesInRepoWorktree(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: acquireScript()}

	path, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, "gb-task")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	wantPath := filepath.Join(project, ".worktrees", "gb-task")
	if path != wantPath {
		t.Errorf("path = %q, want the in-repo worktree %q", path, wantPath)
	}
	want := []execx.Request{
		{Dir: project, Name: "git", Args: []string{"fetch", "--quiet", "origin"}},
		{Dir: project, Name: "git", Args: []string{"remote", "set-head", "origin", "--auto"}},
		{Dir: project, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}},
		{Dir: project, Name: "git", Args: []string{"fetch", "--quiet", "origin", "+refs/heads/main:refs/remotes/origin/main"}},
		{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "origin/main^{commit}"}},
		{Dir: project, Name: "git", Args: []string{"rev-parse", "--git-common-dir"}},
		{Dir: project, Name: "git", Args: []string{"worktree", "add", "--detach", wantPath, "origin/main"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v\nwant %#v", runner.calls, want)
	}
	exclude, err := os.ReadFile(filepath.Join(project, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read info/exclude: %v", err)
	}
	if !strings.Contains(string(exclude), ".worktrees/\n") {
		t.Errorf("info/exclude = %q, want .worktrees/ registered", exclude)
	}
}

func TestRunnerGitAcquireRegistersExcludeOnce(t *testing.T) {
	project := t.TempDir()
	infoDir := filepath.Join(project, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte("# deps\n.worktrees/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: acquireScript()}

	if _, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, "gb-task"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	exclude, err := os.ReadFile(filepath.Join(infoDir, "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(exclude), ".worktrees/"); got != 1 {
		t.Errorf("info/exclude = %q, want .worktrees/ registered exactly once", exclude)
	}
	if !strings.HasPrefix(string(exclude), "# deps\n") {
		t.Errorf("info/exclude = %q, want existing entries preserved", exclude)
	}
}

func TestRunnerGitAcquireRefusesAnExistingPath(t *testing.T) {
	project := t.TempDir()
	existing := filepath.Join(project, ".worktrees", "gb-task")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: acquireScript()}

	_, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, "gb-task")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Acquire error = %v, want existing-path refusal", err)
	}
	for _, call := range runner.calls {
		if len(call.Args) > 1 && call.Args[0] == "worktree" && call.Args[1] == "add" {
			t.Fatalf("Acquire added a worktree over an existing path: %#v", call)
		}
	}
}

func TestRunnerGitAcquireRefusesMalformedHolder(t *testing.T) {
	for _, holder := range []string{"", "..", "a/b", `a\b`} {
		if _, err := (RunnerGit{Commands: &scriptedRunner{}}).Acquire(context.Background(), t.TempDir(), holder); err == nil {
			t.Errorf("Acquire(%q) returned nil, want holder refusal", holder)
		}
	}
}

func TestRunnerGitAcquireSurfacesAddFailure(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	results := acquireScript()
	results[len(results)-1] = scriptedResult{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: invalid reference")}}
	runner := &scriptedRunner{results: results}

	_, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, "gb-task")
	if err == nil || !strings.Contains(err.Error(), "invalid reference") {
		t.Fatalf("Acquire error = %v, want the git worktree add failure", err)
	}
}

func TestRunnerGitWorktreeTopRejectsFailedCommand(t *testing.T) {
	runner := &scriptedRunner{results: []scriptedResult{{result: execx.Result{ExitCode: 128, Stderr: []byte("not a git repository")}}}}
	_, err := (RunnerGit{Commands: runner}).WorktreeTop(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("WorktreeTop returned nil for a failed git command")
	}
}

func TestRunnerGitReturnRemovesWorktreeAndPrunes(t *testing.T) {
	project := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: []scriptedResult{{}, {}, {}}}

	if err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	want := []execx.Request{
		{Dir: worktreePath, Name: "git", Args: []string{"status", "--porcelain"}},
		{Dir: project, Name: "git", Args: []string{"worktree", "remove", "--force", worktreePath}},
		{Dir: project, Name: "git", Args: []string{"worktree", "prune"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v, want status, remove --force, prune %#v", runner.calls, want)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("Return removed the worktree directory itself: %v", err)
	}
}

func TestRunnerGitReturnRetriesOnlyExactIndexLockCollision(t *testing.T) {
	project := t.TempDir()
	worktreePath := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(worktreePath, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: []scriptedResult{
		{}, // git status --porcelain
		{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: Unable to create 'C:\\pool\\worktree\\index.lock': File exists.\n")}},
		{},
		{},
	}}
	var waits []time.Duration
	git := RunnerGit{
		Commands: runner,
		Retries:  1,
		Sleep: func(ctx context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return ctx.Err()
		},
	}

	if err := git.Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("Git calls = %d, want status, remove, remove, prune", len(runner.calls))
	}
	for _, call := range runner.calls[1:3] {
		want := execx.Request{Dir: project, Name: "git", Args: []string{"worktree", "remove", "--force", worktreePath}}
		if !reflect.DeepEqual(call, want) {
			t.Errorf("Git request = %#v, want %#v", call, want)
		}
	}
	if !reflect.DeepEqual(waits, []time.Duration{time.Second}) {
		t.Errorf("retry waits = %v, want [1s]", waits)
	}
}

func TestRunnerGitReturnDoesNotRetryOtherFailures(t *testing.T) {
	worktreePath := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{}, // git status --porcelain
		{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: index.lock is busy")}},
	}}
	git := RunnerGit{
		Commands: runner,
		Retries:  3,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("Return slept after a non-matching failure")
			return nil
		},
	}

	err := git.Return(context.Background(), t.TempDir(), worktreePath)
	if err == nil {
		t.Fatal("Return returned nil for non-lock failure")
	}
	if len(runner.calls) != 2 {
		t.Errorf("Git calls = %d, want status then one failed remove", len(runner.calls))
	}
}

func TestRunnerGitReturnRefusesUncommittedWork(t *testing.T) {
	project := t.TempDir()
	worktreePath := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{Stdout: []byte(" M main.go\n?? scratch.txt\n")}},
	}}
	git := RunnerGit{
		Commands: runner,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("Return slept before refusing a dirty worktree")
			return nil
		},
	}

	err := git.Return(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("Return error = %v, want an uncommitted-work refusal", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Git calls = %d, want the status probe only - remove must never run", len(runner.calls))
	}
}

// makeJunction links link at target through cmd's mklink /J, the same call
// provisioning uses.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s %s: %v: %s", link, target, err, out)
	}
}

func TestRunnerGitReturnUnlinksSharedDirectoriesFirst(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	shared := filepath.Join(project, "node_modules")
	outside := filepath.Join(root, "elsewhere")
	for _, dir := range []string{shared, worktreePath, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(shared, "package.txt")
	if err := os.WriteFile(marker, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeJunction(t, filepath.Join(worktreePath, "node_modules"), shared)
	makeJunction(t, filepath.Join(worktreePath, "elsewhere"), outside)

	runner := &scriptedRunner{results: []scriptedResult{{}, {}, {}}}
	if err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(worktreePath, "node_modules")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("junction into the project survived Return: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("Return deleted through the junction into the primary checkout: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(worktreePath, "elsewhere")); err != nil {
		t.Errorf("Return unlinked a junction pointing outside the project: %v", err)
	}
}

func TestRunnerGitReturnRefusedForUncommittedWorkLeavesJunctionsInPlace(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "worktree")
	shared := filepath.Join(project, "node_modules")
	for _, dir := range []string{shared, worktreePath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(worktreePath, "node_modules")
	makeJunction(t, link, shared)
	if err := os.WriteFile(filepath.Join(shared, "package.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{Stdout: []byte(" M main.go\n")}},
	}}

	err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("Return error = %v, want an uncommitted-work refusal", err)
	}
	// The refusal tells the operator to commit their work. That is only
	// actionable in a worktree that still builds and tests, so the junction
	// carrying its dependencies has to survive a Return that changed nothing.
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("refused Return destroyed the worktree's junction: %v", err)
	}
	entries, err := os.ReadDir(link)
	if err != nil || len(entries) != 1 {
		t.Fatalf("junction no longer resolves to the shared directory: %v %v", entries, err)
	}
}

func TestRemoveSharedLinksToleratesAMissingWorktree(t *testing.T) {
	if err := removeSharedLinks(filepath.Join(t.TempDir(), "gone"), t.TempDir()); err != nil {
		t.Fatalf("removeSharedLinks on a missing worktree: %v", err)
	}
}

func TestRunnerGitEnsureSeededLeavesACommitUntouched(t *testing.T) {
	project := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{Stdout: []byte("abc123\n")}},
	}}
	seeded, err := (RunnerGit{Commands: runner}).EnsureSeeded(context.Background(), project)
	if err != nil {
		t.Fatalf("EnsureSeeded: %v", err)
	}
	if seeded {
		t.Error("EnsureSeeded reported seeding a repo that already has a commit")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Git calls = %d, want 1 (rev-parse only)", len(runner.calls))
	}
	want := execx.Request{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "HEAD"}}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Errorf("call = %#v, want %#v", runner.calls[0], want)
	}
}

func TestRunnerGitEnsureSeededSeedsAnEmptyRepo(t *testing.T) {
	project := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{ExitCode: 1}}, // unborn HEAD
		{},                                  // empty status
		{result: execx.Result{Stdout: []byte("origin\n")}}, // origin remote
		{result: execx.Result{Stdout: []byte("main\n")}},   // current branch
		{}, // git add
		{}, // git commit
		{}, // git push
	}}
	seeded, err := (RunnerGit{Commands: runner}).EnsureSeeded(context.Background(), project)
	if err != nil {
		t.Fatalf("EnsureSeeded: %v", err)
	}
	if !seeded {
		t.Error("EnsureSeeded did not report seeding an empty repo")
	}
	readme, err := os.ReadFile(filepath.Join(project, "README.md"))
	if err != nil {
		t.Fatalf("read seeded README: %v", err)
	}
	if got, want := string(readme), "# "+filepath.Base(project)+"\n"; got != want {
		t.Errorf("README = %q, want %q", got, want)
	}
	want := []execx.Request{
		{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "HEAD"}},
		{Dir: project, Name: "git", Args: []string{"status", "--porcelain", "--untracked-files=all"}},
		{Dir: project, Name: "git", Args: []string{"remote"}},
		{Dir: project, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}},
		{Dir: project, Name: "git", Args: []string{"add", "README.md"}},
		{Dir: project, Name: "git", Args: []string{"-c", "user.name=Code Goblins", "-c", "user.email=code-goblins@localhost", "commit", "-m", "Initial commit"}},
		{Dir: project, Name: "git", Args: []string{"push", "-u", "origin", "main"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v\nwant %#v", runner.calls, want)
	}
}

func TestRunnerGitEnsureSeededRefusesFilesWithoutCommit(t *testing.T) {
	project := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{ExitCode: 1}},                         // unborn HEAD
		{result: execx.Result{Stdout: []byte("?? existing.txt\n")}}, // files present
	}}
	_, err := (RunnerGit{Commands: runner}).EnsureSeeded(context.Background(), project)
	if err == nil || !strings.Contains(err.Error(), "contains files") {
		t.Fatalf("EnsureSeeded error = %v, want files refusal", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("Git calls = %d, want 2 before refusal", len(runner.calls))
	}
}
