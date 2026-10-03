package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
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
// change requires, the changed packages and the packages that import them
// directly, or every package once a module file changed, while CI runs every
// package; --level runs another level and still says what is required, and
// --plan prints the plan and runs nothing. Above the fast level the tests wait
// for the run's turn on the machine, and tests that ran past their level's
// budget do not pass.
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
	readHosted := func() {
		if runtime.gateHosted == nil || plan.Level == gatetest.Fast || len(plan.Tests) == 0 {
			return
		}
		if reportPath == "" {
			report.ReuseDeclined = append(report.ReuseDeclined, "hosted evidence: no report store for the reuse receipt")
			return
		}
		receipt, err := runtime.gateHosted(context.Background(), plan, runtime.gateBudget(plan.Level))
		if err != nil {
			report.ReuseDeclined = append(report.ReuseDeclined, err.Error())
			return
		}
		if receipt == nil {
			report.ReuseDeclined = append(report.ReuseDeclined, "no qualifying hosted evidence")
			return
		}
		report.Reused = receipt
		pending := report
		pending.Status = "running"
		if err := verify.Save(reportPath, pending); err != nil {
			report.Reused = nil
			report.ReuseDeclined = append(report.ReuseDeclined, "hosted evidence: the reuse receipt could not be saved")
			return
		}
		fmt.Fprintf(stdout, "cfo gate test: reusing hosted Go evidence from %s, run %d, tested main %.8s and head %.8s\n", receipt.URL, receipt.RunID, receipt.Main, receipt.Head)
	}
	readHosted()

	who := fmt.Sprintf("%s at %.8s, %s level, in %s", report.Project, plan.Commit, plan.Level, plan.Root)
	if report.Task != "" {
		who += ", task " + report.Task
	}
	env := gatetest.Environment(os.Environ())
	for _, command := range commands {
		check := verify.Result{Command: command, Scope: report.Level, Status: "not_run", ExitCode: -1}
		if report.Reused != nil {
			check.Status, check.ExitCode = "reused", 0
			report.Checks = append(report.Checks, check)
			continue
		}
		if report.Status == "passed" {
			// The tests take a turn, vet does not, and at the fast level not
			// even they do: it is seconds of work.
			takesTurn := command[1] == "test" && plan.Level != gatetest.Fast
			var turn verify.Turn
			var budget time.Duration
			if takesTurn {
				budget = runtime.gateBudget(plan.Level)
				turn = takeGateTurn(stdout, stderr, runtime.availableMemory, who, budget)
				report.QueueSeconds, report.QueueNote = turn.Waited.Seconds(), turn.Note
				readHosted()
				if report.Reused != nil {
					turn.Release()
					check.Status, check.ExitCode, check.Start = "reused", 0, time.Time{}
					report.Checks = append(report.Checks, check)
					continue
				}
			}
			check.Start = time.Now()
			check.Status = "passed"
			exit, err := runtime.gateRun(command, dir, env, stdout, stderr)
			check.ExitCode = exit
			if err != nil {
				check.Status, report.Status = "failed", "failed"
				fmt.Fprintf(stderr, "cfo gate test: go %s: %v\n", command[1], err)
			}
			ran := time.Since(check.Start)
			check.DurationSeconds = ran.Seconds()
			turn.Release()
			if takesTurn && check.Status == "passed" && ran > budget {
				check.Status, report.Status = "over_budget", "failed"
				fmt.Fprintf(stderr, "cfo gate test: go test passed but ran for %s, past the %s budget of the %s level, so the run does not pass\n", ran.Round(time.Second), budget, plan.Level)
			}
		}
		report.Checks = append(report.Checks, check)
	}
	report.DurationSeconds = time.Since(start).Seconds()

	savedReport := ""
	if reportPath != "" {
		if err := verify.Finish(reportPath, report); err != nil {
			fmt.Fprintf(stderr, "cfo gate test: this run leaves no report: %v\n", err)
			if report.Reused != nil {
				report.Status = "failed"
				fmt.Fprintln(stderr, "cfo gate test: reused checks cannot pass without their saved receipt")
			}
		} else {
			savedReport = "; report " + reportPath
		}
	}
	verdict := fmt.Sprintf("cfo gate test: %s at level %s in %s", report.Status, plan.Level, time.Since(start).Round(time.Second))
	if waited := time.Duration(report.QueueSeconds * float64(time.Second)).Round(time.Second); waited > 0 {
		verdict += fmt.Sprintf(", %s of it waiting for its turn", waited)
	}
	verdict += savedReport
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

// takeGateTurn waits for the run's turn among the machine's test runs, every
// goblin's and every gate's: one at a time, or as many as CFO_VERIFY_SLOTS
// says, in the order they asked, and none while the memory a new process can
// have is under the fleet's floor. It prints what the run waits for as it
// starts to wait, whenever its place in line changes and once a minute:
// which run holds the turn, for how long and under what budget, and where
// this run stands in line. who
// names this run to the runs behind it, and budget is how long its turn may
// last: a holder past its budget loses the turn to the next run, and a run
// that has waited an hour for memory goes on under the floor, which the turn
// then notes. A run that cannot take turns at all says so and runs its tests,
// and the turn it returns still says how long it waited before that: neither
// the store nor the wait decides the verdict.
func takeGateTurn(stdout, stderr io.Writer, available func() (uint64, error), who string, budget time.Duration) verify.Turn {
	store, err := verify.StoreDir()
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate test: this run takes no turn: %v\n", err)
		return verify.Turn{}
	}
	slots, problem := gateSlots(os.Getenv("CFO_VERIFY_SLOTS"))
	if problem != "" {
		fmt.Fprintln(stderr, "cfo gate test: "+problem)
	}
	turn, err := verify.Admission{
		Dir:       filepath.Join(store, "slots"),
		Slots:     slots,
		Floor:     supervisor.MemoryFloor,
		Available: available,
		Who:       who,
		Budget:    budget,
		Limit:     time.Hour,
		Poll:      time.Second,
		Waiting: func(waited time.Duration, why string) {
			fmt.Fprintf(stdout, "cfo gate test: waiting for its turn (%s so far): %s\n", waited.Round(time.Second), why)
		},
	}.Wait(context.Background())
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "cfo gate test: this run takes no turn: %v\n", err)
	case turn.Note != "":
		fmt.Fprintf(stdout, "cfo gate test: took its turn after %s: %s\n", turn.Waited.Round(time.Second), turn.Note)
	case turn.Waited >= time.Second:
		fmt.Fprintf(stdout, "cfo gate test: took its turn after %s\n", turn.Waited.Round(time.Second))
	}
	return turn
}

// gateSlots is how many test runs hold a turn at once: one, or the number
// CFO_VERIFY_SLOTS sets for the machine. A setting that is not a number above
// 0 is read as one, and problem says so.
func gateSlots(setting string) (slots int, problem string) {
	if setting == "" {
		return 1, ""
	}
	if asked, err := strconv.Atoi(setting); err == nil && asked > 0 {
		return asked, ""
	}
	return 1, fmt.Sprintf("CFO_VERIFY_SLOTS is %q, not a number above 0, so one run tests at a time", setting)
}

// gateBudget is how long the tests of a level may run: twice go test's 45
// minute package timeout at the affected level, and twice that at the full
// level, which runs every package. Tests that ran past it do not pass, and
// the run waiting behind them takes the turn.
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
func runGateTurns(stdout, stderr io.Writer, available func() (uint64, error)) int {
	store, err := verify.StoreDir()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	holding, waiting, err := verify.Line(filepath.Join(store, "slots"))
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
	}
	if len(waiting) > 0 {
		fmt.Fprintln(stdout, "waiting:")
	}
	for place, run := range waiting {
		fmt.Fprintf(stdout, "%d. %s (pid %d), for %s\n", place+1, run.Who, run.PID, now.Sub(run.Since).Round(time.Second))
	}
	if free, err := available(); err == nil && free < supervisor.MemoryFloor {
		fmt.Fprintf(stdout, "memory: %.1f GB is available, under the %.1f GB floor a run waits for\n", verify.Gigabytes(free), verify.Gigabytes(supervisor.MemoryFloor))
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
