package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

type Preservation struct {
	CanRemove bool
	Branch    string
	Kept      []string
}

// PreserveWork keeps whatever of the task's work is not proven safe
// elsewhere, and says whether its worktree may go. A helper's work is safe
// once its parent's branch holds it, which holder, the parent's worktree,
// shows; holder is empty for any other task.
func PreserveWork(ctx context.Context, commands execx.Runner, meta state.TaskMeta, holder string) (Preservation, error) {
	result := Preservation{Kept: []string{"worktree " + meta.Worktree}}
	git := func(args ...string) (execx.Result, error) {
		return commands.Run(ctx, execx.Request{Dir: meta.Worktree, Name: "git", Args: args})
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil || top.ExitCode != 0 {
		return result, errors.New("worktree could not be verified; left in place")
	}
	if !strings.EqualFold(filepath.Clean(strings.TrimSpace(string(top.Stdout))), filepath.Clean(meta.Worktree)) {
		return result, errors.New("git answered for a different checkout; left untouched")
	}
	status, err := git("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status.ExitCode != 0 {
		return result, errors.New("worktree changes could not be read; left in place")
	}
	if len(strings.TrimSpace(string(status.Stdout))) != 0 {
		result.Kept[0] += " (uncommitted or untracked work)"
		return result, nil
	}
	branch, err := git("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch.ExitCode != 0 {
		result.Kept[0] += " (detached or unreadable branch)"
		return result, nil
	}
	result.Branch = strings.TrimSpace(string(branch.Stdout))
	if result.Branch == "" {
		return result, errors.New("git returned an empty branch; worktree kept")
	}
	result.Kept = append(result.Kept, "branch "+result.Branch)
	head, err := git("rev-parse", "HEAD")
	if err != nil || head.ExitCode != 0 || len(strings.TrimSpace(string(head.Stdout))) == 0 {
		return result, errors.New("branch head could not be read; worktree kept")
	}
	if holder != "" {
		if isHeld, err := (worktree.RunnerGit{Commands: commands}).HoldsHead(ctx, holder, meta.Worktree); err == nil && isHeld {
			result.CanRemove = true
			result.Kept = []string{fmt.Sprintf("branch %s, merged into its parent %s's branch", result.Branch, meta.Parent)}
			return result, nil
		}
	}
	ref := "refs/heads/" + result.Branch
	remote, remoteErr := git("ls-remote", "--exit-code", "origin", ref)
	isPushed := func(remote execx.Result, err error) bool {
		fields := strings.Fields(string(remote.Stdout))
		return err == nil && remote.ExitCode == 0 && len(fields) == 2 && fields[0] == strings.TrimSpace(string(head.Stdout)) && fields[1] == ref
	}
	if !isPushed(remote, remoteErr) && meta.Mode == "direct-PR" {
		pushed, err := git("push", "origin", "HEAD:"+ref)
		if err != nil || pushed.ExitCode != 0 {
			result.Kept[0] += " (push did not complete)"
			return result, nil
		}
		remote, remoteErr = git("ls-remote", "--exit-code", "origin", ref)
	}
	if !isPushed(remote, remoteErr) {
		result.Kept[0] += " (commits not verified on origin)"
		return result, nil
	}
	result.CanRemove = true
	result.Kept = []string{fmt.Sprintf("branch %s, verified on origin", result.Branch)}
	return result, nil
}
