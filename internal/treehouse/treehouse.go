// Package treehouse acquires, validates, refreshes, and returns isolated
// treehouse worktrees without ever falling back to direct worktree deletion.
package treehouse

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	defaultPollInterval = time.Second
	defaultTimeout      = 60 * time.Second
)

// Pane sends commands and observes the foreground shell directory of one live
// Herdr pane.
type Pane interface {
	Run(ctx context.Context, text string) error
	ForegroundCWD(ctx context.Context) (string, error)
}

// Git provides the Git and treehouse commands the service needs. It is an
// interface so orchestration tests do not need real external tools.
type Git interface {
	WorktreeTop(ctx context.Context, dir string) (string, error)
	FetchAndFreshen(ctx context.Context, dir string) error
	Return(ctx context.Context, project, worktree string) error
}

// Service coordinates a single treehouse worktree lifecycle.
type Service struct {
	Commands execx.Runner
	Git      Git
	Poll     time.Duration
	Timeout  time.Duration
	Sleep    func(context.Context, time.Duration) error
}

// Worktree is an acquired isolated checkout.
type Worktree struct {
	Path string
}

// Acquire asks the live pane for a worktree, then waits for two matching
// non-primary foreground-directory reads before trusting the result.
func (s Service) Acquire(ctx context.Context, pane Pane, project string) (Worktree, error) {
	primary, err := fsx.Canonical(project)
	if err != nil {
		return Worktree{}, fmt.Errorf("treehouse: canonicalize primary project %q: %w", project, err)
	}
	if err := pane.Run(ctx, "treehouse get"); err != nil {
		return Worktree{}, fmt.Errorf("treehouse: send treehouse get: %w", err)
	}

	poll, attempts := s.pollSettings()
	var candidate string
	for attempt := 0; attempt < attempts; attempt++ {
		cwd, err := pane.ForegroundCWD(ctx)
		if err != nil {
			return Worktree{}, fmt.Errorf("treehouse: read foreground cwd: %w", err)
		}
		cwd, err = fsx.Canonical(cwd)
		if err == nil {
			switch {
			case fsx.SamePath(cwd, primary):
				candidate = ""
			case candidate != "" && fsx.SamePath(cwd, candidate):
				return Worktree{Path: cwd}, nil
			default:
				candidate = cwd
			}
		} else {
			candidate = ""
		}

		if attempt+1 < attempts {
			if err := s.sleep(ctx, poll); err != nil {
				return Worktree{}, fmt.Errorf("treehouse: wait for foreground cwd: %w", err)
			}
		}
	}

	return Worktree{}, fmt.Errorf("treehouse get did not enter a worktree within 60s for Herdr target %s", target(pane))
}

// Freshen updates an acquired worktree to the current default-branch base.
func (s Service) Freshen(ctx context.Context, worktree string) error {
	git, err := s.git()
	if err != nil {
		return err
	}
	return git.FetchAndFreshen(ctx, worktree)
}

// Return releases an acquired worktree through treehouse. It never removes a
// directory itself, including after a failed treehouse invocation.
func (s Service) Return(ctx context.Context, project, worktree string) error {
	git, err := s.git()
	if err != nil {
		return err
	}
	return git.Return(ctx, project, worktree)
}

// Validate proves that worktree is a readable, isolated Git worktree root.
func Validate(ctx context.Context, git Git, project, worktree string) error {
	if git == nil {
		return errors.New("treehouse: Git is required")
	}
	if err := readableDir(worktree); err != nil {
		return fmt.Errorf("treehouse: worktree %q is not readable: %w", worktree, err)
	}
	if _, err := fsx.Canonical(project); err != nil {
		return fmt.Errorf("treehouse: primary project %q is not readable: %w", project, err)
	}
	top, err := git.WorktreeTop(ctx, worktree)
	if err != nil {
		return fmt.Errorf("treehouse: inspect Git top-level for %q: %w", worktree, err)
	}
	if !fsx.SamePath(top, worktree) {
		return fmt.Errorf("treehouse: Git top-level %q is not worktree root %q", top, worktree)
	}
	if fsx.SamePath(top, project) {
		return fmt.Errorf("treehouse: worktree %q is the primary project", worktree)
	}
	return nil
}

func (s Service) git() (Git, error) {
	if s.Git != nil {
		return s.Git, nil
	}
	if s.Commands == nil {
		return nil, errors.New("treehouse: Git or command runner is required")
	}
	return RunnerGit{Commands: s.Commands, Sleep: s.Sleep}, nil
}

func (s Service) pollSettings() (time.Duration, int) {
	poll := s.Poll
	if poll <= 0 {
		poll = defaultPollInterval
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	attempts := int(timeout / poll)
	if timeout%poll != 0 {
		attempts++
	}
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 60 {
		attempts = 60
	}
	return poll, attempts
}

func (s Service) sleep(ctx context.Context, duration time.Duration) error {
	if s.Sleep != nil {
		return s.Sleep(ctx, duration)
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

func target(pane Pane) string {
	if stringer, ok := pane.(fmt.Stringer); ok && stringer.String() != "" {
		return stringer.String()
	}
	return "unknown"
}

func readableDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("not a directory")
	}
	_, err = os.ReadDir(path)
	return err
}
