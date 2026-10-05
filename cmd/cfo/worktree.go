package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const worktreeUsage = `usage: cfo worktree add <task-id> <name> [--ref <commit>]
       cfo worktree remove <task-id> <name>

add makes the task an extra worktree, <task-id>-<name>, beside its own in the
home's worktrees folder, detached at --ref or at origin's default branch, and
records it on the task, so it is the task's: cfo cleanup removes it with the
task. A worktree made any other way is a stray the janitor reports.

remove returns one extra worktree before the task ends: it refuses one with
uncommitted work, keeps work that is not on the default branch as a local
archive tag, and takes it off the task's record.
`

// runWorktree adds or removes a task's extra worktree.
func runWorktree(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || (args[0] != "add" && args[0] != "remove") {
		fmt.Fprint(stderr, worktreeUsage)
		return 2
	}
	verb := args[0]
	flags := flag.NewFlagSet("cfo worktree "+verb, flag.ContinueOnError)
	flags.SetOutput(stderr)
	ref := flags.String("ref", "", "the commit the extra worktree starts at; origin's default branch when not given")
	var positional []string
	rest := args[1:]
	for len(rest) > 0 {
		if err := flags.Parse(rest); err != nil {
			return 2
		}
		rest = flags.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 2 || (verb == "remove" && *ref != "") {
		fmt.Fprint(stderr, worktreeUsage)
		return 2
	}
	id, name := positional[0], positional[1]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "cfo worktree: not a primary home")
		return 1
	}
	commands := execx.OSRunner{}
	service := worktree.Service{Commands: commands, Root: h.Worktrees()}
	ctx := context.Background()
	var output string
	if verb == "add" {
		output, err = addExtraWorktree(ctx, h, service, id, name, *ref)
	} else {
		output, err = removeExtraWorktree(ctx, h, service, worktree.RunnerGit{Commands: commands}, id, name)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, output)
	return 0
}

// updateTaskRecord reads, changes and writes task id's record under its
// metadata lock, so no other command's write of the record is lost.
func updateTaskRecord(stateDir, id string, change func(*state.TaskMeta) error) (err error) {
	if _, err := lock.AcquireExclusiveNamed(stateDir, state.MetadataLockName(id)); err != nil {
		return fmt.Errorf("cfo worktree: lock task %s's record: %w", id, err)
	}
	defer func() {
		if releaseErr := lock.ReleaseExclusiveNamed(stateDir, state.MetadataLockName(id)); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	meta, err := state.ReadTaskMeta(stateDir, id)
	if err != nil {
		return fmt.Errorf("cfo worktree: read task %s: %w", id, err)
	}
	if err := change(&meta); err != nil {
		return err
	}
	return state.WriteTaskMeta(stateDir, meta)
}

func addExtraWorktree(ctx context.Context, h home.Home, service worktree.Service, id, name, ref string) (string, error) {
	var path string
	err := updateTaskRecord(h.State, id, func(meta *state.TaskMeta) error {
		wt, err := service.AddExtra(ctx, meta.Project, id, name, ref)
		if err != nil {
			return err
		}
		path = wt.Path
		meta.Extras = append(meta.Extras, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("added %s's extra worktree %s; cfo cleanup %s removes it with the task", id, path, id), nil
}

func removeExtraWorktree(ctx context.Context, h home.Home, service worktree.Service, git worktree.RunnerGit, id, name string) (string, error) {
	var path, tag string
	err := updateTaskRecord(h.State, id, func(meta *state.TaskMeta) error {
		want, err := service.Path(meta.Project, id+"-"+name)
		if err != nil {
			return err
		}
		at := slices.IndexFunc(meta.Extras, func(extra string) bool { return strings.EqualFold(filepath.Clean(extra), want) })
		if at < 0 {
			return fmt.Errorf("cfo worktree: task %s records no extra worktree %s", id, want)
		}
		path = meta.Extras[at]
		status, err := git.Commands.Run(ctx, execx.Request{Dir: path, Name: "git", Args: []string{"status", "--porcelain=v1", "--untracked-files=all"}})
		if err != nil || status.ExitCode != 0 {
			return fmt.Errorf("cfo worktree: read %s's status, so whether it holds uncommitted work is unknown: %v", path, err)
		}
		if dirty := strings.TrimSpace(string(status.Stdout)); dirty != "" {
			return fmt.Errorf("cfo worktree: %s has uncommitted work; commit or discard it first:\n%s", path, dirty)
		}
		landing, err := git.Landing(ctx, path)
		if err != nil {
			return err
		}
		if !landing.Landed {
			if tag, err = git.ArchiveTag(ctx, path, landing, filepath.Base(path)); err != nil {
				return err
			}
		}
		if err := service.Return(ctx, meta.Project, path); err != nil {
			return err
		}
		meta.Extras = slices.Delete(meta.Extras, at, at+1)
		return nil
	})
	if err != nil {
		return "", err
	}
	output := fmt.Sprintf("removed %s's extra worktree %s", id, path)
	if tag != "" {
		output += "; its unlanded work is kept as the local tag " + tag
	}
	return output, nil
}
