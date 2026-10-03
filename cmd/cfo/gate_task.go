package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func gateTask(root string, runtime commandRuntime) (string, error) {
	root, err := fsx.Canonical(root)
	if err != nil {
		return "", err
	}
	worktrees := filepath.Dir(filepath.Dir(root))
	if !strings.EqualFold(filepath.Base(worktrees), "worktrees") {
		return taskID(os.Getenv), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader := pipeline.Reader{Root: filepath.Dir(worktrees), Commands: execx.OSRunner{}}
	source, err := reader.WorktreeSource(ctx, root)
	if err != nil {
		return "", err
	}
	result, err := reader.Commands.Run(ctx, execx.Request{Name: "git", Args: []string{"worktree", "list", "--porcelain", "-z"}, Dir: source.Project})
	if err != nil || result.ExitCode != 0 {
		return "", errors.New("cannot inspect the gate's source worktrees")
	}
	h, err := runtime.resolveHome()
	if err != nil {
		return "", err
	}
	var worktree string
	for _, field := range strings.Split(string(result.Stdout), "\x00") {
		if field == "" {
			worktree = ""
		} else if dir, ok := strings.CutPrefix(field, "worktree "); ok {
			worktree = filepath.FromSlash(dir)
		} else if field == "branch refs/heads/"+source.Branch {
			return gateWorktreeTask(h.State, source.Project, worktree)
		}
	}
	return "", fmt.Errorf("no source worktree holds the gate's branch %q", source.Branch)
}

func gateWorktreeTask(stateDir, project, worktree string) (string, error) {
	if !fsx.SamePath(filepath.Dir(worktree), filepath.Join(project, ".worktrees")) {
		return "", errors.New("the gate's source worktree belongs to no fleet task")
	}
	id, hasPrefix := strings.CutPrefix(filepath.Base(worktree), "gb-")
	if !hasPrefix {
		return "", errors.New("the gate's source worktree names no fleet task")
	}
	// An extra worktree keeps its owner's ID followed by a suffix. Prefer an
	// exact task record before trying the progressively shorter owner IDs.
	for id != "" {
		meta, err := state.ReadTaskMeta(stateDir, id)
		if err == nil {
			if !fsx.SamePath(meta.Project, project) || !fsx.SamePath(meta.Worktree, filepath.Join(project, ".worktrees", "gb-"+id)) {
				return "", errors.New("task metadata does not match the gate's source project and worktree")
			}
			return meta.ID, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		separator := strings.LastIndexByte(id, '-')
		if separator < 0 {
			break
		}
		id = id[:separator]
	}
	return "", errors.New("the gate's source worktree has no task record")
}
