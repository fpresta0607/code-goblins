package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/codexguard"
)

const codexGuardUsage = "usage: cfo codex-guard --state <state folder> --task <id> --until <RFC3339 time> -- [codex's arguments]"

// runCodexGuard is the codex a goblin's armed codex guard runs in its place
// (codexguard): it says in one line why codex started nothing, wakes the CFO
// the first time the task meets the guard, and exits 1. It starts nothing
// itself, whatever its arguments.
func runCodexGuard(args []string, stderr io.Writer, now time.Time) int {
	flags := flag.NewFlagSet("cfo codex-guard", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stateDir := flags.String("state", "", "the state folder of the home that armed the guard")
	task := flags.String("task", "", "the task whose guard it is")
	until := flags.String("until", "", "when Codex's used-up allowance renews")
	if err := flags.Parse(args); err != nil {
		fmt.Fprintln(stderr, codexGuardUsage)
		return 2
	}
	renews, err := time.Parse(time.RFC3339, *until)
	if *stateDir == "" || err != nil {
		fmt.Fprintln(stderr, codexGuardUsage)
		return 2
	}
	line, err := codexguard.Refuse(*stateDir, *task, renews, flags.Args(), now)
	if line != "" {
		fmt.Fprintln(stderr, line)
	}
	if err != nil {
		fmt.Fprintf(stderr, "cfo codex-guard: %v\n", err)
	}
	return 1
}
