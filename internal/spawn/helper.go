package spawn

import (
	"context"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// helperBase is where a helper starts: its parent's record, the parent's
// branch and last commit, and the helper's own branch, cut from them.
type helperBase struct {
	parent       state.TaskMeta
	parentBranch string
	head         string
	branch       string
}

// helperStart reads where task req.ID, a helper of req.Parent, starts, or
// says why it cannot. It runs under the spawn lock, so the caps it checks
// hold until the helper's record is published. The helper's branch is its
// parent's with the helper's own suffix, <parent-branch>-h<n> for helper
// <parent>-h<n>, at the parent's last commit.
func (s Service) helperStart(ctx context.Context, req Request, project string) (helperBase, error) {
	parent, err := state.HelperParent(s.StateDir, req.Parent)
	if err != nil {
		return helperBase{}, fmt.Errorf("spawn: %w", err)
	}
	if !fsx.SamePath(parent.Project, project) {
		return helperBase{}, fmt.Errorf("spawn: a helper works in its parent's project %s, not %s", parent.Project, project)
	}
	suffix, isNamed := strings.CutPrefix(strings.ToLower(req.ID), strings.ToLower(req.Parent)+"-")
	if !isNamed || suffix == "" {
		return helperBase{}, fmt.Errorf("spawn: a helper of %s is named %s-<name>, not %s", req.Parent, req.Parent, req.ID)
	}
	git := func(args ...string) (string, bool) {
		result, err := s.commands().Run(ctx, execx.Request{Dir: parent.Worktree, Name: "git", Args: args})
		return strings.TrimSpace(string(result.Stdout)), err == nil && result.ExitCode == 0
	}
	base := helperBase{parent: parent}
	var isBranch, isCommit bool
	base.parentBranch, isBranch = git("symbolic-ref", "--quiet", "--short", "HEAD")
	if !isBranch || base.parentBranch == "" {
		return helperBase{}, fmt.Errorf("spawn: %s's worktree is on no branch, and a helper's branch is cut from its parent's; commit your work on a branch, then ask again", req.Parent)
	}
	base.head, isCommit = git("rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if !isCommit || base.head == "" {
		return helperBase{}, fmt.Errorf("spawn: %s's last commit cannot be read in %s", req.Parent, parent.Worktree)
	}
	base.branch = base.parentBranch + req.ID[len(req.Parent):]
	return base, nil
}

// helperInstruction tells a helper how to report: to its parent, which
// merges its branch and opens the pull request, never to the CFO.
func helperInstruction(exe string, meta state.TaskMeta) string {
	id, parent := meta.ID, meta.Parent
	return " You are a helper goblin of " + parent + ": your worktree is on a branch of your own, cut from " + parent + "'s last commit, and you report to " + parent + ", not to the CFO." +
		" Commit your work on your branch, but never push it and never open a pull request: " + parent + " merges your branch into its own and opens the pull request." +
		" When your work is committed and ready for " + parent + " to merge, run: " + exe + " notify " + id + " --done." +
		" When blocked on a decision run: " + exe + " notify " + id + " --blocked \"<question>\", one short sentence that is the question with the details on lines of their own that start with \"- \"; " + parent + " answers it, and its answer arrives here as a message. Never wait for a choice you can undo: take the better option, say which with --working, and keep going." +
		" On failure run: " + exe + " notify " + id + " --failed \"<reason>\". To say what you are doing run: " + exe + " notify " + id + " --working \"<what>\"." +
		" You cannot start helpers of your own."
}

// helperOffer tells a goblin it may ask the supervisor for one helper.
func helperOffer(exe, id string) string {
	return " When a separate piece of your task can run beside yours, you may ask the supervisor for one helper goblin: write its brief to a file and run: " + exe + " helper start " + id + " --brief <file> --title \"<short title>\". It starts only when memory allows, works on a branch cut from your last commit and reports to you here; the command says when to ask again if it is refused." +
		" While you only wait on it, run: " + exe + " notify " + id + " --waiting-on <helper-id> \"<why>\". When it reports done, merge its work into your branch with: " + exe + " helper merge " + id + ", which retires it; you stay the one who opens the pull request. Stop a helper you no longer need with: " + exe + " kill <helper-id> --reason \"<why>\"."
}
