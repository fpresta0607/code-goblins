package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/testguard"
)

// runGate runs a check a no-mistakes repository gate calls from its run
// worktree. tests-kept exits 1 when the gate's own fix commits deleted or
// skipped a test, which parks the run with an ask-user finding instead of
// letting the deletion through unseen.
func runGate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "tests-kept" {
		fmt.Fprintln(stderr, "cfo gate: tests-kept is the one check")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := testguard.Check(ctx, execx.OSRunner{}, dir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(result.Removals) == 0 {
		fmt.Fprintf(stdout, "tests kept: %d gate commit(s) since %.8s checked, and none deleted or skipped a test\n", result.Commits, result.Base)
		return 0
	}
	fmt.Fprintf(stdout, "A gate fix commit deleted or skipped %d test(s), which a gate may do only as an ask-user decision: approve to accept it, or fix to restore them.\n", len(result.Removals))
	for _, removal := range result.Removals {
		fmt.Fprintln(stdout, "- "+removal.String())
	}
	return 1
}
