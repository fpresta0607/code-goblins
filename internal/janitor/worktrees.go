package janitor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
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

// tidyWorktrees removes every worktree in this home's worktree roots that no
// task owns, its work kept first: one that is clean and on the default branch
// goes, one with commits of its own is archived as a local tag and then goes,
// and one with uncommitted work is never touched and is reported. It does the
// same for a worktree a project registers away from the fleet's folders, which
// a harness's own tooling left in a checkout or a scratchpad. A folder its
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
		isFleet := slices.ContainsFunc(cfg.Home.WorktreeRoots(), func(root string) bool {
			rel, err := filepath.Rel(root, dir.Path)
			return err == nil && filepath.IsLocal(rel)
		})
		if !isFleet {
			cfg.tidyCheckoutWorktree(ctx, git, dir, record)
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
		detail := "a worktree no task records; one a goblin needs beside its own comes from cfo worktree add"
		if dir.Source == reap.SourceCheckout {
			detail = "a worktree " + dir.Project + " registers at a path no task of this home records, left by a harness or an older session"
		}
		record.Strays = append(record.Strays, Item{Kind: "worktree", Path: dir.Path, Bytes: Size(dir.Path), Detail: detail})
	}
}

// inATaskFolder reports whether path lies inside the folders a task of this
// home works in: its worktree, one of its extra worktrees, or its scratch
// folder. A harness's worktree there is that goblin's while it lives, and goes
// with it at its cleanup or its next relaunch.
func (cfg Config) inATaskFolder(path string) bool {
	for _, task := range cfg.Inventory.Tasks {
		for _, folder := range append([]string{task.Meta.Worktree, task.Meta.Scratch}, task.Meta.Extras...) {
			if folder == "" {
				continue
			}
			if rel, err := filepath.Rel(folder, path); err == nil && rel != "." && filepath.IsLocal(rel) {
				return true
			}
		}
	}
	return false
}

// tidyCheckoutWorktree returns a worktree the project registers away from the
// fleet's folders, which a harness's own tooling left in a checkout or a
// scratchpad, on the rule every other worktree here gets: its work is kept
// before it goes, and uncommitted work is never touched. A directory an older
// build's .worktrees holds is left to cfo cleanup and cfo reap, since it is
// found through a checkout every home on the machine shares.
func (cfg Config) tidyCheckoutWorktree(ctx context.Context, git worktree.RunnerGit, dir reap.WorktreeDir, record *Record) {
	if dir.Source != reap.SourceCheckout || dir.Registration != reap.RegistrationListed {
		return
	}
	if cfg.inATaskFolder(dir.Path) {
		record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Detail: "it is inside a live task's own folders, so it goes with that task at its cleanup or its next relaunch"})
		return
	}
	if reason := cfg.notYetOrphaned(dir); reason != "" {
		record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Detail: reason})
		return
	}
	bytes := Size(dir.Path)
	detail, err := cfg.returnWorktree(ctx, git, dir)
	if err != nil {
		record.Kept = append(record.Kept, Item{Kind: "worktree", Path: dir.Path, Bytes: bytes, Detail: err.Error()})
		return
	}
	record.Removed = append(record.Removed, Item{Kind: "worktree", Path: dir.Path, Bytes: bytes, Detail: detail})
}

// notYetOrphaned says why a worktree the inventory found unowned is not
// treated as an orphan yet, or "" when it is one: it is too new, or a task
// record read now, after the inventory, owns it.
func (cfg Config) notYetOrphaned(dir reap.WorktreeDir) string {
	if dir.Created.IsZero() || cfg.Now.Sub(dir.Created) < worktreeGrace {
		return "made less than an hour ago, or when cannot be read; a spawn may not have recorded it yet"
	}
	owner, known, err := reap.WorktreeOwner(cfg.Home.State, cfg.Home.WorktreeRoots(), dir.Project, dir.Path)
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
