package treehouse

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const defaultReturnRetries = 3

var indexLockCollision = regexp.MustCompile(`Unable to create ['"].*index\.lock['"]: File exists`)

// RunnerGit implements Git through the injected CFO subprocess runner.
type RunnerGit struct {
	Commands execx.Runner
	Retries  int
	Sleep    func(context.Context, time.Duration) error
}

// WorktreeTop reports dir's Git worktree root.
func (g RunnerGit) WorktreeTop(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", commandFailure("git rev-parse --show-toplevel", result)
	}
	top := strings.TrimSpace(string(result.Stdout))
	if top == "" {
		return "", errors.New("treehouse: git rev-parse --show-toplevel returned an empty path")
	}
	return top, nil
}

// FetchAndFreshen updates a clean worktree to origin's verified default-branch
// commit and confirms the resulting HEAD matches that commit.
func (g RunnerGit) FetchAndFreshen(ctx context.Context, dir string) error {
	if _, err := g.required(ctx, dir, "git", "fetch", "--quiet", "origin"); err != nil {
		return fmt.Errorf("treehouse: fetch origin: %w", err)
	}
	if _, err := g.required(ctx, dir, "git", "remote", "set-head", "origin", "--auto"); err != nil {
		return fmt.Errorf("treehouse: resolve origin default branch: %w", err)
	}
	branch, err := g.defaultBranch(ctx, dir)
	if err != nil {
		return err
	}
	target := "origin/" + branch
	refspec := "+refs/heads/" + branch + ":refs/remotes/origin/" + branch
	if _, err := g.required(ctx, dir, "git", "fetch", "--quiet", "origin", refspec); err != nil {
		return fmt.Errorf("treehouse: fetch %q: %w", target, err)
	}
	expectedResult, err := g.required(ctx, dir, "git", "rev-parse", "--verify", "--quiet", target+"^{commit}")
	if err != nil {
		return fmt.Errorf("treehouse: resolve %q commit: %w", target, err)
	}
	expected := strings.TrimSpace(string(expectedResult.Stdout))
	if expected == "" {
		return fmt.Errorf("treehouse: resolve %q commit: empty output", target)
	}
	status, err := g.required(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("treehouse: inspect worktree status: %w", err)
	}
	if strings.TrimSpace(string(status.Stdout)) != "" {
		return fmt.Errorf("treehouse: worktree %q is dirty; refusing to discard uncommitted work", dir)
	}
	if _, err := g.required(ctx, dir, "git", "reset", "--hard", expected); err != nil {
		return fmt.Errorf("treehouse: reset worktree to %q: %w", expected, err)
	}
	actualResult, err := g.required(ctx, dir, "git", "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return fmt.Errorf("treehouse: resolve refreshed HEAD: %w", err)
	}
	actual := strings.TrimSpace(string(actualResult.Stdout))
	if actual != expected {
		return fmt.Errorf("treehouse: refreshed HEAD is %q, want %q", actual, expected)
	}
	return nil
}

// Return invokes treehouse from the primary project directory. It retries only
// the narrow transient Git index-lock collision and never touches the lock or
// worktree directory directly.
func (g RunnerGit) Return(ctx context.Context, project, worktree string) error {
	for attempt := 0; ; attempt++ {
		result, err := g.command(ctx, project, "treehouse", "return", "--force", worktree)
		if err != nil {
			return fmt.Errorf("treehouse: return %q: %w", worktree, err)
		}
		if result.ExitCode == 0 {
			return nil
		}
		failure := commandFailure("treehouse return --force", result)
		if !indexLockCollision.Match(combinedOutput(result)) || attempt >= g.retryCount() {
			return failure
		}
		if err := g.sleep(ctx, time.Second); err != nil {
			return fmt.Errorf("treehouse: wait to retry return: %w", err)
		}
	}
}

func (g RunnerGit) defaultBranch(ctx context.Context, dir string) (string, error) {
	result, err := g.command(ctx, dir, "git", "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("treehouse: read origin/HEAD: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("treehouse: origin/HEAD is unavailable: %w", commandFailure("git symbolic-ref --quiet --short refs/remotes/origin/HEAD", result))
	}
	ref := strings.TrimSpace(string(result.Stdout))
	branch, ok := strings.CutPrefix(ref, "origin/")
	if !ok || branch == "" || strings.ContainsAny(branch, "\r\n") {
		return "", fmt.Errorf("treehouse: origin/HEAD reference %q is malformed", ref)
	}
	return branch, nil
}

func (g RunnerGit) required(ctx context.Context, dir, name string, args ...string) (execx.Result, error) {
	result, err := g.command(ctx, dir, name, args...)
	if err != nil {
		return execx.Result{}, err
	}
	if result.ExitCode != 0 {
		return execx.Result{}, commandFailure(name+" "+strings.Join(args, " "), result)
	}
	return result, nil
}

func (g RunnerGit) command(ctx context.Context, dir, name string, args ...string) (execx.Result, error) {
	if g.Commands == nil {
		return execx.Result{}, errors.New("treehouse: command runner is required")
	}
	return g.Commands.Run(ctx, execx.Request{Dir: dir, Name: name, Args: args})
}

func (g RunnerGit) retryCount() int {
	if g.Retries > 0 {
		return g.Retries
	}
	if g.Retries < 0 {
		return 0
	}
	return defaultReturnRetries
}

func (g RunnerGit) sleep(ctx context.Context, duration time.Duration) error {
	if g.Sleep != nil {
		return g.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func commandFailure(command string, result execx.Result) error {
	output := strings.TrimSpace(string(combinedOutput(result)))
	if output == "" {
		return fmt.Errorf("treehouse: %s exited with code %d", command, result.ExitCode)
	}
	return fmt.Errorf("treehouse: %s exited with code %d: %s", command, result.ExitCode, output)
}

func combinedOutput(result execx.Result) []byte {
	if len(result.Stdout) == 0 {
		return result.Stderr
	}
	if len(result.Stderr) == 0 {
		return result.Stdout
	}
	return append(append([]byte{}, result.Stdout...), append([]byte{'\n'}, result.Stderr...)...)
}
