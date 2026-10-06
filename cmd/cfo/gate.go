package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/testguard"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// runGate runs a check a no-mistakes gate calls from its run worktree.
// tests-kept exits 1 when the gate's own fix commits deleted or skipped a
// test, which parks the run with an ask-user finding instead of letting the
// deletion through unseen. test is the repository's local test step, and
// turns shows which test runs hold the machine's turns and which wait.
func runGate(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || !slices.Contains([]string{"tests-kept", "test", "turns"}, args[0]) || (args[0] != "test" && len(args) != 1) {
		fmt.Fprintln(stderr, "cfo gate: the checks are tests-kept, test [--level fast|affected|full] [--plan] and turns")
		return 2
	}
	if args[0] == "turns" {
		return runGateTurns(stdout, stderr, runtime.availableMemory)
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if args[0] == "test" {
		return runGateTest(args[1:], dir, stdout, stderr, runtime)
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
// change requires: the changed packages, the packages that import them
// directly or transitively and the packages a contract of the policy names
// for a changed file, or every package once a module file changed or a
// changed file is one the policy does not account for, while CI runs every
// package. The plan also lists the changed files the policy puts outside the
// Go checks, so what the step does not check is said. --level runs another
// level and still says what is required, and --plan prints the plan and runs
// nothing. Every check waits for the run's turn on the machine, and checks
// that ran past their level's budget do not pass. The tests' output is what
// go test prints without -v, the log holds every line they wrote, the report
// what became of each package, and a run that holds a turn says beside it how
// far its tests are.
func runGateTest(args []string, dir string, stdout, stderr io.Writer, runtime commandRuntime) int {
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
		// -json has go test say what became of each package and each test, which
		// the run prints as go test prints without it and records by package.
		commands = append(commands, append([]string{"go", "test", "-json", "-count=1", "-p", "2", "-timeout", "45m"}, plan.Tests...))
	}
	if *planOnly {
		printGatePlan(stdout, plan)
		fmt.Fprintln(stdout, "policy: "+plan.Policy)
		for _, command := range commands {
			fmt.Fprintln(stdout, "would run: "+strings.Join(command, " "))
		}
		return 0
	}

	task, taskErr := gateTask(plan.Root, runtime)
	report := verify.Report{
		Version:       verify.ReportVersion,
		Project:       path.Base(plan.Module),
		Task:          task,
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
		report.Selected = append(report.Selected, verify.Selection{Package: choice.ImportPath, Why: choice.Reason()})
	}
	for _, left := range plan.Left {
		report.Left = append(report.Left, verify.Left{Check: left.Check, Why: left.Why})
	}
	for _, set := range plan.Outside {
		report.Outside = append(report.Outside, verify.Outside{Why: set.Why, Files: set.Files})
	}
	report.Unknown = plan.Unknown
	// short is where the step's own output goes, and full takes every line
	// the tests wrote: the log, when the run has one.
	short, full := stdout, io.Discard
	log, reportPath, err := verify.Begin(report.Project, start, plan.Commit, report.Level)
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate test: this run leaves no report: %v\n", err)
	} else {
		defer log.Close()
		report.Log = log.Name()
		full = log
		stdout, stderr = io.MultiWriter(stdout, log), io.MultiWriter(stderr, log)
	}
	printGatePlan(stdout, plan)
	if taskErr != nil {
		fmt.Fprintf(stderr, "cfo gate test: task attribution unavailable: %v\n", taskErr)
	}

	who := fmt.Sprintf("%s at %.8s, %s level, in %s", report.Project, plan.Commit, plan.Level, plan.Root)
	if report.Task != "" {
		who += ", task " + report.Task
	}
	env := gatetest.Environment(os.Environ())
	for _, command := range commands {
		check := verify.Result{Command: command, Scope: report.Level, Status: "not_run", ExitCode: -1}
		if report.Status == "passed" {
			budget := runtime.gateBudget(plan.Level)
			turn, admissionErr := takeGateTurn(stdout, stderr, runtime.availableMemory, who, budget, runtime.gateWaitLimit)
			report.QueueSeconds += turn.Waited.Seconds()
			if admissionErr != nil {
				report.Status, report.QueueNote = "failed", admissionErr.Error()
				report.Checks = append(report.Checks, check)
				continue
			}
			check.Start, check.Status = time.Now(), "passed"
			output := stdout
			var events *gatetest.Events
			if command[1] == "test" {
				events = gatetest.NewEvents(short, full)
				output = events
			}
			stopSaying := func() {}
			if events != nil {
				stopSaying = sayProgress(turn, events, runtime.gateProgress)
			}
			exit, err := runtime.gateRun(command, dir, env, output, stderr)
			stopSaying()
			if events != nil {
				if outputErr := events.End(); outputErr != nil && !errors.Is(err, outputErr) {
					err = errors.Join(err, outputErr)
				}
			}
			check.ExitCode = exit
			if err != nil || exit != 0 {
				check.Status, report.Status = "failed", "failed"
				fmt.Fprintf(stderr, "cfo gate test: go %s: exit %d: %v\n", command[1], exit, err)
			}
			ran := time.Since(check.Start)
			check.DurationSeconds = ran.Seconds()
			if events != nil {
				check.Packages = recordPackages(stdout, events.Results())
			}
			turn.Release()
			if check.Status == "passed" && ran > budget {
				check.Status, report.Status = "over_budget", "failed"
				fmt.Fprintf(stderr, "cfo gate test: go %s passed but ran for %s, past the %s budget of the %s level, so the run does not pass\n", command[1], ran.Round(time.Second), budget, plan.Level)
			}
		}
		report.Checks = append(report.Checks, check)
	}
	report.DurationSeconds = time.Since(start).Seconds()

	verdict := fmt.Sprintf("cfo gate test: %s at level %s in %s", report.Status, plan.Level, time.Since(start).Round(time.Second))
	if waited := time.Duration(report.QueueSeconds * float64(time.Second)).Round(time.Second); waited > 0 {
		verdict += fmt.Sprintf(", %s of it waiting for its turn", waited)
	}
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

// runGateCommand runs one of the step's commands as a process in dir with
// env and returns its exit code, -1 when it did not start, with an error
// unless it passed.
func runGateCommand(command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
	process := execx.Command(command[0], command[1:]...)
	process.Dir, process.Env, process.Stdout, process.Stderr = dir, env, stdout, stderr
	err := process.Run()
	if process.ProcessState == nil {
		return -1, err
	}
	return process.ProcessState.ExitCode(), err
}

// takeGateTurn waits in the machine's common line. Both physical and commit
// availability must reach the floor before a check starts. It prints what
// the run waits for as it
// starts to wait, whenever its place in line changes and once a minute:
// which run holds the turn, for how long and under what budget, and where
// this run stands in line. An unavailable or expired floor reading, or a
// store failure, prevents execution. Budget expiry never displaces custody.
func takeGateTurn(stdout, stderr io.Writer, available func() (supervisor.Memory, error), who string, budget, limit time.Duration) (verify.Turn, error) {
	store, err := verify.AdmissionDir()
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate test: this run takes no turn: %v\n", err)
		return verify.Turn{}, err
	}
	var pairedAvailable func() (uint64, error)
	if available != nil {
		pairedAvailable = func() (uint64, error) {
			memory, err := available()
			return min(memory.Available, memory.CommitAvailable), err
		}
	}
	turn, err := verify.Admission{
		Dir:       store,
		Floor:     supervisor.MemoryFloor,
		Available: pairedAvailable,
		Who:       who,
		Budget:    budget,
		Limit:     limit,
		Poll:      time.Second,
		Waiting: func(waited time.Duration, why string) {
			fmt.Fprintf(stdout, "cfo gate test: waiting for its turn (%s so far): %s\n", waited.Round(time.Second), why)
		},
	}.Wait(context.Background())
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "cfo gate test: this run takes no turn: %v\n", err)
	case turn.Waited >= time.Second:
		fmt.Fprintf(stdout, "cfo gate test: took its turn after %s\n", turn.Waited.Round(time.Second))
	}
	return turn, err
}

// gateBudget is how long the tests of a level may run: twice go test's 45
// minute package timeout at the affected level, and twice that at the full
// level, which runs every package. Checks that ran past it do not pass, and
// keep custody until their execution ends.
func gateBudget(level gatetest.Level) time.Duration {
	if level == gatetest.Full {
		return 3 * time.Hour
	}
	return 90 * time.Minute
}

// runGateTurns shows the line that cfo gate test runs take their turns in:
// each run that holds a turn, with how long it has and its budget, the runs
// that wait, in the order they asked, and the machine's memory when it is
// under the floor a run waits for. A gate shows a step's output only once
// the step has ended, so this is where a run's wait can be read while it
// lasts.
func runGateTurns(stdout, stderr io.Writer, available func() (supervisor.Memory, error)) int {
	store, err := verify.AdmissionDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	_, floor, err := verify.AdmissionLimits(store)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	holding, waiting, err := verify.Line(store)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	now := time.Now()
	switch {
	case len(holding) == 0 && len(waiting) == 0:
		fmt.Fprintln(stdout, "no run holds the turn and none waits")
	case len(holding) == 0:
		fmt.Fprintln(stdout, "turn: free")
	}
	for _, run := range holding {
		line := fmt.Sprintf("turn: %s (pid %d), for %s", run.Who, run.PID, now.Sub(run.Since).Round(time.Second))
		if run.Budget > 0 {
			line += fmt.Sprintf(" of its %s budget", run.Budget)
		}
		fmt.Fprintln(stdout, line)
		if run.Now != "" {
			fmt.Fprintln(stdout, "  now: "+run.Now)
		}
	}
	if len(waiting) > 0 {
		fmt.Fprintln(stdout, "waiting:")
	}
	for place, run := range waiting {
		fmt.Fprintf(stdout, "%d. %s (pid %d), for %s\n", place+1, run.Who, run.PID, now.Sub(run.Since).Round(time.Second))
	}
	if available == nil {
		fmt.Fprintln(stderr, "available memory cannot be read")
		return 2
	}
	memory, err := available()
	if err != nil {
		fmt.Fprintf(stderr, "available memory cannot be read: %v\n", err)
		return 2
	}
	if min(memory.Available, memory.CommitAvailable) < floor {
		fmt.Fprintf(stdout, "memory: %.1f GB physical and %.1f GB commit are available; both require the %.1f GB floor\n", verify.Gigabytes(memory.Available), verify.Gigabytes(memory.CommitAvailable), verify.Gigabytes(floor))
	}
	return 0
}

// unknownShown is how many of the files the policy does not account for a
// plan names before it counts the rest; the report holds them all.
const unknownShown = 10

// printGatePlan prints the level a plan runs and why, each package the
// change reaches with why, each test run the level leaves to a broader one,
// the changed files the policy puts outside the Go checks, under its reason
// for each, and the changed files it does not account for.
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
		fmt.Fprintf(w, "cfo gate test: every package is tested; the change since %.8s reaches %d package(s)\n", plan.Base, len(plan.Choices))
	default:
		fmt.Fprintf(w, "cfo gate test: the change since %.8s reaches %d package(s); CI runs every package\n", plan.Base, len(plan.Choices))
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
	if len(plan.Outside) > 0 {
		fmt.Fprintln(w, "changed files outside the Go checks:")
		for _, set := range plan.Outside {
			fmt.Fprintln(w, "- "+set.String())
		}
	}
	if len(plan.Unknown) > 0 {
		fmt.Fprintln(w, "changed files the policy does not account for:")
		for _, file := range plan.Unknown[:min(len(plan.Unknown), unknownShown)] {
			fmt.Fprintln(w, "- "+file)
		}
		if more := len(plan.Unknown) - unknownShown; more > 0 {
			fmt.Fprintf(w, "- and %d more\n", more)
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

// sayProgress has a run say beside its turn, every interval until the
// returned function is called, how far its tests are. A gate shows a step's
// output only once the step has ended, so the turn's card is where a run's
// progress is read while it lasts, with cfo gate turns.
func sayProgress(turn verify.Turn, events *gatetest.Events, every time.Duration) (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				turn.Say(progressLine(events, now))
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// progressShown is how many running tests a progress line names before it
// counts the rest.
const progressShown = 3

// progressLine is what a run says of its tests beside its turn: how many
// packages are done, which tests are running and for how long, and the last
// line of the step's output.
func progressLine(events *gatetest.Events, now time.Time) string {
	running, done, last := events.Progress()
	line := fmt.Sprintf("go test: %d package(s) done", done)
	var names []string
	for _, run := range running[:min(len(running), progressShown)] {
		name := run.ImportPath
		if run.Test != "" {
			name += " " + run.Test
		}
		names = append(names, fmt.Sprintf("%s for %s", name, now.Sub(run.Since).Round(time.Second)))
	}
	if len(names) > 0 {
		line += "; running " + strings.Join(names, ", ")
	}
	if more := len(running) - progressShown; more > 0 {
		line += fmt.Sprintf(" and %d more", more)
	}
	if last != "" {
		line += "; last output: " + last
	}
	return line
}

// recordPackages turns what became of each package's tests into the report's
// form, and when any did not pass says which, and which tests in them, after
// the tests' own output: one place to read what failed.
func recordPackages(stdout io.Writer, results []gatetest.PackageResult) []verify.PackageResult {
	var recorded []verify.PackageResult
	var failures []string
	for _, result := range results {
		recorded = append(recorded, verify.PackageResult{Package: result.ImportPath, Status: result.Status, Seconds: result.Seconds, Tests: result.Tests, Failed: result.Failed, Unfinished: result.Unfinished})
		if result.Status != "passed" && result.Status != "no_tests" {
			failures = append(failures, notPassed(result))
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(stdout, "cfo gate test: go test did not pass in %d of %d package(s):\n", len(failures), len(results))
		for _, failure := range failures {
			fmt.Fprintln(stdout, "- "+failure)
		}
	}
	return recorded
}

// namedTests is how many test names a line gives before it counts the rest.
const namedTests = 5

// notPassed says what became of a package that did not pass: the tests that
// failed, the tests that never finished, as one that hangs, or that it did
// not compile.
func notPassed(result gatetest.PackageResult) string {
	named := func(tests []string) string {
		if more := len(tests) - namedTests; more > 0 {
			return fmt.Sprintf("%s and %d more", strings.Join(tests[:namedTests], ", "), more)
		}
		return strings.Join(tests, ", ")
	}
	var what []string
	if len(result.Failed) > 0 {
		what = append(what, named(result.Failed)+" failed")
	}
	if len(result.Unfinished) > 0 {
		what = append(what, named(result.Unfinished)+" did not finish")
	}
	if len(what) == 0 {
		switch result.Status {
		case "build_failed":
			what = []string{"did not compile"}
		case "unfinished":
			what = []string{"did not finish"}
		default:
			what = []string{"failed outside any test"}
		}
	}
	return result.ImportPath + ": " + strings.Join(what, "; ")
}
