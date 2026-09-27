package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/testguard"
)

// runGate runs a check a no-mistakes gate calls from its run worktree.
// tests-kept exits 1 when the gate's own fix commits deleted or skipped a
// test, which parks the run with an ask-user finding instead of letting the
// deletion through unseen. test is the repository's local test step.
func runGate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "tests-kept" && args[0] != "test") {
		fmt.Fprintln(stderr, "cfo gate: the checks are tests-kept and test")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if args[0] == "test" {
		return runGateTest(dir, stdout, stderr)
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

// runGateTest is this repository's local gate test step: go vet and go test
// on the packages the branch changed and the packages that import them
// directly, each named with why, while CI runs every package. It runs them
// without the fleet's CFO_HOME and CFO_STATE_OVERRIDE, which a gate step
// inherits from the goblin's pane.
func runGateTest(dir string, stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	plan, err := gatetest.Read(ctx, execx.OSRunner{}, dir)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var packages []string
	switch {
	case plan.Everything:
		fmt.Fprintf(stdout, "cfo gate test: go.mod or go.sum changed since %.8s, so every package is tested\n", plan.Base)
		packages = []string{"./..."}
	case len(plan.Choices) == 0:
		fmt.Fprintf(stdout, "cfo gate test: no Go package changed since %.8s, so there is nothing to test here; CI runs every package\n", plan.Base)
		return 0
	default:
		fmt.Fprintf(stdout, "cfo gate test: %d package(s) changed since %.8s or import one directly; CI runs every package\n", len(plan.Choices), plan.Base)
		for _, choice := range plan.Choices {
			fmt.Fprintln(stdout, "- "+choice.String())
			packages = append(packages, choice.ImportPath)
		}
	}
	env := gatetest.Environment(os.Environ())
	for _, args := range [][]string{
		append([]string{"vet"}, packages...),
		append([]string{"test", "-count=1", "-p", "2", "-timeout", "45m"}, packages...),
	} {
		command := exec.Command("go", args...)
		command.Dir, command.Env, command.Stdout, command.Stderr = dir, env, stdout, stderr
		if err := command.Run(); err != nil {
			fmt.Fprintf(stderr, "cfo gate test: go %s: %v\n", args[0], err)
			return 1
		}
	}
	return 0
}
