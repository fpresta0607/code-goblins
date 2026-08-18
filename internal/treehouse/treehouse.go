// Package treehouse acquires, validates, refreshes, and returns isolated
// treehouse worktrees without ever falling back to direct worktree deletion.
package treehouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Git provides the Git and treehouse commands the service needs. It is an
// interface so orchestration tests do not need real external tools.
type Git interface {
	WorktreeTop(ctx context.Context, dir string) (string, error)
	FetchAndFreshen(ctx context.Context, dir string) error
	Return(ctx context.Context, project, worktree string) error
	// EnsureSeeded makes an unborn or empty primary project a real repository
	// with one commit on its default branch pushed to origin, so treehouse can
	// lease a worktree against refs/remotes/origin/<branch>. A freshly created
	// empty GitHub repo has no commits, so `treehouse get --lease` dies on the
	// invalid remote-branch reference. It reports whether it seeded anything; a
	// repo that already has a commit is left untouched.
	EnsureSeeded(ctx context.Context, project string) (bool, error)
}

// Service coordinates a single treehouse worktree lifecycle.
type Service struct {
	Commands execx.Runner
	Git      Git
	Sleep    func(context.Context, time.Duration) error
}

// Worktree is an acquired isolated checkout.
type Worktree struct {
	Path string
}

// leaseAllocation is the machine-readable `treehouse get --lease --json`
// stdout contract. Banners go to stderr, so stdout carries only this object.
type leaseAllocation struct {
	Path    string `json:"path"`
	LeaseID string `json:"lease_id"`
}

// Acquire durably leases a pooled worktree through treehouse's non-interactive
// acquisition. The lease itself is the allocation evidence: treehouse never
// hands a leased worktree to a later get and never prunes it until
// treehouse.Service.Return releases it.
func (s Service) Acquire(ctx context.Context, project, holder string) (Worktree, error) {
	primary, err := fsx.Canonical(project)
	if err != nil {
		return Worktree{}, fmt.Errorf("treehouse: canonicalize primary project %q: %w", project, err)
	}
	if s.Commands == nil {
		return Worktree{}, errors.New("treehouse: command runner is required")
	}
	args := []string{"get", "--lease", "--json"}
	if holder != "" {
		args = append(args, "--lease-holder", holder)
	}
	result, err := s.Commands.Run(ctx, execx.Request{Dir: primary, Name: "treehouse", Args: args})
	if err != nil {
		return Worktree{}, fmt.Errorf("treehouse: lease worktree for %q: %w", primary, err)
	}
	if result.ExitCode != 0 {
		return Worktree{}, commandFailure("treehouse get --lease --json", result)
	}
	var lease leaseAllocation
	if err := json.Unmarshal(result.Stdout, &lease); err != nil {
		return Worktree{}, fmt.Errorf("treehouse: decode lease response: %w", err)
	}
	if lease.Path == "" || lease.LeaseID == "" {
		return Worktree{}, errors.New("treehouse: lease response is missing path or lease_id")
	}
	path, err := fsx.Canonical(lease.Path)
	if err != nil {
		return Worktree{}, fmt.Errorf("treehouse: canonicalize leased worktree %q: %w", lease.Path, err)
	}
	if fsx.SamePath(path, primary) {
		return Worktree{}, fmt.Errorf("treehouse: leased worktree %q is the primary project", path)
	}
	return Worktree{Path: path}, nil
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
