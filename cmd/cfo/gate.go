package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/testguard"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// runGate runs a check a no-mistakes gate calls from its run worktree.
// tests-kept exits 1 when the gate's own fix commits deleted or skipped a
// test, which parks the run with an ask-user finding instead of letting the
// deletion through unseen. test is the repository's local test step.
func runGate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "tests-kept" && args[0] != "test") || (args[0] == "tests-kept" && len(args) != 1) {
		fmt.Fprintln(stderr, "cfo gate: the checks are tests-kept and test [--level fast|affected|full] [--plan]")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if args[0] == "test" {
		return runGateTest(args[1:], dir, stdout, stderr)
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

// runGateTest is this repository's local gate test step. It plans what the
// branch's change requires, prints the plan with why each package is in it,
// runs go vet and go test at the planned level without the fleet's CFO_HOME
// and CFO_STATE_OVERRIDE, which a gate step inherits from the goblin's pane,
// and leaves a report of what it ran. With no --level it runs the level the
// change requires, the changed packages and the packages that import them
// directly, or every package once a module file changed, while CI runs every
// package; --level runs another level and still says what is required, and
// --plan prints the plan and runs nothing.
func runGateTest(args []string, dir string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cfo gate test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	levelName := flags.String("level", "", "fast, affected or full; the level the change requires when not given")
	planOnly := flags.Bool("plan", false, "print the plan and run nothing")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: cfo gate test [--level fast|affected|full] [--plan]")
		return 2
	}
	var asked gatetest.Level
	if *levelName != "" {
		level, err := gatetest.ParseLevel(*levelName)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		asked = level
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	plan, err := gatetest.Read(ctx, execx.OSRunner{}, dir, asked)
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var commands [][]string
	if len(plan.Vet) > 0 {
		commands = append(commands, append([]string{"go", "vet"}, plan.Vet...))
	}
	if len(plan.Tests) > 0 {
		commands = append(commands, append([]string{"go", "test", "-count=1", "-p", "2", "-timeout", "45m"}, plan.Tests...))
	}
	if *planOnly {
		printGatePlan(stdout, plan)
		fmt.Fprintln(stdout, "policy: "+plan.Policy)
		for _, command := range commands {
			fmt.Fprintln(stdout, "would run: "+strings.Join(command, " "))
		}
		return 0
	}

	report := verify.Report{
		Version:       verify.ReportVersion,
		Project:       path.Base(plan.Module),
		Task:          taskID(os.Getenv),
		Root:          plan.Root,
		Commit:        plan.Commit,
		Uncommitted:   plan.Uncommitted,
		Base:          plan.Base,
		Level:         string(plan.Level),
		RequiredLevel: string(plan.Required),
		RequiredWhy:   plan.Why,
		Policy:        plan.Policy,
		Toolchain:     plan.Toolchain,
		Checks:        []verify.Result{},
		Status:        "passed",
		Start:         start,
	}
	for _, choice := range plan.Choices {
		why := "changed"
		if choice.Imports != "" {
			why = "imports " + choice.Imports
		}
		report.Selected = append(report.Selected, verify.Selection{Package: choice.ImportPath, Why: why})
	}
	for _, left := range plan.Left {
		report.Left = append(report.Left, verify.Left{Check: left.Check, Why: left.Why})
	}
	log, reportPath, err := verify.Begin(report.Project, start, plan.Commit, report.Level)
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate test: this run leaves no report: %v\n", err)
	} else {
		defer log.Close()
		report.Log = log.Name()
		stdout, stderr = io.MultiWriter(stdout, log), io.MultiWriter(stderr, log)
	}
	printGatePlan(stdout, plan)

	env := gatetest.Environment(os.Environ())
	for _, command := range commands {
		check := verify.Result{Command: command, Scope: report.Level, Status: "not_run", ExitCode: -1}
		if report.Status == "passed" {
			check.Start, check.Status, check.ExitCode = time.Now(), "passed", 0
			process := execx.Command(command[0], command[1:]...)
			process.Dir, process.Env, process.Stdout, process.Stderr = dir, env, stdout, stderr
			if err := process.Run(); err != nil {
				check.Status, check.ExitCode, report.Status = "failed", -1, "failed"
				if process.ProcessState != nil {
					check.ExitCode = process.ProcessState.ExitCode()
				}
				fmt.Fprintf(stderr, "cfo gate test: go %s: %v\n", command[1], err)
			}
			check.DurationSeconds = time.Since(check.Start).Seconds()
		}
		report.Checks = append(report.Checks, check)
	}
	report.DurationSeconds = time.Since(start).Seconds()

	verdict := fmt.Sprintf("cfo gate test: %s at level %s in %s", report.Status, plan.Level, time.Since(start).Round(time.Second))
	if reportPath != "" {
		if err := verify.Finish(reportPath, report); err != nil {
			fmt.Fprintf(stderr, "cfo gate test: this run leaves no report: %v\n", err)
		} else {
			verdict += "; report " + reportPath
		}
	}
	fmt.Fprintln(stdout, verdict)
	if report.Status == "passed" && !plan.Level.Covers(plan.Required) {
		fmt.Fprintf(stdout, "cfo gate test: the change still requires the %s level before it merges\n", plan.Required)
	}
	if report.Status != "passed" {
		return 1
	}
	return 0
}

// printGatePlan prints the level a plan runs and why, each package the
// change reaches with why, and each test run the level leaves to a broader
// one.
func printGatePlan(w io.Writer, plan gatetest.Plan) {
	if plan.Level == plan.Required {
		fmt.Fprintf(w, "cfo gate test: level %s: %s\n", plan.Level, plan.Why)
	} else {
		fmt.Fprintf(w, "cfo gate test: level %s, asked for; the change requires %s: %s\n", plan.Level, plan.Required, plan.Why)
	}
	switch {
	case len(plan.Vet) == 0:
		fmt.Fprintf(w, "cfo gate test: no Go package changed since %.8s, so there is nothing to test here; CI runs every package\n", plan.Base)
	case plan.Everything:
		fmt.Fprintf(w, "cfo gate test: go.mod or go.sum changed since %.8s, which reaches every package\n", plan.Base)
	case plan.Level == gatetest.Full:
		fmt.Fprintf(w, "cfo gate test: every package is tested; %d changed since %.8s or import one that did\n", len(plan.Choices), plan.Base)
	default:
		fmt.Fprintf(w, "cfo gate test: %d package(s) changed since %.8s or import one directly; CI runs every package\n", len(plan.Choices), plan.Base)
	}
	for _, choice := range plan.Choices {
		fmt.Fprintln(w, "- "+choice.String())
	}
	if len(plan.Left) > 0 {
		fmt.Fprintf(w, "left to the %s level:\n", plan.Required)
		for _, left := range plan.Left {
			fmt.Fprintln(w, "- "+left.String())
		}
	}
}

// taskID is the fleet task a run belongs to, as the goblin's pane names it.
// A gate's steps run in the shared gate daemon's environment, which can be
// another goblin's, so a step under a gate names none.
func taskID(getenv func(string) string) string {
	if getenv("NO_MISTAKES_GATE") != "" {
		return ""
	}
	return getenv("CFO_TASK_ID")
}
