package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	projectcfg "github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/testguard"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// runGate runs a check a no-mistakes gate calls from its run worktree.
// tests-kept exits 1 when the gate's own fix commits deleted or skipped a
// test, which parks the run with an ask-user finding instead of letting the
// deletion through unseen, and leaves out the gate commits a person already
// let through at an earlier park. test is the repository's local test step,
// prepush what a goblin runs before it pushes, turn runs any command in the
// line those two take their turns in, and turns shows which runs hold the
// machine's turns and which wait.
func runGate(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) > 0 && args[0] == "turn" {
		return runGateTurn(args[1:], stdout, stderr, runtime)
	}
	if len(args) > 0 && args[0] == "turns" {
		switch {
		case len(args) == 1:
			return runGateTurns(stdout, stderr, runtime.availableMemory)
		case len(args) == 2 && args[1] == "--json":
			return runGateTurnsJSON(stdout, stderr, runtime.availableMemory)
		}
		fmt.Fprintln(stderr, "usage: goblins gate turns [--json]")
		return 2
	}
	takesFlags := len(args) > 0 && (args[0] == "test" || args[0] == "lint" || args[0] == "prepush")
	if len(args) == 0 || !slices.Contains([]string{"tests-kept", "prepare", "test", "lint", "prepush", "turns"}, args[0]) || (!takesFlags && len(args) != 1) {
		fmt.Fprintln(stderr, "goblins gate: the checks are tests-kept, prepare, test [--level changed|fast|affected|full | --focused] [--plan], lint [--plan], prepush [--plan] [--limit <duration>], turn [--as <name>] [--light] -- <program> [<argument>...] and turns [--json]")
		return 2
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if args[0] == "prepare" {
		return runGatePrepare(dir, stdout, stderr, runtime)
	}
	if args[0] == "test" || args[0] == "lint" {
		return runGateChecks(args[0], args[1:], dir, stdout, stderr, runtime)
	}
	if args[0] == "prepush" {
		return runGatePrepush(args[1:], dir, stdout, stderr, runtime)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	approved, approvalsErr := approvedHeads(ctx)
	result, err := testguard.Check(ctx, execx.OSRunner{}, dir, approved)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintln(stdout, testguard.CheckedLine(result.Head))
	switch {
	case approvalsErr != nil:
		fmt.Fprintf(stdout, "tests kept: approvals given at earlier parks were not read (%v), so every gate commit since the base was read\n", approvalsErr)
	case result.Approved > 0:
		fmt.Fprintf(stdout, "tests kept: left out %d gate commit(s) a person already let through at an earlier tests-kept park\n", result.Approved)
	}
	if len(result.Removals) == 0 {
		fmt.Fprintf(stdout, "tests kept: %d gate commit(s) since %.8s checked, and none deleted or skipped a test\n", result.Commits, result.Base)
		return 0
	}
	fmt.Fprintf(stdout, "A gate fix commit deleted or skipped %d test(s), which a gate may do only as an ask-user decision: approve to accept it, and later runs leave these commits out, or fix to restore them.\n", len(result.Removals))
	for _, removal := range result.Removals {
		fmt.Fprintln(stdout, "- "+removal.String())
	}
	return 1
}

func runGatePrepare(dir string, stdout, stderr io.Writer, runtime commandRuntime) int {
	ctx, cancel := context.WithTimeout(context.Background(), runtime.gatePlanLimit)
	defer cancel()
	plan, err := gatetest.Read(ctx, execx.OSRunner{}, dir, gatetest.Fast)
	if err != nil {
		fmt.Fprintf(stderr, "goblins gate prepare: %v\n", err)
		return 2
	}
	if !slices.ContainsFunc(plan.Changed, func(file string) bool { return strings.HasPrefix(file, "frontend/") }) {
		fmt.Fprintln(stdout, "goblins gate prepare: no frontend change; no dependencies installed")
		return 0
	}
	// The locked install is as heavy as the tests after it, so it waits for
	// its turn and the memory floor as they do (#2367).
	const label = "goblins gate prepare"
	task, _ := gateTask(plan.Root, runtime)
	who := fmt.Sprintf("%s at %.8s, preparation, in %s", path.Base(plan.Module), plan.Commit, plan.Root)
	if task != "" {
		who += ", task " + taskLabel(task, runtime)
	}
	turn, err := takeGateTurn(context.Background(), label, stdout, stderr, runtime.availableMemory, who, taskPriority(label, task, stderr, runtime), runtime.gateBudget(plan.Level), runtime.gateWaitLimit, gateLineLimit(runtime, os.Getenv, false), 0)
	if err != nil {
		return 1
	}
	defer turn.Release()
	start := time.Now()
	fmt.Fprintln(stdout, "goblins gate prepare: npm ci in this worktree's frontend")
	command := []string{"npm", "ci"}
	if goruntime.GOOS == "windows" {
		command = []string{"cmd.exe", "/d", "/c", "npm", "ci"}
	}
	exit, err := runtime.gateRun(command, filepath.Join(plan.Root, "frontend"), gatetest.Environment(os.Environ()), stdout, stderr)
	fmt.Fprintf(stdout, "goblins gate prepare: locked install finished in %s\n", time.Since(start).Round(time.Millisecond))
	if err != nil || exit != 0 {
		fmt.Fprintf(stderr, "goblins gate prepare: npm ci: exit %d: %v\n", exit, err)
		return 1
	}
	return 0
}

// approvedHeads are the heads of earlier tests-kept parks a person approved
// or skipped, read from the gate's database, where each park's summary keeps
// the check's first line. When the database cannot be read the check leaves
// nothing out, as it did before it read approvals.
func approvedHeads(ctx context.Context) ([]string, error) {
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return nil, err
	}
	summaries, err := pipeline.Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}.ApprovedGateSummaries(ctx)
	if err != nil {
		return nil, err
	}
	return testguard.ApprovedHeads(summaries), nil
}

// runGateChecks runs this repository's test or vet-only lint step. It plans what the
// branch's change requires, prints the plan with why each package is in it,
// runs go vet and, for the test step, go test without the fleet's GOBLINS_HOME
// and GOBLINS_STATE_OVERRIDE, which a gate step inherits from the goblin's pane,
// and leaves a report only for the test step. With no --level or --focused,
// test runs fast, or changed when its environment says the project's hosted
// CI is its test evidence (testEvidenceVariable), as goblins gate turn tells a
// gate's Test step of such a project; lint vets at the required level. CI
// checks the level the
// change requires: the changed packages, the packages that
// import them directly or transitively and the packages a contract of the
// policy names for a changed file, or every package once a module file
// changed or a changed file is one the policy does not account for. Running
// that level here as well ran the slowest packages twice, here and in CI. The
// plan also lists the changed files the policy puts outside the Go checks, so
// what the step does not check is said. --level runs another level and still
// says what is required, and --plan prints the plan and runs nothing. Every check waits for the run's turn on the machine, and checks
// that ran past their level's budget do not pass. The tests' output is what
// go test prints without -v, the log holds every line they wrote, the report
// what became of each package, and a run that holds a turn says beside it how
// far its tests are.
func runGateChecks(kind string, args []string, dir string, stdout, stderr io.Writer, runtime commandRuntime) int {
	label := "goblins gate " + kind
	isLint := kind == "lint"
	flags := flag.NewFlagSet(label, flag.ContinueOnError)
	flags.SetOutput(stderr)
	levelName := flags.String("level", "", "changed, fast, affected or full; fast when not given, since CI runs every package, and changed for a project whose hosted CI is its test evidence")
	planOnly := flags.Bool("plan", false, "print the plan and run nothing")
	isFocused := flags.Bool("focused", false, "read repair tests and paths from NO_MISTAKES_TEST_FOCUS")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *isFocused && *levelName != "" || isLint && (*isFocused || *levelName != "") {
		fmt.Fprintln(stderr, "usage: goblins gate test [--level changed|fast|affected|full | --focused] [--plan]; goblins gate lint [--plan]")
		return 2
	}
	asked, defaulted := gatetest.Fast, true
	isHostedCI := os.Getenv(testEvidenceVariable) == projectcfg.HostedCIEvidence
	if isHostedCI {
		asked = gatetest.Changed
	}
	if *levelName != "" {
		level, err := gatetest.ParseLevel(*levelName)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		asked, defaulted = level, false
	}
	if isLint {
		asked, defaulted = "", false
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), runtime.gatePlanLimit)
	var plan gatetest.Plan
	var note string
	var err error
	if *isFocused {
		plan, note, err = gatetest.ReadFocused(ctx, execx.OSRunner{}, dir, os.Getenv("NO_MISTAKES_TEST_FOCUS"))
		defaulted = false
	} else {
		plan, err = gatetest.Read(ctx, execx.OSRunner{}, dir, asked)
	}
	isStalled := err != nil && ctx.Err() != nil
	cancel()
	if isStalled {
		return planStalled(label, runtime.gatePlanLimit, err, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if note != "" {
		fmt.Fprintln(stdout, note)
	}
	if isLint {
		plan.Tests, plan.Alone, plan.Guards = nil, nil, nil
	}
	var commands [][]string
	for _, guard := range plan.Guards {
		commands = append(commands, guard.Command)
	}
	if len(plan.Vet) > 0 {
		commands = append(commands, append([]string{"go", "vet"}, plan.Vet...))
	}
	if len(plan.Tests) > 0 {
		// -json has go test say what became of each package and each test, which
		// the run prints as go test prints without it and records by package.
		commands = append(commands, append(slices.Clone(gatetest.StepTestArgs), plan.Tests...))
	}
	if len(plan.Alone) > 0 {
		// The packages the policy runs alone take a turn of their own, one at
		// a time, so nothing competes with the real Chrome their tests drive.
		commands = append(commands, append([]string{"go", "test", "-json", "-count=1", "-p", "1", "-timeout", "45m"}, plan.Alone...))
	}
	for _, named := range plan.Named {
		commands = append(commands, named.Command)
	}
	printPlan := func() {
		if isLint {
			fmt.Fprintf(stdout, "%s: vet at level %s: %s; no tests run\n", label, plan.Level, plan.Why)
			for _, choice := range plan.Choices {
				fmt.Fprintln(stdout, "- "+choice.String())
			}
			return
		}
		if *isFocused && note == "" {
			fmt.Fprintln(stdout, label+": focused repair check, not a full baseline")
			for _, named := range plan.Named {
				fmt.Fprintln(stdout, "- "+named.What)
			}
		}
		if isHostedCI && defaulted {
			fmt.Fprintln(stdout, label+": the project's hosted CI is its test evidence, so this step runs the quick checks and the tests the change added or edited, and CI runs every test before the merge")
		}
		printGatePlan(stdout, plan, defaulted)
	}
	if *planOnly {
		printPlan()
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
	if *isFocused && note == "" {
		report.Level = "focused"
	}
	if isLint {
		report.Level = "lint"
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
	reportPath := ""
	if !isLint {
		log, filename, err := verify.Begin(report.Project, start, plan.Commit, report.Level)
		if err != nil {
			fmt.Fprintf(stderr, "%s: this run leaves no report: %v\n", label, err)
		} else {
			defer log.Close()
			reportPath, report.Log = filename, log.Name()
			full = log
			stdout, stderr = io.MultiWriter(stdout, log), io.MultiWriter(stderr, log)
		}
	}
	printPlan()
	if taskErr != nil {
		fmt.Fprintf(stderr, "%s: task attribution unavailable: %v\n", label, taskErr)
	}

	// A run under the disk floor is refused before it takes a turn: its
	// tests write build output and test homes, and a full disk fails them
	// for no reason of their own.
	if short, err := gateDiskShortfall(runtime, plan.Root); err != nil || short != "" {
		if err != nil {
			short = "free disk cannot be read, so the run cannot be shown to leave the disk floor free: " + err.Error()
		}
		fmt.Fprintf(stderr, "%s: %s; a run needs the floor free\n", label, short)
		report.Status, report.QueueNote = "failed", short
	}

	who := fmt.Sprintf("%s at %.8s, %s level, in %s", report.Project, plan.Commit, plan.Level, plan.Root)
	if report.Task != "" {
		who += ", task " + taskLabel(report.Task, runtime)
	}
	priority := taskPriority(label, report.Task, stderr, runtime)
	env := gatetest.Environment(os.Environ())
	remainingFailures := slices.Clone(plan.Failures)
	lineLimit := gateLineLimit(runtime, os.Getenv, false)
	for _, command := range commands {
		check := verify.Result{Command: command, Scope: report.Level, Status: "not_run", ExitCode: -1}
		if report.Status == "passed" {
			budget := runtime.gateBudget(plan.Level)
			lineWaited := time.Duration(report.QueueSeconds * float64(time.Second))
			turn, admissionErr := takeGateTurn(context.Background(), label, stdout, stderr, runtime.availableMemory, who, priority, budget, runtime.gateWaitLimit, lineLimit, lineWaited)
			report.QueueSeconds += turn.Waited.Seconds()
			if admissionErr != nil {
				report.Status, report.QueueNote = "failed", admissionErr.Error()
				report.Checks = append(report.Checks, check)
				continue
			}
			// The check's source is read with its turn held, before it runs
			// and once it has ended, so the report says what it tested. The
			// reads count in no check's time against its budget.
			check.Source.ReadStart(verify.ReadSource(context.Background(), execx.OSRunner{}, dir))
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
			ran := time.Since(check.Start)
			check.Source.ReadEnd(verify.ReadSource(context.Background(), execx.OSRunner{}, dir))
			_, sourceErr := check.Source.Tested()
			err = errors.Join(err, sourceErr)
			if events != nil {
				if outputErr := events.End(); outputErr != nil && !errors.Is(err, outputErr) {
					err = errors.Join(err, outputErr)
				}
				remainingFailures = slices.DeleteFunc(remainingFailures, func(failure gatetest.FailedTest) bool {
					return events.Tested(failure.Package, failure.Name)
				})
				// What this run timed is what goblins gate prepush goes by when
				// it leaves a slow package's slowest tests to CI.
				if keepErr := verify.KeepTimes(report.Project, events.Times()); keepErr != nil {
					fmt.Fprintf(stderr, "%s: this run's test times are not kept: %v\n", label, keepErr)
				}
			}
			check.ExitCode = exit
			if err != nil || exit != 0 {
				check.Status, report.Status = "failed", "failed"
				fmt.Fprintf(stderr, "%s: go %s: exit %d: %v\n", label, command[1], exit, err)
			}
			check.DurationSeconds = ran.Seconds()
			if events != nil {
				check.Packages = recordPackages(stdout, events.Results())
			}
			turn.Release()
			if check.Status == "passed" && ran > budget {
				check.Status, report.Status = "over_budget", "failed"
				fmt.Fprintf(stderr, "%s: go %s passed but ran for %s, past the %s budget of the %s level, so the run does not pass\n", label, command[1], ran.Round(time.Second), budget, plan.Level)
			}
		}
		report.Checks = append(report.Checks, check)
	}
	for _, failure := range remainingFailures {
		report.Status = "failed"
		fmt.Fprintf(stderr, "%s: named failure %s %s was missing, skipped or unfinished\n", label, failure.Package, failure.Name)
	}
	report.DurationSeconds = time.Since(start).Seconds()

	verdict := fmt.Sprintf("%s: %s at level %s in %s", label, report.Status, report.Level, time.Since(start).Round(time.Second))
	if waited := time.Duration(report.QueueSeconds * float64(time.Second)).Round(time.Second); waited > 0 {
		verdict += fmt.Sprintf(", %s of it waiting for its turn", waited)
	}
	if reportPath != "" {
		if err := verify.Finish(reportPath, report); err != nil {
			fmt.Fprintf(stderr, "%s: this run leaves no report: %v\n", label, err)
		} else {
			verdict += "; report " + reportPath
		}
	}
	fmt.Fprintln(stdout, verdict)
	if !isLint && report.Status == "passed" && !plan.Level.Covers(plan.Required) {
		fmt.Fprintf(stdout, "goblins gate test: the change requires the %s level before it merges, which CI's run of every package checks\n", plan.Required)
	}
	if report.Status != "passed" {
		return 1
	}
	return 0
}

// planLimit bounds a plan's read of the checkout, nearly all of it go list
// opening every Go file one after another. That read waits on the disk rather
// than works: from 2026-10-21 to 2026-10-28, for hours after a reboot, reads
// that reached the disk took up to 26 seconds on the dev drive and 51 on the
// drive that holds it, where they take 200 microseconds, and the 19 plans that
// two minutes ended on 2026-10-27 and 28 had used at most 6 seconds of CPU. The
// slowest plan seen to finish took 255 seconds.
const planLimit = 15 * time.Minute

// planStalled says a plan ran out its limit, with the error of what it was
// reading, and returns the exit code of a run that gave no result. Nothing ran,
// so nothing about the change failed, and a caller asks no one to repair it.
// The line is the last on standard error, by which a gate tells this run, and
// the last on standard output, which is what goblins verify shows of a check.
func planStalled(label string, limit time.Duration, err error, stdout, stderr io.Writer) int {
	said := fmt.Sprintf("%s: nothing was tested, since the plan stalled: it read the checkout for %s, as a disk that is slow to answer makes it do, and did not finish: %s", label, limit, oneLine(err))
	fmt.Fprintln(stdout, said)
	fmt.Fprintln(stderr, said)
	return exitNoTurn
}

// runGateCommand runs one of the step's commands as a process in dir with
// env and returns its exit code, -1 when it did not start, with an error
// unless it passed. The command runs as a tree (execx.StartTree), so a step
// that is ended, as its gate's time limit or an interrupt ends it, ends with
// everything its command started, a test's helper whose parent already
// exited included.
func runGateCommand(command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
	process := execx.Command(command[0], command[1:]...)
	process.Dir, process.Env, process.Stdout, process.Stderr = dir, env, stdout, stderr
	tree, err := execx.StartTree(process)
	if err != nil {
		return -1, err
	}
	err = process.Wait()
	if releaseErr := tree.Release(); releaseErr != nil {
		err = errors.Join(err, releaseErr)
	}
	if process.ProcessState == nil {
		return -1, err
	}
	return process.ProcessState.ExitCode(), err
}

// testEvidenceVariable is the variable goblins gate turn gives a gate's Test step
// of a project whose record names its test evidence, as hosted-ci, which has
// goblins gate test run the changed level when it is given no other.
const testEvidenceVariable = "GOBLINS_TEST_EVIDENCE"

// gateAgentLineLimit is the longest a run that a gate's agent started waits
// in line. A gate ends an agent whose invocation ran past its budget, and
// counts the agent's wait for a turn in it: on 2026-11-02 two review fixers
// each spent their whole hour in line behind six heavy steps, on a machine
// short of memory, and were ended with their repairs written and not
// committed (issue 1932). Three minutes outlasts a short check ahead and a
// dip of memory under the floor, and is a tenth of the 30 minutes a gate
// gives an agent unless its machine says otherwise.
const gateAgentLineLimit = 3 * time.Minute

// gateLineLimit is how long this run may wait in line: runtime's limit when a
// gate's agent started it, which the gate's variable in its environment says,
// and no limit otherwise. A gate's own Test step has none whatever its
// environment says, since the gate waits on it under no agent's budget.
func gateLineLimit(runtime commandRuntime, getenv func(string) string, isTestStep bool) time.Duration {
	if isTestStep || getenv(gateAgentVariable) == "" {
		return 0
	}
	return runtime.agentLineLimit
}

// agentLeftLine is what a run a gate's agent started says once it has waited
// limit with no turn: why it left the line, and what its agent does instead.
// The gate's Test step checks the agent's work in a turn of its own, so the
// agent's own heavy run is one it can go without.
func agentLeftLine(limit time.Duration) string {
	return fmt.Sprintf("no turn came within %s, which is as long as a run a gate's agent starts waits in line, since its gate counts the wait against the agent's invocation budget. Go on without this run and start nothing heavy outside the line: commit the work, and the gate runs the project's checks in a turn of its own", limit)
}

// takeGateTurn waits in the machine's common line, for goblins gate test and for
// goblins gate turn alike, each under its own label. Both physical and commit
// availability must reach the floor before a run starts. It writes to says
// what the run waits for as it starts to wait, whenever its place in line
// changes and once a minute:
// which run holds the turn, for how long and under what budget, and where
// this run stands in line. An unavailable or expired floor reading, a store
// failure, or a wait that ctx ended, prevents execution, and is written to
// stderr, as is a wait that lineLimit ended, which is zero for any run but one
// a gate's agent started (gateLineLimit) and bounds that run's wait across
// every turn it takes: lineWaited is what its earlier turns took of the
// limit, and a run with none of it left takes a turn that is free and waits
// for none. Budget expiry never displaces custody.
func takeGateTurn(ctx context.Context, label string, says, stderr io.Writer, available func() (supervisor.Memory, error), who, priority string, budget, limit, lineLimit, lineWaited time.Duration) (verify.Turn, error) {
	store, err := verify.AdmissionDir()
	if err != nil {
		fmt.Fprintf(stderr, "%s: this run takes no turn: %v\n", label, err)
		return verify.Turn{}, err
	}
	var pairedAvailable func() (uint64, error)
	if available != nil {
		pairedAvailable = func() (uint64, error) {
			memory, err := available()
			return min(memory.Available, memory.CommitAvailable), err
		}
	}
	lineLeft := lineLimit
	if lineLimit > 0 {
		lineLeft = max(lineLimit-lineWaited, time.Nanosecond)
	}
	turn, err := verify.Admission{
		Dir:       store,
		Floor:     supervisor.MemoryFloor,
		Available: pairedAvailable,
		Who:       who,
		Priority:  priority,
		Budget:    budget,
		Limit:     limit,
		LineLimit: lineLeft,
		Poll:      time.Second,
		Waiting: func(waited time.Duration, why string) {
			fmt.Fprintf(says, "%s: waiting for its turn (%s so far): %s\n", label, waited.Round(time.Second), why)
		},
	}.Wait(ctx)
	switch {
	case errors.Is(err, context.Canceled):
		err = errors.New("it was interrupted while it waited")
	case errors.Is(err, verify.ErrLineLimit):
		err = errors.New(agentLeftLine(lineLimit))
	}
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "%s: this run takes no turn: %v\n", label, err)
	case turn.Waited >= time.Second:
		fmt.Fprintf(says, "%s: took its turn after %s\n", label, turn.Waited.Round(time.Second))
	}
	return turn, err
}

// measuredExecutor runs commands as measured executions in the machine's one
// line for heavy runs, the line takeGateTurn waits in, and says on says why a
// command waits for its turn, as takeGateTurn does. Its caller retries its
// pending cleanup before it exits.
func measuredExecutor(label, who string, says io.Writer, available func() (supervisor.Memory, error)) (*verify.Executor, error) {
	if available == nil {
		return nil, errors.New("the machine's memory cannot be read")
	}
	store, err := verify.AdmissionDir()
	if err != nil {
		return nil, err
	}
	return &verify.Executor{
		Admission: verify.Admission{
			Dir:   store,
			Floor: supervisor.MemoryFloor,
			Who:   who,
			Limit: verify.NoLimit,
			Poll:  time.Second,
			Waiting: func(waited time.Duration, why string) {
				fmt.Fprintf(says, "%s: waiting for its turn (%s so far): %s\n", label, waited.Round(time.Second), why)
			},
		},
		Took: func(waited time.Duration) {
			if waited >= time.Second {
				fmt.Fprintf(says, "%s: took its turn after %s\n", label, waited.Round(time.Second))
			}
		},
		Monitor: verify.Monitor{Headroom: func() (verify.Headroom, error) {
			memory, err := available()
			return verify.Headroom{PhysicalAvailable: memory.Available, CommitAvailable: memory.CommitAvailable}, err
		}},
	}, nil
}

// gateDiskShortfall reads the free disk of the drive the run's checkout is on
// against the disk floor, the one the fleet's home sets when this runs for a
// home and the default otherwise, and says why the run does not fit, or ""
// when it does.
func gateDiskShortfall(runtime commandRuntime, dir string) (string, error) {
	if runtime.gateDisk != nil {
		d, err := runtime.gateDisk(dir)
		return d.Shortfall(), err
	}
	settings := fleetconfig.Defaults()
	if h, err := home.Resolve(); err == nil && home.IsPrimary(h) {
		if settings, err = fleetconfig.Read(h.Root); err != nil {
			return "", err
		}
	}
	reading, err := disk.Read(dir)
	if err != nil {
		return "", err
	}
	return supervisor.Disk{Reading: reading, Floor: fleetconfig.Bytes(settings.DiskFloorGB)}.Shortfall(), nil
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

// runGateTurns shows the line that goblins gate test, goblins gate prepush and goblins
// gate turn runs take their turns in:
// each run that holds a turn, with how long it has and its budget, the runs
// that wait, in the order they take their turns, each with why it stands
// behind the one before it, and the machine's memory when it is under the
// floor a run waits for. The light runs beside the slots follow the slots'
// holders, each on a line of its own that says so, with its bound and its
// memory. A gate shows a step's output only once the step has ended, so this
// is where a run's wait can be read while it lasts.
func runGateTurns(stdout, stderr io.Writer, available func() (supervisor.Memory, error)) int {
	report, err := readTurns(available)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	now := time.Now()
	switch {
	case len(report.Holding) == 0 && len(report.Waiting) == 0 && len(report.Light) == 0:
		fmt.Fprintln(stdout, "no run holds the turn and none waits")
	case len(report.Holding) == 0 && report.StuckLock == nil:
		fmt.Fprintln(stdout, "turn: free")
	case len(report.Holding) == 0:
		fmt.Fprintln(stdout, "turn: free, but no run can take it")
	}
	if report.StuckLock != nil {
		fmt.Fprintf(stdout, "lock: the line's lock (custody-mutation in the line's store) has been held by pid %d for %s, and while it is held no run joins the line, moves in it or takes a turn\n", report.StuckLock.PID, now.Sub(report.StuckLock.Since).Round(time.Second))
	}
	for _, run := range report.Holding {
		line := fmt.Sprintf("turn: %s (pid %d), for %s", run.Who, run.PID, now.Sub(run.Since).Round(time.Second))
		if run.BudgetSeconds > 0 {
			line += fmt.Sprintf(" of its %s budget", time.Duration(run.BudgetSeconds*float64(time.Second)))
		}
		if report.IsWeighed && run.EstimateBytes > 0 {
			line += fmt.Sprintf(", using %.1f GB of the %.1f GB it is expected to use", verify.Gigabytes(run.UsedBytes), verify.Gigabytes(run.EstimateBytes))
		}
		fmt.Fprintln(stdout, line)
		if run.Now != "" {
			fmt.Fprintln(stdout, "  now: "+run.Now)
		}
	}
	for _, run := range report.Light {
		fmt.Fprintf(stdout, "light, beside the slots: %s (pid %d), for %s of its %s bound, using %.1f GB of the %.1f GB it is expected to use\n", run.Who, run.PID, now.Sub(run.Since).Round(time.Second), time.Duration(run.BudgetSeconds*float64(time.Second)), verify.Gigabytes(run.UsedBytes), verify.Gigabytes(run.EstimateBytes))
		if run.Now != "" {
			fmt.Fprintln(stdout, "  now: "+run.Now)
		}
	}
	if len(report.Waiting) > 0 {
		fmt.Fprintln(stdout, "waiting:")
	}
	for place, run := range report.Waiting {
		line := fmt.Sprintf("%d. %s (pid %d), for %s", place+1, run.Who, run.PID, now.Sub(run.Since).Round(time.Second))
		if report.IsWeighed && run.EstimateBytes > 0 {
			line += fmt.Sprintf(", expected to use %.1f GB", verify.Gigabytes(run.EstimateBytes))
		}
		if run.IsLowPriority {
			line += ", at the lowest priority"
		}
		if run.Behind != "" {
			line += fmt.Sprintf(", behind run %d: %s", place, run.Behind)
		}
		fmt.Fprintln(stdout, line)
	}
	if report.Memory.IsShort {
		fmt.Fprintf(stdout, "memory: %.1f GB physical and %.1f GB commit are available; both require the %.1f GB floor\n", verify.Gigabytes(report.Memory.AvailableBytes), verify.Gigabytes(report.Memory.CommitAvailableBytes), verify.Gigabytes(report.Memory.FloorBytes))
	}
	if report.Hold != "" {
		fmt.Fprintln(stdout, "goblins: the supervisor starts and resumes none by itself, keeping the test slots open: "+report.Hold)
	}
	return 0
}

// runGateTurnsJSON runs goblins gate turns --json: the line runGateTurns shows,
// as turnsReport, the shape the board reads.
func runGateTurnsJSON(stdout, stderr io.Writer, available func() (supervisor.Memory, error)) int {
	report, err := readTurns(available)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	data, err := json.Marshal(report)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprintln(stdout, string(data))
	return 0
}

// turnsReport is the machine's line as goblins gate turns --json gives it, a
// shape whose keys stay as they are, for the board: each run that holds a
// turn, each that waits in the order it takes its turn, each light run that
// holds a turn beside the slots, apart from both, and the machine's memory
// against the floor the line's head waits for. Every key is always there, and
// an empty line is empty lists.
type turnsReport struct {
	// Capacity is how many runs may hold a turn at once, and IsWeighed whether
	// the line weighs each run's expected memory, which a line of two does.
	Capacity  int         `json:"capacity"`
	IsWeighed bool        `json:"is_weighed"`
	Holding   []turnsRun  `json:"holding"`
	Waiting   []turnsRun  `json:"waiting"`
	Light     []turnsRun  `json:"light"`
	Memory    turnsMemory `json:"memory"`
	// Hold says why the supervisor starts and resumes no goblin by itself,
	// a new session leaving the line's next run short of its floor, and is
	// empty when it may.
	Hold string `json:"hold"`
	// StuckLock is the line's lock while a live owner has held it
	// verify.StuckLockAfter or longer, which keeps every run out of the line
	// and out of a free turn, and null otherwise.
	StuckLock *turnsLock `json:"stuck_lock"`
}

// turnsLock is the owner of the line's lock and when it took it.
type turnsLock struct {
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
}

// turnsRun is one run in the line: who it is, since when it holds its turn
// or waits, the budget and what it is doing that a holder said, the memory
// it is expected to use and, for a holder, uses now, and whether it waits
// at the lowest priority. Behind is for goblins gate turns alone, so the board's
// keys stay as they are.
type turnsRun struct {
	Who           string    `json:"who"`
	PID           int       `json:"pid"`
	Since         time.Time `json:"since"`
	BudgetSeconds float64   `json:"budget_seconds"`
	Now           string    `json:"now"`
	EstimateBytes uint64    `json:"estimate_bytes"`
	UsedBytes     uint64    `json:"used_bytes"`
	IsLowPriority bool      `json:"is_low_priority"`
	Behind        string    `json:"-"`
}

// turnsMemory is the machine's physical and commit memory available now,
// the floor the line's head waits for, and whether either is under it.
type turnsMemory struct {
	AvailableBytes       uint64 `json:"available_bytes"`
	CommitAvailableBytes uint64 `json:"commit_available_bytes"`
	FloorBytes           uint64 `json:"floor_bytes"`
	IsShort              bool   `json:"is_short"`
}

// readTurns reads the machine's line and its memory with available.
func readTurns(available func() (supervisor.Memory, error)) (turnsReport, error) {
	store, err := verify.AdmissionDir()
	if err != nil {
		return turnsReport{}, err
	}
	capacity, _, err := verify.AdmissionLimits(store)
	if err != nil {
		return turnsReport{}, err
	}
	holding, waiting, err := verify.Line(store)
	if err != nil {
		return turnsReport{}, err
	}
	light := slices.DeleteFunc(slices.Clone(holding), func(run verify.Standing) bool { return !run.IsLight })
	holding = slices.DeleteFunc(holding, func(run verify.Standing) bool { return run.IsLight })
	var headEstimate uint64
	if len(waiting) > 0 {
		headEstimate = waiting[0].Estimate
	}
	floor, err := verify.HeadFloor(store, headEstimate, 0)
	if err != nil {
		return turnsReport{}, err
	}
	if available == nil {
		return turnsReport{}, errors.New("available memory cannot be read")
	}
	memory, err := available()
	if err != nil {
		return turnsReport{}, fmt.Errorf("available memory cannot be read: %w", err)
	}
	stuck, err := verify.StuckLock(store, time.Now())
	if err != nil {
		return turnsReport{}, fmt.Errorf("the line's lock cannot be read: %w", err)
	}
	report := turnsReport{
		Hold:      supervisor.SlotHold(floor, memory),
		Capacity:  capacity,
		IsWeighed: capacity == 2,
		Holding:   turnsRuns(holding),
		Waiting:   turnsRuns(waiting),
		Light:     turnsRuns(light),
		Memory: turnsMemory{
			AvailableBytes:       memory.Available,
			CommitAvailableBytes: memory.CommitAvailable,
			FloorBytes:           floor,
			IsShort:              min(memory.Available, memory.CommitAvailable) < floor,
		},
	}
	if stuck != nil {
		report.StuckLock = &turnsLock{PID: stuck.PID, Since: stuck.Acquired}
	}
	return report, nil
}

func turnsRuns(standing []verify.Standing) []turnsRun {
	runs := []turnsRun{}
	for _, run := range standing {
		runs = append(runs, turnsRun{Who: run.Who, PID: run.PID, Since: run.Since, BudgetSeconds: run.Budget.Seconds(), Now: run.Now, EstimateBytes: run.Estimate, UsedBytes: run.Used, IsLowPriority: run.IsLowPriority, Behind: run.Behind})
	}
	return runs
}

// unknownShown is how many of the files the policy does not account for a
// plan names before it counts the rest; the report holds them all.
const unknownShown = 10

// printGatePlan prints the level a plan runs and why, each package the
// change reaches with why, at the changed level the tests it runs by name,
// each test run the level leaves to a broader one, or to CI at the changed
// level, the changed files the policy puts outside the Go checks, under its reason
// for each, and the changed files it does not account for. defaulted says the
// level is the one a run without --level takes.
func printGatePlan(w io.Writer, plan gatetest.Plan, defaulted bool) {
	switch {
	case plan.Level == plan.Required:
		fmt.Fprintf(w, "goblins gate test: level %s: %s\n", plan.Level, plan.Why)
	case defaulted:
		fmt.Fprintf(w, "goblins gate test: level %s, the default, since CI runs every package; the change requires %s: %s\n", plan.Level, plan.Required, plan.Why)
	default:
		fmt.Fprintf(w, "goblins gate test: level %s, asked for; the change requires %s: %s\n", plan.Level, plan.Required, plan.Why)
	}
	switch {
	case len(plan.Vet) == 0 && len(plan.Guards) > 0:
		fmt.Fprintf(w, "goblins gate test: no Go package changed since %.8s; required repository guards still run\n", plan.Base)
	case len(plan.Vet) == 0:
		fmt.Fprintf(w, "goblins gate test: no Go package changed since %.8s, so there is nothing to test here; CI runs every package\n", plan.Base)
	case plan.Everything:
		fmt.Fprintf(w, "goblins gate test: go.mod or go.sum changed since %.8s, which reaches every package\n", plan.Base)
	case plan.Level == gatetest.Full:
		fmt.Fprintf(w, "goblins gate test: every package is tested; the change since %.8s reaches %d package(s)\n", plan.Base, len(plan.Choices))
	default:
		fmt.Fprintf(w, "goblins gate test: the change since %.8s reaches %d package(s); CI runs every package\n", plan.Base, len(plan.Choices))
	}
	for _, choice := range plan.Choices {
		fmt.Fprintln(w, "- "+choice.String())
	}
	for _, guard := range plan.Guards {
		fmt.Fprintln(w, "- "+guard.String())
	}
	if plan.Level == gatetest.Changed {
		for _, named := range plan.Named {
			fmt.Fprintln(w, "- "+named.String())
		}
	}
	switch {
	case len(plan.Left) > 0 && plan.Level == gatetest.Changed:
		fmt.Fprintln(w, "left to CI:")
	case len(plan.Left) > 0:
		fmt.Fprintf(w, "left to the %s level:\n", plan.Required)
	}
	for _, left := range plan.Left {
		fmt.Fprintln(w, "- "+left.String())
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
	return getenv("GOBLINS_TASK_ID")
}

// sayProgress has a run say beside its turn, every interval until the
// returned function is called, how far its tests are. A gate shows a step's
// output only once the step has ended, so the turn's card is where a run's
// progress is read while it lasts, with goblins gate turns.
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
		fmt.Fprintf(stdout, "goblins gate test: go test did not pass in %d of %d package(s):\n", len(failures), len(results))
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
