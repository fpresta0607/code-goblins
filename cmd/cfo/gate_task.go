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
	"github.com/fpresta0607/code-goblins/internal/home"
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
	h, err := runtime.resolveHome()
	if err != nil {
		return "", err
	}
	// A goblin's own worktree in the home has the gate's shape too,
	// <root>\worktrees\<project>\<task>, and is no gate run.
	if fsx.SamePath(worktrees, h.Worktrees()) {
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
	var worktree string
	for _, field := range strings.Split(string(result.Stdout), "\x00") {
		if field == "" {
			worktree = ""
		} else if dir, ok := strings.CutPrefix(field, "worktree "); ok {
			worktree = filepath.FromSlash(dir)
		} else if field == "branch refs/heads/"+source.Branch {
			return gateWorktreeTask(h, source.Project, worktree)
		}
	}
	return "", fmt.Errorf("no source worktree holds the gate's branch %q", source.Branch)
}

func gateWorktreeTask(h home.Home, project, worktree string) (string, error) {
	owner, known, err := reap.WorktreeOwner(h.State, h.Worktrees(), project, worktree)
	if err != nil {
		return "", err
	}
	if !known {
		return "", errors.New("the gate's source worktree belongs to no fleet task")
	}
	if !fsx.SamePath(owner.Meta.Project, project) {
		return "", errors.New("task metadata does not match the gate's source project and worktree")
	}
	return owner.ID, nil
}
