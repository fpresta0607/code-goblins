package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

## Delivery

kind: %s
mode: %s
`, id, *project, *kind, *mode)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

// runPR handles "cfo pr check <id> <url>" and "cfo pr merge <url>".
func runPR(sub string, args []string, stdout, stderr io.Writer) int {
	switch sub {
	case "check":
		return runPRCheck(args, stdout, stderr)
	case "merge":
		return runPRMerge(args, stdout, stderr)
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
func runPRMerge(args []string, stdout, stderr io.Writer) int {
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
	cmdArgs := []string{"pr", "merge", url, "--" + *method}
	if *deleteBranch {
		cmdArgs = append(cmdArgs, "--delete-branch")
	}
	res, err := execx.OSRunner{}.Run(context.Background(), execx.Request{Name: "gh", Args: cmdArgs})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if res.ExitCode != 0 {
		fmt.Fprintf(stderr, "cfo pr merge: gh exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		return 1
	}
	fmt.Fprintln(stdout, strings.TrimSpace(string(res.Stdout)))
	return 0
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
