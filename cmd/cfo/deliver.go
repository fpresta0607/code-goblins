package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// runBrief writes a task brief scaffold at data/<id>/brief.md and prints its
// absolute path. It refuses to overwrite an existing brief.
func runBrief(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo brief: task ID is required")
		return 2
	}
	id := args[0]
	if strings.HasPrefix(id, "-") {
		fmt.Fprintf(stderr, "cfo brief: unknown flag %q\n", id)
		return 2
	}
	fs := flag.NewFlagSet("brief", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "project checkout path")
	mode := fs.String("mode", "no-mistakes", "no-mistakes, direct-PR, or local-only")
	kind := fs.String("kind", "ship", "ship or scout")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *project == "" {
		fmt.Fprintln(stderr, "cfo brief: --project is required")
		return 2
	}
	if *kind != "ship" && *kind != "scout" {
		fmt.Fprintln(stderr, "cfo brief: --kind must be ship or scout")
		return 2
	}
	if !validSpawnMode(*mode) {
		fmt.Fprintln(stderr, "cfo brief: --mode must be no-mistakes, direct-PR, or local-only")
		return 2
	}
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	dir := filepath.Join(h.Data, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path := filepath.Join(dir, "brief.md")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stderr, "cfo brief: %s already exists; refusing to overwrite\n", path)
		return 1
	}
	body := fmt.Sprintf(`# Brief %s

## Project

%s

## Task

{TASK - what to build or learn, in one clear paragraph}

## Acceptance criteria

{ACCEPTANCE - concrete, verifiable outcomes}

## Constraints

{CONSTRAINTS - things not to touch, boundaries, non-goals}

## Authentication

Services this task needs are declared in %s.
Run %s before dispatch; add any service the task needs that the manifest does not list yet.

## Commits

Never name an AI product, company, model, agent, or assistant identity as a
commit co-author: not in a %s trailer, not anywhere else in a
commit message, and not in a pull request body. This is the Overlord's
standing rule, and the fleet's history must not credit an author that did not
exist.

## Delivery

kind: %s
mode: %s
`, id, *project, auth.ManifestPath("data", *project), "`cfo auth "+*project+" --fix`", "`Co-Authored-By`", *kind, *mode)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

// runPR handles "cfo pr check <id> <url>" and "cfo pr merge <url>".
func runPR(sub string, args []string, stdout, stderr io.Writer, commands execx.Runner) int {
	switch sub {
	case "check":
		return runPRCheck(args, stdout, stderr)
	case "merge":
		return runPRMerge(args, stdout, stderr, commands)
	default:
		fmt.Fprintf(stderr, "cfo pr: unknown subcommand %q (want check or merge)\n", sub)
		return 2
	}
}

// runPRCheck records pr= and pr_head= in state/<id>.meta for a goblin's opened PR.
func runPRCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "cfo pr check: <id> and <url> are required")
		return 2
	}
	id, url := args[0], args[1]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !strings.HasPrefix(url, "https://") {
		fmt.Fprintln(stderr, "cfo pr check: url must start with https://")
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	metaPath := filepath.Join(h.State, id+".meta")
	kv, _ := state.ReadMeta(metaPath)
	if kv == nil {
		kv = make(map[string]string)
	}
	kv["pr"] = url
	// Best-effort head resolution; a missing gh or a not-yet-created PR leaves pr_head unset.
	if head := prHead(url); head != "" {
		kv["pr_head"] = head
	}
	if err := state.WriteMeta(metaPath, kv); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "recorded pr=%s\n", url)
	return 0
}

// runPRMerge merges an open PR through the gh CLI. It never merges red work:
// the caller is responsible for confirming CI is green first.
//
// --delete-branch is deliberately NOT forwarded to gh. gh's flag deletes the
// local and the remote branch as one step, and git refuses to delete a branch
// that is checked out in a worktree - which is every goblin branch, because
// every goblin works in one. Forwarding it therefore lets a local deletion
// that cannot succeed decide whether the remote ref survives. The cleanup is
// done here as two independent steps instead, remote first.
//
// Neither cleanup step can fail the command. The merge is the irreversible
// half and has already succeeded by the time they run, so exiting non-zero
// over a leftover ref would report a successful merge as a failure.
func runPRMerge(args []string, stdout, stderr io.Writer, commands execx.Runner) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "cfo pr merge: <url> is required")
		return 2
	}
	url := args[0]
	fs := flag.NewFlagSet("pr-merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	method := fs.String("method", "merge", "merge, squash, or rebase")
	deleteBranch := fs.Bool("delete-branch", false, "delete the branch after merge")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if !strings.HasPrefix(url, "https://") {
		fmt.Fprintln(stderr, "cfo pr merge: url must start with https://")
		return 2
	}
	switch *method {
	case "merge", "squash", "rebase":
	default:
		fmt.Fprintln(stderr, "cfo pr merge: --method must be merge, squash, or rebase")
		return 2
	}
	ctx := context.Background()
	proof, err := verifyPRReady(ctx, url, commands)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	res, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"pr", "merge", url, "--" + *method, "--match-head-commit", proof.HeadRefOID}})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if res.ExitCode != 0 {
		fmt.Fprintf(stderr, "cfo pr merge: gh exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		return 1
	}
	fmt.Fprintln(stdout, strings.TrimSpace(string(res.Stdout)))
	if *deleteBranch {
		deleteMergedBranch(ctx, url, stdout, stderr, commands)
	}
	return 0
}

// deleteMergedBranch removes the head branch of a merged PR, remote ref first.
//
// It reports every failure as a warning rather than an error. The merge it
// follows has already landed, and a branch that outlives it is untidy rather
// than wrong - the operator can delete it later, which is not true of the
// merge.
func deleteMergedBranch(ctx context.Context, url string, stdout, stderr io.Writer, commands execx.Runner) {
	view, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{
		"pr", "view", url, "--json", "headRefName,headRefOid,headRepository,headRepositoryOwner",
	}})
	if err != nil {
		fmt.Fprintf(stderr, "cfo pr merge: merged, but the branch was left in place: %v\n", err)
		return
	}
	if view.ExitCode != 0 {
		fmt.Fprintf(stderr, "cfo pr merge: merged, but the branch was left in place: gh exited %d: %s\n", view.ExitCode, strings.TrimSpace(string(view.Stderr)))
		return
	}
	var head struct {
		HeadRefName    string `json:"headRefName"`
		HeadRefOid     string `json:"headRefOid"`
		HeadRepository struct {
			Name string `json:"name"`
		} `json:"headRepository"`
		HeadRepositoryOwner struct {
			Login string `json:"login"`
		} `json:"headRepositoryOwner"`
	}
	if err := json.Unmarshal(view.Stdout, &head); err != nil {
		fmt.Fprintf(stderr, "cfo pr merge: merged, but the branch was left in place: decode pr head: %v\n", err)
		return
	}
	owner, repo, branch := head.HeadRepositoryOwner.Login, head.HeadRepository.Name, head.HeadRefName
	if owner == "" || repo == "" || branch == "" {
		fmt.Fprintln(stderr, "cfo pr merge: merged, but the branch was left in place: pr head is missing an owner, repository, or branch name")
		return
	}

	// The remote ref is deleted first and on its own. It is the half that
	// matters - a stale remote branch is what clutters the fork and what the
	// next goblin sees - and doing it first means a local deletion that cannot
	// succeed never prevents it.
	ref := fmt.Sprintf("repos/%s/%s/git/refs/heads/%s", owner, repo, branch)
	del, err := commands.Run(ctx, execx.Request{Name: "gh", Args: []string{"api", "--method", "DELETE", ref}})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "cfo pr merge: merged, but the remote branch %s was left in place: %v\n", branch, err)
	// A repository with "Automatically delete head branches" enabled has
	// already removed the ref during the merge, and the DELETE says so. The
	// end state the flag asks for was reached, so warning that the branch was
	// left in place would be a warning about a branch that is not there.
	case del.ExitCode != 0 && refAlreadyGone(del.Stderr):
		fmt.Fprintf(stdout, "remote branch %s was already gone\n", branch)
	case del.ExitCode != 0:
		fmt.Fprintf(stderr, "cfo pr merge: merged, but the remote branch %s was left in place: gh exited %d: %s\n", branch, del.ExitCode, strings.TrimSpace(string(del.Stderr)))
	default:
		fmt.Fprintf(stdout, "deleted remote branch %s\n", branch)
	}

	// The local branch is identified by the commit GitHub merged, never by its
	// name alone. cfo is run from checkouts that never held the goblin's
	// branch as often as from the one that did, goblin branch names collide
	// across projects, and -D would force-delete an unrelated branch's unpushed
	// commits. The same test skips a local branch that is ahead of what was
	// pushed. Either way the branch here is not the merged one, so there is
	// nothing to clean up and nothing to report.
	localRef, err := commands.Run(ctx, execx.Request{Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch}})
	if err != nil || localRef.ExitCode != 0 || strings.TrimSpace(string(localRef.Stdout)) != head.HeadRefOid {
		return
	}
	// -D rather than -d: git's "is it merged" test asks whether the commits
	// are reachable from the current branch, which is false after a squash or
	// rebase merge even though GitHub merged the PR. GitHub has already
	// answered the question -d is trying to ask.
	local, err := commands.Run(ctx, execx.Request{Name: "git", Args: []string{"branch", "-D", branch}})
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "cfo pr merge: the local branch %s was left in place: %v\n", branch, err)
	case local.ExitCode != 0:
		fmt.Fprintf(stderr, "cfo pr merge: the local branch %s was left in place: %s\n", branch, strings.TrimSpace(string(local.Stderr)))
	default:
		fmt.Fprintf(stdout, "deleted local branch %s\n", branch)
	}
}

// refAlreadyGone reports whether a ref deletion failed because the ref was not
// there: GitHub answers a DELETE of a missing ref with HTTP 422 "Reference does
// not exist".
func refAlreadyGone(stderr []byte) bool {
	return strings.Contains(strings.ToLower(string(stderr)), "reference does not exist")
}

// runMergeLocal fast-forwards a project's main branch to a goblin's landed worktree
// head. It only ever fast-forwards: a divergent or dirty main refuses loudly.
func runMergeLocal(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo merge-local: task ID is required")
		return 2
	}
	id := args[0]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if meta.Worktree == "" || meta.Project == "" {
		fmt.Fprintln(stderr, "cfo merge-local: task metadata has no worktree or project")
		return 1
	}
	runner := execx.OSRunner{}
	head, err := gitOutput(runner, meta.Worktree, "rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Refuse a dirty primary checkout before touching it.
	if dirty, _ := gitOutput(runner, meta.Project, "status", "--porcelain"); dirty != "" {
		fmt.Fprintln(stderr, "cfo merge-local: primary checkout is dirty; refusing to merge")
		return 1
	}
	branch, err := gitOutput(runner, meta.Project, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if _, err := gitOutput(runner, meta.Project, "fetch", "--quiet", "origin"); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// git merge --ff-only <sha> refuses (non-zero) on any divergence.
	res, err := runner.Run(context.Background(), execx.Request{
		Dir:  meta.Project,
		Name: "git",
		Args: []string{"merge", "--ff-only", strings.TrimSpace(head)},
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if res.ExitCode != 0 {
		fmt.Fprintf(stderr, "cfo merge-local: fast-forward refused: %s", strings.TrimSpace(string(res.Stderr)))
		return 1
	}
	fmt.Fprintf(stdout, "merged %s into %s (%s)\n", strings.TrimSpace(head)[:12], branch, id)
	return 0
}

func prHead(url string) string {
	res, err := execx.OSRunner{}.Run(context.Background(), execx.Request{
		Name: "gh",
		Args: []string{"pr", "view", url, "--json", "headRefOid", "--jq", ".headRefOid"},
	})
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(string(res.Stdout))
}

func gitOutput(runner execx.Runner, dir string, args ...string) (string, error) {
	res, err := runner.Run(context.Background(), execx.Request{Dir: dir, Name: "git", Args: args})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}
