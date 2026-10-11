package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

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

// acquireScript is the fetchDefault conversation every Acquire on the
// default branch makes before `git worktree add`.
func acquireScript() []scriptedResult {
	return []scriptedResult{
		{}, // git fetch --quiet origin
		{result: execx.Result{Stdout: []byte("origin/main\n")}},
		{result: execx.Result{Stdout: []byte("ref: refs/heads/main\tHEAD\nabc123\tHEAD\n")}},
		{}, // git fetch refspec
		{result: execx.Result{Stdout: []byte("abc123\n")}},
		{}, // git worktree add --detach
	}
}

func TestRunnerGitAcquireCreatesTheWorktreeOutsideTheProject(t *testing.T) {
	// Arrange
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "worktrees", "app", "task")
	runner := &scriptedRunner{results: acquireScript()}

	// Act
	got, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, path, "")

	// Assert
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if got != path {
		t.Errorf("path = %q, want %q", got, path)
	}
	want := []execx.Request{
		{Dir: project, Name: "git", Args: []string{"fetch", "--quiet", "origin"}},
		{Dir: project, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}},
		{Dir: project, Name: "git", Args: []string{"ls-remote", "--symref", "origin", "HEAD"}},
		{Dir: project, Name: "git", Args: []string{"fetch", "--quiet", "origin", "+refs/heads/main:refs/remotes/origin/main"}},
		{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "origin/main^{commit}"}},
		{Dir: project, Name: "git", Args: []string{"-c", "core.longpaths=true", "worktree", "add", "--detach", path, "origin/main"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v\nwant %#v", runner.calls, want)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("the project's folder under the worktrees root was not made: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "info", "exclude")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Acquire wrote the project's info/exclude: %v", err)
	}
	if entries, _ := os.ReadDir(project); len(entries) != 1 {
		t.Errorf("project holds %v, want only its .git", entries)
	}
}

func TestRunnerGitAcquireOnARefDetachesThereWithoutFetching(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(t.TempDir(), "app", "task-proof")
	runner := &scriptedRunner{results: []scriptedResult{{}}}

	if _, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, path, "abc123"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	want := []execx.Request{{Dir: project, Name: "git", Args: []string{"-c", "core.longpaths=true", "worktree", "add", "--detach", path, "abc123"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v\nwant %#v", runner.calls, want)
	}
}

func TestRunnerGitAcquireRefusesAnExistingPath(t *testing.T) {
	project := t.TempDir()
	existing := filepath.Join(t.TempDir(), "app", "task")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: acquireScript()}

	_, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, existing, "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Acquire error = %v, want existing-path refusal", err)
	}
	for _, call := range runner.calls {
		if slices.Contains(call.Args, "add") {
			t.Fatalf("Acquire added a worktree over an existing path: %#v", call)
		}
	}
}

func TestRunnerGitAcquireRefusesARelativePath(t *testing.T) {
	for _, path := range []string{"", "task", `app\task`} {
		if _, err := (RunnerGit{Commands: &scriptedRunner{}}).Acquire(context.Background(), t.TempDir(), path, ""); err == nil {
			t.Errorf("Acquire(%q) returned nil, want a refusal", path)
		}
	}
}

func TestRunnerGitAcquireSurfacesAddFailure(t *testing.T) {
	project := t.TempDir()
	results := acquireScript()
	results[len(results)-1] = scriptedResult{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: invalid reference")}}
	runner := &scriptedRunner{results: results}

	_, err := (RunnerGit{Commands: runner}).Acquire(context.Background(), project, filepath.Join(t.TempDir(), "app", "task"), "")
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
	for path, content := range map[string]string{".git": "gitdir: elsewhere", "main.go": "package main"} {
		if err := os.MkdirAll(worktreePath, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(worktreePath, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runner := &scriptedRunner{results: []scriptedResult{{}, {}}}

	if err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	want := []execx.Request{
		{Dir: worktreePath, Name: "git", Args: []string{"status", "--porcelain"}},
		{Dir: project, Name: "git", Args: []string{"worktree", "prune"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v, want status, prune %#v", runner.calls, want)
	}
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree survived Return: %v", err)
	}
}

func TestRunnerGitReturnSurfacesAFailedPrune(t *testing.T) {
	worktreePath := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{}, // git status --porcelain
		{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: cannot prune")}},
	}}

	err := (RunnerGit{Commands: runner}).Return(context.Background(), t.TempDir(), worktreePath)
	if err == nil || !strings.Contains(err.Error(), "cannot prune") {
		t.Fatalf("Return error = %v, want the failed prune", err)
	}
}

func TestRunnerGitReturnRefusesUncommittedWork(t *testing.T) {
	project := t.TempDir()
	worktreePath := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{Stdout: []byte(" M main.go\n?? scratch.txt\n")}},
	}}

	err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath)
	if err == nil || !strings.Contains(err.Error(), "uncommitted work") {
		t.Fatalf("Return error = %v, want an uncommitted-work refusal", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Git calls = %d, want the status probe only - nothing may be removed", len(runner.calls))
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("a refused Return removed the worktree: %v", err)
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

func TestRunnerGitReturnRemovesEveryLinkAsALink(t *testing.T) {
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
	outsideMarker := filepath.Join(outside, "package.txt")
	for _, path := range []string{marker, outsideMarker} {
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	makeJunction(t, filepath.Join(worktreePath, "node_modules"), shared)
	makeJunction(t, filepath.Join(worktreePath, "elsewhere"), outside)

	runner := &scriptedRunner{results: []scriptedResult{{}, {}}}
	if err := (RunnerGit{Commands: runner}).Return(context.Background(), project, worktreePath); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree survived Return: %v", err)
	}
	for _, path := range []string{marker, outsideMarker} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Return deleted %s through a junction: %v", path, err)
		}
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

func TestRunnerGitRemoveOfAMissingWorktreeOnlyPrunes(t *testing.T) {
	project := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{{}}}

	if err := (RunnerGit{Commands: runner}).Remove(context.Background(), project, filepath.Join(t.TempDir(), "gone")); err != nil {
		t.Fatalf("Remove of a missing worktree: %v", err)
	}
	want := []execx.Request{{Dir: project, Name: "git", Args: []string{"worktree", "prune"}}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v, want prune alone", runner.calls)
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
