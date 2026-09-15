package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// runNotify is the goblin-to-CFO direct ping: it writes a task's outcome
// straight into the wake queue with the actual payload (PR URL, question, or
// failure reason), so the CFO is woken with the real thing instead of the
// watcher guessing from pane text. Identical for claude, codex, and pi.
//
//	cfo notify <task-id> --done --pr <url>
//	cfo notify <task-id> --blocked "<question>"
//	cfo notify <task-id> --failed "<reason>"
func runNotify(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo notify: task ID is required")
		return 2
	}
	id := args[0]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	done := fs.Bool("done", false, "report completion")
	pr := fs.String("pr", "", "PR URL, required with --done")
	blocked := fs.String("blocked", "", "report a question the goblin is blocked on")
	failed := fs.String("failed", "", "report a failure reason")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo notify: unexpected arguments")
		return 2
	}

	var verb, detail string
	switch {
	case *done && *blocked == "" && *failed == "":
		if *pr == "" {
			fmt.Fprintln(stderr, "cfo notify: --done requires --pr <url>")
			return 2
		}
		verb, detail = "done", "PR "+*pr
	case !*done && *blocked != "" && *failed == "":
		verb, detail = "blocked", *blocked
	case !*done && *blocked == "" && *failed != "":
		verb, detail = "failed", *failed
	default:
		fmt.Fprintln(stderr, "cfo notify: exactly one of --done, --blocked, or --failed is required")
		return 2
	}

	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *done {
		recap := taskcontext.PathsFor(h, id).Recap
		if info, e := os.Stat(recap); e != nil || info.IsDir() || info.Size() == 0 {
			fmt.Fprintf(stderr, "cfo notify: save the finished branch recap first: cfo recap %s --file <html>; include tests, limitations and accurate PR/merge/deployment status\n", id)
			return 1
		}
	}

	line := verb + ": " + state.NormalizeStatusDetail(detail)
	if _, err := wake.Notify(h.State, id, line); err != nil {
		fmt.Fprintln(stderr, "cfo notify: wake the CFO: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "notified %s %s\n", id, line)
	return 0
}
