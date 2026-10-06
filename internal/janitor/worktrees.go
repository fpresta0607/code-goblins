package janitor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// worktreeGrace is how old a worktree no task owns must be before the
// janitor touches it: a spawn makes the worktree a moment before it writes
// the task's record, and a sweep between the two must not take it for an
// orphan.
const worktreeGrace = time.Hour

// tidyWorktrees removes every worktree in this home's worktrees folder that no
// task owns, its work kept first: one that is clean and on the default branch
// goes, one with commits of its own is archived as a local tag and then goes,
// and one with uncommitted work is never touched and is reported. A folder its
// project does not register goes only when it is empty. A worktree an older
// build put in a checkout's .worktrees is never removed here: it is found
// through the projects root every home on the machine shares, so nothing says
// it is this home's rather than another's, and cfo cleanup and cfo reap are
// left to it. Every fleet worktree no task records that is still there
// afterwards is a stray.
func (cfg Config) tidyWorktrees(ctx context.Context, record *Record) {
	git := worktree.RunnerGit{Commands: cfg.Commands}
	gone := map[string]bool{}
	for _, dir := range reap.Unowned(cfg.Inventory) {
		if rel, err := filepath.Rel(cfg.Home.Worktrees(), dir.Path); err != nil || !filepath.IsLocal(rel) {
			continue
		}
		if reason := cfg.notYetOrphaned(dir); reason != "" {
			record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Detail: reason})
			continue
		}
		if dir.Registration == reap.RegistrationUnlisted {
			if empty, err := isEmpty(dir.Path); err != nil || !empty {
				record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Detail: "its project does not register it as a worktree and it holds files, so whose they are is unknown"})
				continue
			}
			if err := os.Remove(dir.Path); err != nil {
				record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Detail: "an empty folder that could not be removed: " + err.Error()})
				continue
			}
			gone[strings.ToLower(dir.Path)] = true
			record.Removed = append(record.Removed, Item{Kind: "worktree", Path: dir.Path, Detail: "an empty folder its project does not register"})
			continue
		}
		bytes := Size(dir.Path)
		detail, err := cfg.returnWorktree(ctx, git, dir)
		if err != nil {
			record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Bytes: bytes, Detail: err.Error()})
			continue
		}
		gone[strings.ToLower(dir.Path)] = true
		record.Removed = append(record.Removed, Item{Kind: "worktree", Path: dir.Path, Bytes: bytes, Detail: detail})
	}
	for _, dir := range cfg.Inventory.Worktrees {
		if gone[strings.ToLower(dir.Path)] || reap.Recorded(cfg.Inventory, dir.Path) {
			continue
		}
		if _, err := os.Stat(dir.Path); err != nil {
			continue
		}
		record.Strays = append(record.Strays, Item{Kind: "worktree", Path: dir.Path, Bytes: Size(dir.Path), Detail: "a worktree no task records; one a goblin needs beside its own comes from cfo worktree add"})
	}
}

// notYetOrphaned says why a worktree the inventory found unowned is not
// treated as an orphan yet, or "" when it is one: it is too new, or a task
// record read now, after the inventory, owns it.
func (cfg Config) notYetOrphaned(dir reap.WorktreeDir) string {
	if dir.Created.IsZero() || cfg.Now.Sub(dir.Created) < worktreeGrace {
		return "made less than an hour ago, or when cannot be read; a spawn may not have recorded it yet"
	}
	owner, known, err := reap.WorktreeOwner(cfg.Home.State, cfg.Home.Worktrees(), dir.Project, dir.Path)
	if err != nil {
		return "the task records could not be read again before removing it: " + err.Error()
	}
	if known {
		return "task " + owner.ID + " owns it"
	}
	return ""
}

// returnWorktree checks a registered worktree's work is safe and returns it,
// saying what became of the work. It refuses, changing nothing, when the
// worktree holds uncommitted work or anything about it cannot be read.
func (cfg Config) returnWorktree(ctx context.Context, git worktree.RunnerGit, dir reap.WorktreeDir) (string, error) {
	if err := worktree.Validate(ctx, git, dir.Project, dir.Path); err != nil {
		return "", errors.New("it could not be proven a worktree of its own: " + err.Error())
	}
	status, err := cfg.Commands.Run(ctx, execx.Request{Dir: dir.Path, Name: "git", Args: []string{"status", "--porcelain=v1", "--untracked-files=all"}})
	if err != nil || status.ExitCode != 0 {
		return "", errors.New("its status could not be read, so whether it holds uncommitted work is unknown")
	}
	if strings.TrimSpace(string(status.Stdout)) != "" {
		return "", errors.New("it holds uncommitted work, which the janitor never touches")
	}
	landing, err := git.Landing(ctx, dir.Path)
	if err != nil {
		return "", err
	}
	detail := "clean, and its work is on the default branch"
	if !landing.Landed {
		tag, err := git.ArchiveTag(ctx, dir.Path, landing, filepath.Base(dir.Path))
		if err != nil {
			return "", err
		}
		detail = "clean, its unlanded work kept as the local tag " + tag
	}
	if err := git.Return(ctx, dir.Project, dir.Path); err != nil {
		return "", err
	}
	return detail, nil
}

func isEmpty(dir string) (bool, error) {
	file, err := fsx.Open(dir)
	if err != nil {
		return false, err
	}
	defer file.Close()
	_, err = file.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}
