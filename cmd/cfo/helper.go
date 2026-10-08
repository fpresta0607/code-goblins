package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// runHelper is a goblin's way to a helper goblin of its own:
//
//	cfo helper start <parent-id> --brief <file> [--title "<short title>"]
//	cfo helper merge <parent-id>
//
// start hands the brief's text to the supervisor, which alone starts
// helpers: one per goblin at a time, never a helper's, and only when memory
// allows. It prints the helper and its branch, or why not and when to ask
// again. merge brings the helper's branch into the goblin's own with a merge
// commit and retires the helper; the goblin stays the one who opens the pull
// request.
func runHelper(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, `usage: cfo helper start <parent-id> --brief <file> [--title "<short title>"] | cfo helper merge <parent-id>`)
		return 2
	}
	switch args[0] {
	case "start":
		return runHelperStart(args[1:], stdout, stderr, runtime)
	case "merge":
		return runHelperMerge(args[1:], stdout, stderr, runtime)
	}
	fmt.Fprintf(stderr, "cfo helper: unknown subcommand %q\n", args[0])
	return 2
}

func runHelperStart(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || state.ValidTaskID(args[0]) != nil {
		fmt.Fprintln(stderr, "cfo helper start: your task ID comes first: cfo helper start <parent-id> --brief <file>")
		return 2
	}
	parent := args[0]
	flags := flag.NewFlagSet("helper start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	brief := flags.String("brief", "", "the file holding the helper's brief: what it does, and how it knows it is done")
	givenTitle := flags.String("title", "", "the helper's short title for the board")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if *brief == "" {
		fmt.Fprintln(stderr, "cfo helper start: --brief <file> is required")
		return 2
	}
	title := ""
	if *givenTitle != "" {
		var err error
		if title, err = shortTitle(*givenTitle); err != nil {
			fmt.Fprintf(stderr, "cfo helper start: --title: %v\n", err)
			return 2
		}
	}
	text, err := fsx.ReadFile(*brief)
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper start: %v\n", err)
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	started, err := runtime.requestHelper(h, supervisor.HelperRequest{Parent: parent, Brief: string(text), Title: title})
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper start: not started: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "starting helper %s on branch %s, cut from your last commit; you are told here when it is up, and it reports to you here. While you only wait on it, run: cfo notify %s --waiting-on %s \"<why>\"\n", started.ID, started.Branch, parent, started.ID)
	return 0
}

// runHelperMerge merges the goblin's helper into the goblin's own branch, in
// its own worktree, with a merge commit of the commit the helper's clean
// worktree has checked out, and then retires the helper through Stop, which
// lets its worktree go once the goblin's branch holds its work. A conflict is
// left for the goblin to resolve and commit; asked again, it only retires
// the helper.
func runHelperMerge(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 1 || state.ValidTaskID(args[0]) != nil {
		fmt.Fprintln(stderr, "usage: cfo helper merge <parent-id>")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	parent, err := state.ReadTaskMeta(h.State, args[0])
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper merge: %s is not a live task: %v\n", args[0], err)
		return 1
	}
	helpers, err := state.HelpersOf(h.State, parent.ID)
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper merge: %v\n", err)
		return 1
	}
	if len(helpers) == 0 {
		fmt.Fprintf(stderr, "cfo helper merge: %s has no live helper to merge\n", parent.ID)
		return 1
	}
	helper := helpers[0]
	ctx := context.Background()
	commands := execx.OSRunner{}
	// git answers with what it printed, or with what it said went wrong.
	git := func(dir string, args ...string) (string, int, error) {
		result, err := commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
		if err == nil && result.ExitCode != 0 {
			return strings.TrimSpace(string(result.Stdout) + " " + string(result.Stderr)), result.ExitCode, nil
		}
		return strings.TrimSpace(string(result.Stdout)), result.ExitCode, err
	}
	for _, side := range []struct{ who, dir, fix string }{
		{helper.ID, helper.Worktree, "ask it to commit them with cfo send " + helper.ID + " \"<what>\""},
		{"your worktree", parent.Worktree, "commit them, or finish the merge you are resolving"},
	} {
		changes, code, err := git(side.dir, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil || code != 0 {
			fmt.Fprintf(stderr, "cfo helper merge: the changes in %s cannot be read: %s %v\n", side.dir, changes, err)
			return 1
		}
		if changes != "" {
			fmt.Fprintf(stderr, "cfo helper merge: %s has uncommitted changes in %s; %s, then run this again\n", side.who, side.dir, side.fix)
			return 1
		}
	}
	branch, code, err := git(parent.Worktree, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || code != 0 || branch == "" {
		fmt.Fprintf(stderr, "cfo helper merge: your worktree is on no branch; check out your branch, then run this again\n")
		return 1
	}
	helperBranch, _, _ := git(helper.Worktree, "symbolic-ref", "--quiet", "--short", "HEAD")
	head, code, err := git(helper.Worktree, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || code != 0 || head == "" {
		fmt.Fprintf(stderr, "cfo helper merge: %s's last commit cannot be read in %s\n", helper.ID, helper.Worktree)
		return 1
	}
	isHeld, err := (worktree.RunnerGit{Commands: commands}).HoldsHead(ctx, parent.Worktree, helper.Worktree)
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper merge: %v\n", err)
		return 1
	}
	if isHeld {
		fmt.Fprintf(stdout, "%s already holds %s's work\n", branch, helper.ID)
	} else {
		title := helper.Title
		if title == "" {
			title = "its brief"
		}
		subject := "Merge helper " + helper.ID + " (" + helperBranch + "): " + title
		if output, code, err := git(parent.Worktree, "merge", "--no-ff", "--no-edit", "-m", subject, head); err != nil || code != 0 {
			conflicts, _, _ := git(parent.Worktree, "diff", "--name-only", "--diff-filter=U")
			if conflicts == "" {
				fmt.Fprintf(stderr, "cfo helper merge: git merge failed: %s %v\n", output, err)
				return 1
			}
			fmt.Fprintf(stderr, "cfo helper merge: the merge of %s conflicts in %s; resolve them in your worktree, commit the merge, then run cfo helper merge %s again to retire %s\n", helperBranch, strings.Join(strings.Fields(conflicts), ", "), parent.ID, helper.ID)
			return 1
		}
		fmt.Fprintf(stdout, "merged %s (%s at %s) into %s with a merge commit\n", helper.ID, helperBranch, head[:min(12, len(head))], branch)
	}
	request := lifecycle.Request{ID: helper.ID, Generation: helper.SpawnGen, Operation: fmt.Sprintf("merge-%d", time.Now().UnixNano()), Action: "stop", Reason: "Merged into its parent " + parent.ID + "'s branch " + branch}
	retired, err := runtime.taskLifecycle(ctx, h, request, "")
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper merge: %s holds the work, but %s could not be retired: %v; stop it with cfo kill %s\n", branch, helper.ID, err, helper.ID)
		return 1
	}
	fmt.Fprintf(stdout, "retired %s: %s; you stay the one who opens the pull request\n", helper.ID, strings.Join(retired.Kept, "; "))
	return 0
}
