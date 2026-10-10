package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// pushLimit is how long a push's checks run when --limit names no other
// time: about what CI takes to answer a push, so the check costs no more
// than the red run it saves.
const pushLimit = 15 * time.Minute

// runGatePrepush is what a goblin runs before it pushes: it picks what the
// branch's change can break, says each check with why, and runs them one at
// a time until one fails, which ends the run with one plain line. Of the 164
// tests that failed pull requests from 2026-09-05 to 2026-10-10, 52 percent
// were in a test file the pull request had changed and 23 percent in another
// file of a package it had changed, tests their goblin had not run. The run
// takes the machine's turn, as cfo gate test does, starts no check while
// memory is under the floor and none after its time limit, and ends one
// still running at the limit. What it does not run it names as left to CI.
// --plan prints the pick with each command and runs nothing.
func runGatePrepush(args []string, dir string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("cfo gate prepush", flag.ContinueOnError)
	flags.SetOutput(stderr)
	planOnly := flags.Bool("plan", false, "print the pick and run nothing")
	limit := flags.Duration("limit", pushLimit, "how long the checks may run before the rest is left to CI")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *limit <= 0 {
		fmt.Fprintln(stderr, "usage: cfo gate prepush [--plan] [--limit <duration>]")
		return 2
	}
	project := ""
	// Beside a working fleet on 2026-10-10 the pick took 2 to 67 seconds
	// for sixteen pull requests, nearly all of it go list, and over two
	// minutes once, in a checkout just made while a test run held the
	// machine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	pick, err := gatetest.ReadPush(ctx, execx.OSRunner{}, dir, func(module string) gatetest.Times {
		project = path.Base(module)
		times, err := verify.Times(project)
		if err != nil {
			fmt.Fprintf(stderr, "cfo gate prepush: this machine's test times cannot be read, so no slow package counts as timed: %v\n", err)
		}
		return times
	})
	cancel()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	printPush(stdout, pick, *limit, *planOnly)
	if *planOnly {
		return 0
	}
	if len(pick.Steps) == 0 {
		if len(pick.Left) > 0 {
			fmt.Fprintf(stdout, "cfo gate prepush: no check ran here. CI is the check of the %d left to it, named above.\n", len(pick.Left))
			return 0
		}
		fmt.Fprintln(stdout, "cfo gate prepush: passed. This change picks no check to run here.")
		return 0
	}
	if short, err := gateDiskShortfall(runtime, pick.Root); err != nil || short != "" {
		if err != nil {
			short = "free disk cannot be read: " + err.Error()
		}
		fmt.Fprintf(stdout, "cfo gate prepush: nothing ran, since %s. CI is the only check of this push.\n", short)
		return 1
	}
	who := fmt.Sprintf("%s at %.8s, before a push, in %s", project, pick.Commit, pick.Root)
	if task := taskID(os.Getenv); task != "" {
		who += ", task " + task
	}
	turn, err := takePushTurn(stdout, runtime, who, *limit)
	if err != nil {
		fmt.Fprintf(stdout, "cfo gate prepush: nothing ran, since the machine gave this run no turn: %v. CI is the only check of this push.\n", err)
		return 1
	}
	defer turn.Release()

	began := time.Now()
	env := gatetest.Environment(os.Environ())
	ran, cut := 0, ""
	for index, step := range pick.Steps {
		used := time.Since(began)
		switch {
		case used >= *limit:
			cut = fmt.Sprintf("the time limit of %s passed", *limit)
		case index > 0:
			cut = waitForPushMemory(stdout, runtime, index+1, began.Add(*limit), *limit)
		}
		if cut != "" {
			break
		}
		fmt.Fprintf(stdout, "cfo gate prepush: check %d of %d: %s\n", index+1, len(pick.Steps), step.What)
		stepCtx, cancel := context.WithTimeout(context.Background(), *limit-used)
		failure, isCutShort := runPushStep(stepCtx, step, pick.Root, project, env, turn, stdout, stderr, runtime)
		cancel()
		if failure != "" {
			fmt.Fprintf(stdout, "cfo gate prepush: failed at check %d of %d, %s: %s. CI would fail on it too, so fix it before you push.\n", index+1, len(pick.Steps), step.What, failure)
			return 1
		}
		if isCutShort {
			cut = fmt.Sprintf("the time limit of %s passed while check %d ran", *limit, index+1)
			break
		}
		ran++
	}
	if cut != "" {
		fmt.Fprintf(stdout, "left to CI, since %s:\n", cut)
		for _, step := range pick.Steps[ran:] {
			fmt.Fprintln(stdout, "- "+step.String())
		}
	}
	took := time.Since(began).Round(time.Second)
	if left := len(pick.Left) + len(pick.Steps) - ran; left > 0 {
		fmt.Fprintf(stdout, "cfo gate prepush: passed what it ran. %d of %d checks ran in %s, and CI is the check of the %d left to it, named above.\n", ran, len(pick.Steps), took, left)
		return 0
	}
	fmt.Fprintf(stdout, "cfo gate prepush: passed. All %d checks ran in %s.\n", ran, took)
	return 0
}

// printPush prints a pick: how much changed, each check with why, what the
// pick leaves to CI with why, and the policy it follows. With commands it
// prints under each check the command it would run.
func printPush(w io.Writer, pick gatetest.Push, limit time.Duration, commands bool) {
	count := func(number int, one, many string) string {
		if number == 1 {
			return fmt.Sprintf("1 %s", one)
		}
		return fmt.Sprintf("%d %s", number, many)
	}
	fmt.Fprintf(w, "cfo gate prepush: %s changed since %.8s, which picks %s to run within %s\n", count(pick.Changed, "file", "files"), pick.Base, count(len(pick.Steps), "check", "checks"), limit)
	for index, step := range pick.Steps {
		fmt.Fprintf(w, "%d. %s\n", index+1, step)
		for _, detail := range step.Detail {
			fmt.Fprintln(w, "   - "+detail)
		}
		if commands {
			where := ""
			if step.Dir != "" {
				where = " in " + step.Dir
			}
			fmt.Fprintf(w, "   would run%s: %s\n", where, strings.Join(step.Command, " "))
		}
	}
	if len(pick.Left) > 0 {
		fmt.Fprintln(w, "left to CI:")
		for _, left := range pick.Left {
			fmt.Fprintln(w, "- "+left.String())
		}
	}
	fmt.Fprintln(w, "policy: "+pick.Policy)
}

// runPushStep runs one check of a pick from the repository's root and says
// why it failed, or "" when it did not. isCutShort says its time ended it,
// which is no failure. A test that fails runs once more by itself, and
// fails the check only when it fails again: on 2026-10-10 a test of cmd/cfo
// that waits 15 seconds for a real terminal failed here while another run
// held the processors, which the change had not caused and CI would not
// see.
func runPushStep(ctx context.Context, step gatetest.Step, root, project string, env []string, turn verify.Turn, stdout, stderr io.Writer, runtime commandRuntime) (failure string, isCutShort bool) {
	dir := filepath.Join(root, filepath.FromSlash(step.Dir))
	var exit int
	var err error
	if step.Command[0] != "go" || step.Command[1] != "test" {
		exit, err = runtime.pushRun(ctx, step.Command, dir, env, stdout, stderr)
	} else {
		var failed []string
		var isBuilt bool
		failed, isBuilt, exit, err = runPushTests(ctx, step.Command, dir, project, env, turn, stdout, stderr, runtime)
		if len(failed) > 0 && ctx.Err() == nil {
			it, itself := "it runs", "itself"
			if len(failed) > 1 {
				it, itself = "they run", "themselves"
			}
			fmt.Fprintf(stdout, "cfo gate prepush: %s failed, so %s again by %s\n", namedPushTests(failed), it, itself)
			again, _, againExit, againErr := runPushTests(ctx, step.Again(failed), dir, project, env, turn, stdout, stderr, runtime)
			switch {
			case len(again) > 0:
				failed = again
			case againExit == 0 && againErr == nil:
				fmt.Fprintf(stdout, "cfo gate prepush: %s passed by %s, so this machine was busy and the change did not break %s\n", namedPushTests(failed), itself, map[bool]string{false: "it", true: "them"}[len(failed) > 1])
				failed, exit, err = nil, 0, nil
			}
		}
		switch {
		case len(failed) > 0:
			return namedPushTests(failed) + " failed", false
		case !isBuilt:
			return "it does not build", false
		}
	}
	switch {
	case err != nil && ctx.Err() != nil:
		return "", true
	case err != nil:
		return "it did not run: " + err.Error(), false
	case exit != 0:
		return fmt.Sprintf("it exited %d, and what it said is above", exit), false
	}
	return "", false
}

// runPushTests runs one go test of a pick and returns the top-level tests
// that failed and whether every package built. Its output is what go test
// prints without -v, its tests' times are kept for the next pick, and while
// it runs it says beside the run's turn how far it is.
func runPushTests(ctx context.Context, command []string, dir, project string, env []string, turn verify.Turn, stdout, stderr io.Writer, runtime commandRuntime) (failed []string, isBuilt bool, exit int, err error) {
	events := gatetest.NewEvents(stdout, io.Discard)
	stopSaying := sayProgress(turn, events, runtime.gateProgress)
	exit, err = runtime.pushRun(ctx, command, dir, env, events, stderr)
	stopSaying()
	if outputErr := events.End(); outputErr != nil && err == nil {
		err = outputErr
	}
	if keepErr := verify.KeepTimes(project, events.Times()); keepErr != nil {
		fmt.Fprintf(stderr, "cfo gate prepush: this run's test times are not kept: %v\n", keepErr)
	}
	isBuilt = true
	for _, result := range events.Results() {
		isBuilt = isBuilt && result.Status != "build_failed"
		for _, test := range result.Failed {
			if !strings.Contains(test, "/") {
				failed = append(failed, test)
			}
		}
	}
	return failed, isBuilt, exit, err
}

// namedPushTests names tests in one line: the first few, and how many more.
func namedPushTests(tests []string) string {
	if more := len(tests) - namedTests; more > 0 {
		return fmt.Sprintf("%s and %d more", strings.Join(tests[:namedTests], ", "), more)
	}
	return strings.Join(tests, ", ")
}

// runPushCommand runs one check as a process in dir with env and returns
// its exit code. A check its context ended is ended with everything it
// started, since npm's and go test's own children do the work.
func runPushCommand(ctx context.Context, command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
	return execx.OSRunner{}.Stream(ctx, execx.Request{Name: command[0], Args: command[1:], Dir: dir, Env: env, KillTree: true}, stdout, stderr)
}

// takePushTurn waits in the machine's common line, the one cfo gate test
// runs wait in, so a push's checks and a gate's tests never run together.
// The turn's budget is the run's time limit, which the run keeps itself.
func takePushTurn(stdout io.Writer, runtime commandRuntime, who string, budget time.Duration) (verify.Turn, error) {
	store, err := verify.AdmissionDir()
	if err != nil {
		return verify.Turn{}, err
	}
	var available func() (uint64, error)
	if runtime.availableMemory != nil {
		available = func() (uint64, error) {
			memory, err := runtime.availableMemory()
			return min(memory.Available, memory.CommitAvailable), err
		}
	}
	return verify.Admission{
		Dir:       store,
		Floor:     supervisor.MemoryFloor,
		Available: available,
		Who:       who,
		Budget:    budget,
		Limit:     runtime.gateWaitLimit,
		Poll:      time.Second,
		Waiting: func(waited time.Duration, why string) {
			fmt.Fprintf(stdout, "cfo gate prepush: waiting for its turn on the machine, %s so far: %s\n", waited.Round(time.Second), strings.ReplaceAll(why, "; ", ", "))
		},
	}.Wait(context.Background())
}

// waitForPushMemory lets check number start only once the machine has the
// memory floor free, physical and commit both, as a run's turn requires. It
// returns "" once it has. Beside a working fleet free memory dips under the
// floor for seconds at a time, so the run waits, saying once what for, and
// reads memory again every pushMemoryPoll. It returns why no further check
// starts when the run's time ends first, at deadline, or at once when memory
// cannot be read, since no reading would say when to go on.
func waitForPushMemory(stdout io.Writer, runtime commandRuntime, number int, deadline time.Time, limit time.Duration) string {
	for isSaid := false; ; isSaid = true {
		if runtime.availableMemory == nil {
			return "free memory cannot be read"
		}
		memory, err := runtime.availableMemory()
		if err != nil {
			return "free memory cannot be read (" + err.Error() + ")"
		}
		free := min(memory.Available, memory.CommitAvailable)
		if free >= supervisor.MemoryFloor {
			return ""
		}
		short := fmt.Sprintf("the machine has %.1f GB of memory free and a check starts only above %.1f GB", verify.Gigabytes(free), verify.Gigabytes(supervisor.MemoryFloor))
		if !time.Now().Before(deadline) {
			return fmt.Sprintf("%s, and the time limit of %s passed while the run waited for it", short, limit)
		}
		if !isSaid {
			fmt.Fprintf(stdout, "cfo gate prepush: waiting to start check %d, since %s\n", number, short)
		}
		time.Sleep(min(runtime.pushMemoryPoll, time.Until(deadline)))
	}
}
