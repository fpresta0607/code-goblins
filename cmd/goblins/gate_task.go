package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/reap"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func gateTask(root string, runtime commandRuntime) (string, error) {
	root, err := fsx.AbsClean(root)
	if err != nil {
		return "", err
	}
	root = fsx.LongPath(root)
	worktrees := filepath.Dir(filepath.Dir(root))
	if !fsx.EqualPath(worktrees, filepath.Join(filepath.Dir(worktrees), "worktrees")) {
		return taskID(os.Getenv), nil
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return "", err
	}
	// A goblin's own worktree in the home has the gate's shape too,
	// <root>\worktrees\<project>\<task>, and is no gate run.
	if slices.ContainsFunc(h.WorktreeRoots(), func(root string) bool { return fsx.SamePath(worktrees, root) }) {
		return taskID(os.Getenv), nil
	}
	const budget = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	reader := pipeline.Reader{Root: filepath.Dir(worktrees), Commands: gatedb.Runner{Next: execx.OSRunner{}}}
	source, err := reader.WorktreeSource(ctx, root)
	if errors.Is(err, context.DeadlineExceeded) {
		return "", fmt.Errorf("the gate's database gave no answer about this run within %s: %w", budget, err)
	}
	if err != nil {
		return "", err
	}
	worktree, err := branchWorktree(source.Project, source.Branch)
	if err != nil {
		return "", fmt.Errorf("cannot inspect the gate's source worktrees: %w", err)
	}
	if worktree == "" {
		return "", fmt.Errorf("%w: no source worktree holds the gate's branch %q", errNoFleetTask, source.Branch)
	}
	return gateWorktreeTask(h, source.Project, worktree)
}

// branchWorktree is the worktree of project that has branch checked out, or
// empty when none has. It reads git's own records, the main worktree's HEAD
// and each linked worktree's HEAD and gitdir under the common git folder,
// since git worktree list took 12.8 s in a project of 48 worktrees on a
// loaded host, past any budget a gate turn can wait (#2627). A record that
// is half made or half removed is skipped, as git skips it. A project whose
// .git is a file has its common folder where that file and the commondir it
// leads to say, and one that keeps its refs in a reftable, whose HEAD files
// name no branch, is read by git (listedWorktree).
func branchWorktree(project, branch string) (string, error) {
	main, common := project, filepath.Join(project, ".git")
	info, err := os.Stat(common)
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() {
		content, err := fsx.ReadFile(common)
		if err != nil {
			return "", err
		}
		link, isLink := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir: ")
		if !isLink || link == "" {
			return "", fmt.Errorf("%s names no git folder", common)
		}
		common = recordedPath(project, link)
		content, err = fsx.ReadFile(filepath.Join(common, "commondir"))
		if err == nil {
			common = recordedPath(common, string(content))
			main = filepath.Dir(common)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	if _, err := os.Stat(filepath.Join(common, "reftable")); !errors.Is(err, fs.ErrNotExist) {
		return listedWorktree(project, branch)
	}
	want := "ref: refs/heads/" + branch
	head, err := fsx.ReadFile(filepath.Join(common, "HEAD"))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(head)) == want {
		return main, nil
	}
	records := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(records)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		record := filepath.Join(records, entry.Name())
		head, err := fsx.ReadFile(filepath.Join(record, "HEAD"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(string(head)) != want {
			continue
		}
		link, err := fsx.ReadFile(filepath.Join(record, "gitdir"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		return filepath.Dir(recordedPath(record, string(link))), nil
	}
	return "", nil
}

// recordedPath is the path a git record holds, read relative to dir when it
// is relative, as git writes it under worktree.useRelativePaths.
func recordedPath(dir, recorded string) string {
	path := filepath.FromSlash(strings.TrimSpace(recorded))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path)
}

// listedWorktree is branchWorktree for a project that keeps its refs in a
// reftable, read by git worktree list within a budget of its own.
func listedWorktree(project, branch string) (string, error) {
	const budget = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: "git", Args: []string{"worktree", "list", "--porcelain", "-z"}, Dir: project})
	if err != nil {
		return "", fmt.Errorf("git worktree list --porcelain -z in a reftable project, given %s: %w", budget, err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git worktree list --porcelain -z in a reftable project exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var worktree string
	for _, field := range strings.Split(string(result.Stdout), "\x00") {
		if field == "" {
			worktree = ""
		} else if dir, ok := strings.CutPrefix(field, "worktree "); ok {
			worktree = filepath.FromSlash(dir)
		} else if field == "branch refs/heads/"+branch {
			return worktree, nil
		}
	}
	return "", nil
}

// errNoFleetTask says a gate run's source is no fleet task's, as the
// Overlord's own repositories' runs are: gateTask read it, and found none.
var errNoFleetTask = errors.New("the gate's source is no fleet task's")

// taskLabel names a fleet task to whoever reads the line of turns: its
// goblin as Name (id), as the fleet names a goblin everywhere, or the id
// alone when the task's record names no goblin or cannot be read.
func taskLabel(task string, runtime commandRuntime) string {
	h, err := runtime.resolveHome()
	if err != nil {
		return task
	}
	meta, err := state.ReadTaskMeta(h.State, task)
	if err != nil || meta.GoblinName == "" {
		return task
	}
	return meta.GoblinName + " (" + task + ")"
}

// taskPriority is the priority on task's backlog row, which orders its runs in
// the machine's heavy line, and empty for no task, a task with no row and a
// row that names none. A backlog that cannot be read costs the run only its
// priority, which it says on says under label.
func taskPriority(label, task string, says io.Writer, runtime commandRuntime) string {
	if task == "" {
		return ""
	}
	h, err := runtime.resolveHome()
	var backlog fleet.BacklogRows
	if err == nil {
		backlog, err = fleet.ReadBacklog(h)
	}
	if err != nil {
		fmt.Fprintf(says, "%s: its task's priority cannot be read, so it takes a place in line as a run with no task: %v\n", label, err)
		return ""
	}
	for _, row := range slices.Concat(backlog.Queued, backlog.Parked) {
		if row.ID == task {
			return row.Priority
		}
	}
	return ""
}

func gateWorktreeTask(h home.Home, project, worktree string) (string, error) {
	owner, known, err := reap.WorktreeOwner(h.State, h.WorktreeRoots(), project, worktree)
	if err != nil {
		return "", err
	}
	if !known {
		return "", fmt.Errorf("%w: its source worktree belongs to no fleet task", errNoFleetTask)
	}
	// A name alone is no proof: the owner's record must name its own
	// worktree where spawn puts it, in one of the home's worktree folders or
	// where an older build did.
	if !fsx.SamePath(owner.Meta.Project, project) || !slices.ContainsFunc(h.OwnWorktrees(project, owner.ID), func(own string) bool { return fsx.SamePath(owner.Meta.Worktree, own) }) {
		return "", errors.New("task metadata does not match the gate's source project and worktree")
	}
	return owner.ID, nil
}
