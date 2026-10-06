// Package worktree acquires, provisions, and returns isolated Git worktrees
// for goblin tasks. A worktree lives in the CFO home, at
// <home>\worktrees\<project folder>\<task id>, never inside the project:
// the project's checkout gains no folder and no file, its tools never walk a
// goblin's copy of the code, and a worktree inherits neither the project's
// CLAUDE.md as a parent memory nor its node_modules by walking up.
// Provisioning shares the project's config and caches into it instead.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// Git provides the Git commands the service needs. It is an interface so
// orchestration tests do not need a real repository.
type Git interface {
	// Acquire creates a detached worktree at path, outside project, based on
	// ref, or on origin's current default-branch commit when ref is empty,
	// and returns its path.
	Acquire(ctx context.Context, project, path, ref string) (string, error)
	WorktreeTop(ctx context.Context, dir string) (string, error)
	// Return removes a worktree and prunes its administrative entry. The
	// uncommitted-work check comes first and a refused Return must change
	// nothing at all, so the operator it tells to commit still has a worktree
	// that builds and tests. Only once removal is going to proceed are the
	// shared directory links provisioned into the worktree unlinked, still
	// before git runs: Git for Windows follows a junction during recursive
	// deletion and would otherwise delete the primary checkout's files
	// through it.
	Return(ctx context.Context, project, worktree string) error
	// EnsureSeeded makes an unborn or empty primary project a real repository
	// with one commit on its default branch pushed to origin, so a worktree
	// can be based on refs/remotes/origin/<branch>. A freshly created empty
	// GitHub repo has no commits, so there is no remote branch to detach onto.
	// It reports whether it seeded anything; a repo that already has a commit
	// is left untouched.
	EnsureSeeded(ctx context.Context, project string) (bool, error)
	// Landing reads where a worktree's work stands against the default
	// branch, and ArchiveTag keeps unlanded work reachable as a local
	// archive tag before its worktree goes.
	Landing(ctx context.Context, dir string) (Landing, error)
	ArchiveTag(ctx context.Context, dir string, landing Landing, name string) (string, error)
}

// Service coordinates a single worktree lifecycle.
type Service struct {
	Commands execx.Runner
	Git      Git
	// Root is the home's worktrees folder. A task's worktree is
	// <Root>\<project folder>\<task id>, and an extra one beside it
	// <task id>-<name>.
	Root string
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

// Path is where the worktree named name of project goes: the project's folder
// under Root, named for its checkout's folder, so every worktree of one
// project sits together.
func (s Service) Path(project, name string) (string, error) {
	if strings.TrimSpace(s.Root) == "" || !filepath.IsAbs(s.Root) {
		return "", fmt.Errorf("worktree: the home's worktrees folder %q is not an absolute path", s.Root)
	}
	if strings.TrimSpace(name) == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("worktree: %q is not a usable worktree name", name)
	}
	folder := filepath.Base(filepath.Clean(project))
	if folder == "" || folder == "." || folder == string(filepath.Separator) || strings.HasSuffix(folder, ":") {
		return "", fmt.Errorf("worktree: project %q names no folder", project)
	}
	return filepath.Join(s.Root, folder, name), nil
}

// Acquire creates a fresh worktree named for task id in the project's folder
// under Root, on origin's default branch. Spawn's per-home lock and task-id
// uniqueness make the path itself the lease, so no second ledger can drift
// from Git's own worktree registry.
func (s Service) Acquire(ctx context.Context, project, id string) (Worktree, error) {
	return s.acquire(ctx, project, id, "")
}

// AddExtra creates a task's extra worktree, <id>-<name>, beside its own in
// the project's folder under Root, on ref or, when ref is empty, on origin's
// default branch. The caller records it on the task, which is what makes it
// the task's and removes it with the task.
func (s Service) AddExtra(ctx context.Context, project, id, name, ref string) (Worktree, error) {
	if !validExtraName(name) {
		return Worktree{}, fmt.Errorf("worktree: extra worktree name %q must be 1 to 32 letters, digits, dots, dashes or underscores", name)
	}
	return s.acquire(ctx, project, id+"-"+name, ref)
}

func validExtraName(name string) bool {
	if name == "" || len(name) > 32 || name == "." || name == ".." {
		return false
	}
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '.', char == '-', char == '_':
		default:
			return false
		}
	}
	return true
}

func (s Service) acquire(ctx context.Context, project, name, ref string) (Worktree, error) {
	primary, err := fsx.Canonical(project)
	if err != nil {
		return Worktree{}, fmt.Errorf("worktree: canonicalize primary project %q: %w", project, err)
	}
	target, err := s.Path(primary, name)
	if err != nil {
		return Worktree{}, err
	}
	git, err := s.git()
	if err != nil {
		return Worktree{}, err
	}
	path, err := git.Acquire(ctx, primary, target, ref)
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
