package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// CanTypeFreshDetached proves worktree is a new task's worktree that no gate
// run works in, so the Overlord may type into its goblin before it makes a
// branch (#1987). Its HEAD must be detached at the commit git made the
// worktree with and never have moved since: its HEAD history is that one
// record, from no commit to HEAD, so no branch a gate could hold was ever
// checked out there. No run may work in it: it is none of the gate's own
// worktrees, and no run's recorded worktree is it, by any spelling or link.
// HEAD is read before and after the gate's records, and one that changed in
// between is refused. A worktree it cannot prove so is refused; it changes
// nothing.
func (r Reader) CanTypeFreshDetached(ctx context.Context, worktree string) error {
	worktree, err := fsx.Canonical(worktree)
	if err != nil {
		return err
	}
	historyPath, err := headHistory(ctx, worktree)
	if err != nil {
		return err
	}
	before, err := r.freshHead(ctx, worktree, historyPath)
	if err != nil {
		return err
	}
	if err := r.bindsNoRun(ctx, worktree); err != nil {
		return err
	}
	after, err := r.freshHead(ctx, worktree, historyPath)
	if err != nil {
		return err
	}
	if after != before {
		return errors.New("pipeline: HEAD changed while its gate custody was read")
	}
	return nil
}

// headHistory is the file git keeps worktree's HEAD history in, read from its
// .git entry with no git started, as readcache's openRepository reads it: a
// .git folder keeps it, and a .git file names the folder that does. Any other
// entry is refused.
func headHistory(ctx context.Context, worktree string) (string, error) {
	dotGit := filepath.Join(worktree, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return "", fmt.Errorf("pipeline: HEAD history cannot be located: %w", err)
	}
	if info.IsDir() {
		return filepath.Join(dotGit, "logs", "HEAD"), nil
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("pipeline: HEAD history cannot be located")
	}
	raw, err := fsx.ReadFileContext(ctx, dotGit)
	if err != nil {
		return "", fmt.Errorf("pipeline: HEAD history cannot be located: %w", err)
	}
	gitDir, isLink := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !isLink || gitDir == "" {
		return "", errors.New("pipeline: HEAD history cannot be located")
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktree, gitDir)
	}
	return filepath.Join(gitDir, "logs", "HEAD"), nil
}

// freshHead is the one record of worktree's HEAD history, which proves HEAD
// is detached at the commit git made the worktree with and has not moved.
func (r Reader) freshHead(ctx context.Context, worktree, historyPath string) (string, error) {
	symbolic, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "HEAD"}})
	if err != nil {
		return "", err
	}
	if symbolic.ExitCode != 1 {
		return "", errors.New("pipeline: HEAD is not detached")
	}
	head, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"rev-parse", "--verify", "HEAD"}})
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(head.Stdout))
	if head.ExitCode != 0 || !gateCommit.MatchString(commit) {
		return "", errors.New("pipeline: detached HEAD cannot be read")
	}
	raw, err := fsx.ReadFileContext(ctx, historyPath)
	if err != nil {
		return "", fmt.Errorf("pipeline: HEAD history cannot be read: %w", err)
	}
	record, rest, isWhole := strings.Cut(string(raw), "\n")
	fields := strings.Fields(record)
	if !isWhole || rest != "" || len(fields) < 2 || fields[0] != strings.Repeat("0", len(commit)) || fields[1] != commit {
		return "", errors.New("pipeline: HEAD has moved since the worktree was made")
	}
	return record, nil
}

// bindsNoRun proves no run the gate records works in worktree. The gate makes
// each run's worktree in a folder of its own, so any worktree in that folder
// is the gate's. A run's recorded worktree outside it is compared as the
// folder it is on disk, so another spelling of worktree, or a link or
// junction to it, is still it.
func (r Reader) bindsNoRun(ctx context.Context, worktree string) error {
	own, err := os.Stat(worktree)
	if err != nil {
		return fmt.Errorf("pipeline: the worktree cannot be read: %w", err)
	}
	gateWorktrees, err := fsx.AbsClean(filepath.Join(r.Root, "worktrees"))
	if err != nil {
		return err
	}
	folders := []string{gateWorktrees}
	if physical, err := fsx.Canonical(gateWorktrees); err == nil {
		folders = append(folders, physical)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("pipeline: the gate's worktrees cannot be read: %w", err)
	}
	if isWithinAny(worktree, folders) {
		return ErrCustodyHeld
	}
	var runs []struct {
		Worktree string `json:"worktree"`
	}
	if err := r.query(ctx, `SELECT worktree_dir AS worktree FROM runs WHERE COALESCE(worktree_dir,'')<>''`, &runs); err != nil {
		return err
	}
	for _, run := range runs {
		if !filepath.IsAbs(run.Worktree) {
			return errors.New("pipeline: a gate run records a worktree that is not a full path")
		}
		bound, err := fsx.AbsClean(run.Worktree)
		if err != nil {
			return err
		}
		if isWithinAny(bound, folders) {
			continue
		}
		info, err := os.Stat(bound)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("pipeline: a gate run's worktree cannot be read: %w", err)
		}
		if os.SameFile(info, own) {
			return ErrCustodyHeld
		}
	}
	return nil
}

// isWithinAny is whether path is one of folders or inside one of them.
func isWithinAny(path string, folders []string) bool {
	for _, folder := range folders {
		relative, err := filepath.Rel(folder, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
