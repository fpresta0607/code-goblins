// Package worktree acquires, provisions, freshens, and returns isolated
// in-repo Git worktrees for goblin tasks. A worktree lives at
// <project>/.worktrees/<holder>, inside the repository it belongs to, so the
// project tooling, credentials, and dependency caches the operator already
// set up are one directory away instead of stranded in the primary checkout.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Git provides the Git commands the service needs. It is an interface so
// orchestration tests do not need a real repository.
type Git interface {
	// Acquire creates a detached worktree for holder at
	// <project>/.worktrees/<holder>, based on origin's current default-branch
	// commit, and returns its path. The .worktrees directory is registered in
	// the clone's info/exclude first, so the worktree is invisible to the
	// project's own git status without touching .gitignore.
	Acquire(ctx context.Context, project, holder string) (string, error)
	WorktreeTop(ctx context.Context, dir string) (string, error)
	FetchAndFreshen(ctx context.Context, dir string) error
	// Return removes a worktree and prunes its administrative entry. Shared
	// directory links provisioned into the worktree are unlinked first: Git
	// for Windows follows a junction during recursive deletion and would
	// otherwise delete the primary checkout's files through it.
	Return(ctx context.Context, project, worktree string) error
	// EnsureSeeded makes an unborn or empty primary project a real repository
	// with one commit on its default branch pushed to origin, so a worktree
	// can be based on refs/remotes/origin/<branch>. A freshly created empty
	// GitHub repo has no commits, so there is no remote branch to detach onto.
	// It reports whether it seeded anything; a repo that already has a commit
	// is left untouched.
	EnsureSeeded(ctx context.Context, project string) (bool, error)
}

// Service coordinates a single in-repo worktree lifecycle.
type Service struct {
	Commands execx.Runner
	Git      Git
	// DataDir is the CFO home's data directory, where per-project worktree
	// manifests live under projects/<name>/worktree.json. Provisioning reads
	// it; acquisition and return do not.
	DataDir string
	Sleep   func(context.Context, time.Duration) error
}

// Worktree is an acquired isolated checkout.
type Worktree struct {
	Path string
}

// Acquire creates a fresh in-repo worktree for holder. Holder is the task's
// goblin name ("gb-<id>"); spawn's per-home lock and task-id uniqueness make
// the path itself the lease, so no second ledger can drift from Git's own
// worktree registry.
func (s Service) Acquire(ctx context.Context, project, holder string) (Worktree, error) {
	primary, err := fsx.Canonical(project)
	if err != nil {
		return Worktree{}, fmt.Errorf("worktree: canonicalize primary project %q: %w", project, err)
	}
	git, err := s.git()
	if err != nil {
		return Worktree{}, err
	}
	path, err := git.Acquire(ctx, primary, holder)
	if err != nil {
		return Worktree{}, err
	}
	path, err = fsx.Canonical(path)
	if err != nil {
		return Worktree{}, fmt.Errorf("worktree: canonicalize acquired worktree %q: %w", path, err)
	}
	if fsx.SamePath(path, primary) {
		return Worktree{}, fmt.Errorf("worktree: acquired worktree %q is the primary project", path)
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

// Return releases an acquired worktree: its shared links are unlinked, the
// worktree is removed, and its administrative entry is pruned. It never
// deletes a directory itself.
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
		return errors.New("worktree: Git is required")
	}
	if err := readableDir(worktree); err != nil {
		return fmt.Errorf("worktree: worktree %q is not readable: %w", worktree, err)
	}
	if _, err := fsx.Canonical(project); err != nil {
		return fmt.Errorf("worktree: primary project %q is not readable: %w", project, err)
	}
	top, err := git.WorktreeTop(ctx, worktree)
	if err != nil {
		return fmt.Errorf("worktree: inspect Git top-level for %q: %w", worktree, err)
	}
	if !fsx.SamePath(top, worktree) {
		return fmt.Errorf("worktree: Git top-level %q is not worktree root %q", top, worktree)
	}
	if fsx.SamePath(top, project) {
		return fmt.Errorf("worktree: worktree %q is the primary project", worktree)
	}
	return nil
}

func (s Service) git() (Git, error) {
	if s.Git != nil {
		return s.Git, nil
	}
	if s.Commands == nil {
		return nil, errors.New("worktree: Git or command runner is required")
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
