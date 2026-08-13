package treehouse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestRunnerGitFreshenUsesExpectedGitSequence(t *testing.T) {
	worktree := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{},
		{},
		{result: execx.Result{Stdout: []byte("origin/main\r\n")}},
		{},
		{result: execx.Result{Stdout: []byte("abc123\n")}},
		{},
		{},
		{result: execx.Result{Stdout: []byte("abc123\n")}},
	}}

	err := RunnerGit{Commands: runner}.FetchAndFreshen(context.Background(), worktree)
	if err != nil {
		t.Fatalf("FetchAndFreshen: %v", err)
	}

	want := []execx.Request{
		{Dir: worktree, Name: "git", Args: []string{"fetch", "--quiet", "origin"}},
		{Dir: worktree, Name: "git", Args: []string{"remote", "set-head", "origin", "--auto"}},
		{Dir: worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"}},
		{Dir: worktree, Name: "git", Args: []string{"fetch", "--quiet", "origin", "+refs/heads/main:refs/remotes/origin/main"}},
		{Dir: worktree, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "origin/main^{commit}"}},
		{Dir: worktree, Name: "git", Args: []string{"status", "--porcelain"}},
		{Dir: worktree, Name: "git", Args: []string{"reset", "--hard", "origin/main"}},
		{Dir: worktree, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "HEAD"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Errorf("Git calls = %#v, want %#v", runner.calls, want)
	}
}

func TestRunnerGitFreshenRefusesDirtyWorktreeBeforeReset(t *testing.T) {
	worktree := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{},
		{},
		{result: execx.Result{Stdout: []byte("origin/main\n")}},
		{},
		{result: execx.Result{Stdout: []byte("abc123\n")}},
		{result: execx.Result{Stdout: []byte(" M changed.txt\n")}},
	}}

	err := RunnerGit{Commands: runner}.FetchAndFreshen(context.Background(), worktree)
	if err == nil {
		t.Fatal("FetchAndFreshen returned nil for dirty worktree")
	}
	if len(runner.calls) != 6 {
		t.Fatalf("Git calls = %d, want 6 before dirty refusal", len(runner.calls))
	}
	if got := runner.calls[len(runner.calls)-1].Args; !reflect.DeepEqual(got, []string{"status", "--porcelain"}) {
		t.Errorf("last Git args = %q, want status --porcelain", got)
	}
}

func TestRunnerGitFreshenFallsBackToLocalMainWhenOriginHeadIsUnavailable(t *testing.T) {
	worktree := t.TempDir()
	runner := &scriptedRunner{results: []scriptedResult{
		{},
		{},
		{result: execx.Result{ExitCode: 1}},
		{},
		{},
		{result: execx.Result{Stdout: []byte("abc123\n")}},
		{},
		{},
		{result: execx.Result{Stdout: []byte("abc123\n")}},
	}}

	err := RunnerGit{Commands: runner}.FetchAndFreshen(context.Background(), worktree)
	if err != nil {
		t.Fatalf("FetchAndFreshen: %v", err)
	}
	if len(runner.calls) != 9 {
		t.Fatalf("Git calls = %d, want 9", len(runner.calls))
	}
	if got := runner.calls[3].Args; !reflect.DeepEqual(got, []string{"show-ref", "--verify", "--quiet", "refs/heads/main"}) {
		t.Errorf("fallback args = %q, want local main check", got)
	}
}

func TestRunnerGitWorktreeTopRejectsFailedCommand(t *testing.T) {
	runner := &scriptedRunner{results: []scriptedResult{{result: execx.Result{ExitCode: 128, Stderr: []byte("not a git repository")}}}}
	_, err := (RunnerGit{Commands: runner}).WorktreeTop(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("WorktreeTop returned nil for a failed git command")
	}
}

func TestRunnerGitReturnRetriesOnlyExactIndexLockCollision(t *testing.T) {
	project := t.TempDir()
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedRunner{results: []scriptedResult{
		{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: Unable to create 'C:\\pool\\worktree\\index.lock': File exists.\n")}},
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

	if err := git.Return(context.Background(), project, worktree); err != nil {
		t.Fatalf("Return: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("treehouse return calls = %d, want 2", len(runner.calls))
	}
	for _, call := range runner.calls {
		want := execx.Request{Dir: project, Name: "treehouse", Args: []string{"return", "--force", worktree}}
		if !reflect.DeepEqual(call, want) {
			t.Errorf("treehouse request = %#v, want %#v", call, want)
		}
	}
	if !reflect.DeepEqual(waits, []time.Duration{time.Second}) {
		t.Errorf("retry waits = %v, want [1s]", waits)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("Return removed worktree directly: %v", err)
	}
}

func TestRunnerGitReturnDoesNotRetryOtherFailures(t *testing.T) {
	runner := &scriptedRunner{results: []scriptedResult{{result: execx.Result{ExitCode: 128, Stderr: []byte("fatal: index.lock is busy")}}}}
	git := RunnerGit{
		Commands: runner,
		Retries:  3,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("Return slept after a non-matching failure")
			return nil
		},
	}

	err := git.Return(context.Background(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("Return returned nil for non-lock failure")
	}
	if len(runner.calls) != 1 {
		t.Errorf("treehouse return calls = %d, want 1", len(runner.calls))
	}
}
