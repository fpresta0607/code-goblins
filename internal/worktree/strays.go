package worktree

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Stray is one worktree a project registers inside a task's own folders and
// how returning it went: one a harness's own tooling made, in a scratchpad or
// inside the checkout it works in, rather than cfo, which keeps its goblin
// worktrees under the home.
type Stray struct {
	Path string
	// Tag is the local archive tag that keeps its unlanded work, empty when
	// its work was already on the default branch.
	Tag string
	// Kept is why it is still there, empty when it was returned.
	Kept string
}

// WorktreeList lists the worktrees the repository at dir registers, by git's
// own record, the repository's primary checkout first. A path git still lists
// whose directory is gone is an entry awaiting a prune and is left out: it is
// no directory anything can return.
func WorktreeList(ctx context.Context, commands execx.Runner, dir string) ([]string, error) {
	if commands == nil {
		return nil, errors.New("worktree: command runner is required")
	}
	result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: []string{"worktree", "list", "--porcelain"}})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, commandFailure("git worktree list --porcelain", result)
	}
	var paths []string
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		path = filepath.Clean(path)
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("worktree: read registered path %q: %w", path, err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// Strays lists the worktrees project registers that lie inside one of folders
// and are none of them: the folders are a task's own, its worktree, its extra
// worktrees and its scratch folder, so what is left is a harness's own
// scratchpad worktree or one it made in the checkout it works in. The
// project's own checkout and the folders themselves are never listed, since
// returning either is not this scan's business.
func Strays(ctx context.Context, commands execx.Runner, project string, folders []string) ([]string, error) {
	registered, err := WorktreeList(ctx, commands, project)
	if err != nil {
		return nil, err
	}
	var excluded []os.FileInfo
	for _, folder := range append([]string{project}, folders...) {
		if folder == "" {
			continue
		}
		info, err := os.Stat(folder)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		excluded = append(excluded, info)
	}
	var strays []string
	for _, path := range registered {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(excluded, func(folder os.FileInfo) bool { return os.SameFile(info, folder) }) {
			continue
		}
		for _, folder := range folders {
			if folder == "" {
				continue
			}
			if info, err := os.Stat(folder); err == nil && Under(path, info) {
				strays = append(strays, path)
				break
			}
		}
	}
	return strays, nil
}

// ReturnStrays returns every worktree Strays lists, keeping unlanded work as a
// local archive tag first, as cfo cleanup keeps a goblin's own. A worktree
// holding uncommitted work is kept and named: nothing in the fleet discards
// work, and a harness's scratchpad is no exception. One folder's failure does
// not stop the rest, and each is reported as it ended.
func ReturnStrays(ctx context.Context, commands execx.Runner, git Git, project string, folders []string) ([]Stray, error) {
	if git == nil {
		return nil, errors.New("worktree: Git is required")
	}
	paths, err := Strays(ctx, commands, project, folders)
	if err != nil {
		return nil, err
	}
	// Return children first, so returning an ignored parent cannot remove
	// another worktree before that worktree's own preservation checks.
	slices.SortFunc(paths, func(left, right string) int {
		return cmp.Compare(strings.Count(right, string(filepath.Separator)), strings.Count(left, string(filepath.Separator)))
	})
	returned := make([]Stray, 0, len(paths))
	for _, path := range paths {
		stray := Stray{Path: path}
		landing, err := git.Landing(ctx, path)
		if err != nil {
			stray.Kept = "its work could not be read, so nothing was returned: " + err.Error()
			returned = append(returned, stray)
			continue
		}
		if !landing.Landed {
			tag, err := git.ArchiveTag(ctx, path, landing, filepath.Base(path))
			if err != nil {
				stray.Kept = "its unlanded work could not be kept as an archive tag, so it stays: " + err.Error()
				returned = append(returned, stray)
				continue
			}
			stray.Tag = tag
		}
		if err := git.Return(ctx, project, path); err != nil {
			stray.Kept = err.Error()
			returned = append(returned, stray)
			continue
		}
		returned = append(returned, stray)
	}
	return returned, nil
}

// StrayNote renders what became of each stray, one line each, as a command's
// output carries them. owner names the command that left one alone, such as
// "cleanup" or "relaunch".
func StrayNote(returned []Stray, owner string) string {
	note := ""
	for _, stray := range returned {
		switch {
		case stray.Kept != "":
			note += fmt.Sprintf("\nkept the harness worktree %s, which the %s does not touch: %s", stray.Path, owner, stray.Kept)
		case stray.Tag != "":
			note += fmt.Sprintf("\nreturned harness worktree %s, its unlanded work kept as the local tag %s", stray.Path, stray.Tag)
		default:
			note += "\nreturned harness worktree " + stray.Path
		}
	}
	return note
}

// Under reports whether path lies inside folder, by walking path's parents and
// comparing each with folder's own directory identity, whose info the caller
// reads once. A comparison by spelling is wrong here: git spells a path through
// an 8.3 folder name, as C:\Users\RUNNER~1\... for C:\Users\runneradmin\...,
// while the home records the path in full, and git resolves a junction to its
// target while the scan holds the junction's own name.
func Under(path string, folderInfo os.FileInfo) bool {
	if folderInfo == nil {
		return false
	}
	for current := filepath.Dir(filepath.Clean(path)); ; current = filepath.Dir(current) {
		if info, err := os.Stat(current); err == nil && os.SameFile(info, folderInfo) {
			return true
		}
		if parent := filepath.Dir(current); parent == current {
			return false
		}
	}
}
