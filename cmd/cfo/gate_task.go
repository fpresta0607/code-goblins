package main

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
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/reap"
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
	if !strings.HasPrefix(filepath.Base(worktree), "gb-") {
		return "", errors.New("the gate's source worktree names no fleet task")
	}
	owner, known, err := reap.WorktreeOwner(stateDir, project, worktree)
	if err != nil {
		return "", err
	}
	if !known {
		return "", errors.New("the gate's source worktree has no readable task record or owner")
	}
	if !fsx.SamePath(owner.Meta.Project, project) || !fsx.SamePath(owner.Meta.Worktree, filepath.Join(project, ".worktrees", "gb-"+owner.ID)) {
		return "", errors.New("task metadata does not match the gate's source project and worktree")
	}
	return owner.ID, nil
}
