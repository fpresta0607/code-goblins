package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// cfo gate turn's own exit codes, for a run whose command gave none. Neither
// is a test result, and a command that itself exits with one is told apart
// by the last line on standard error, which then is not cfo gate turn's.
const (
	// exitNoTurn says no turn was taken and the command was never started.
	exitNoTurn = 125
	// exitNotStarted says a turn was taken and given back, and the command
	// could not be started.
	exitNotStarted = 126
)

// statusControlCExit is the exit status of a console program Ctrl+C ended.
const statusControlCExit = 0xC000013A

const gateTurnUsage = "usage: cfo gate turn [--as <name>] -- <program> [<argument>...]"

// runGateTurn runs any command in the machine's one line for heavy runs, the
// line cfo gate test takes its turns in, so that one such run goes at a time
// whoever started it: another repository's test command under its gate, a
// goblin's own suite, a run one of the Overlord's sessions starts. It waits
// while another run holds the turn and while free physical memory or free
// commit is under the floor, for as long as that lasts: it sets no limit of
// its own, and the limits that end a run are its command's and its caller's.
// While it waits it says on standard error, as it starts to, whenever its
// place in line changes and once a minute, which run holds the turn and
// since when, and where this run stands.
//
// The command is the wrapper's in every other respect. It is started with
// the arguments after --, unparsed, in this folder, with this process's
// standard input, output and error and its environment, to which the turn is
// added (verify.TurnVariable) so that a command that takes turns itself, as
// cfo gate test does, runs inside this one. Standard output is the command's
// alone, every line of cfo gate turn's own goes to standard error and starts
// with its name, and with the turn free and memory to spare it writes none,
// unless the goblin whose run it is could not be told that it ended
// (tellGoblinRunEnded). The exit code is the command's. The turn is held
// until the command has ended, through an interrupt too, which the command
// is sent as well, in the console group it shares with this run, and
// answers for itself; an interrupt while the run waits takes it out of the
// line.
//
// The command runs as a measured execution (verify.Executor), in a job of
// its own that nothing it starts can leave: a run that is ended, as its
// caller's time limit ends it, ends with everything its command started.
// Once the command's own process has exited, what it left running may run
// on for the linger, after which the run ends it and names each process,
// but a machine service it started, which serves the whole machine, is left
// running and named. While free memory or commit stays under the desktop
// reserve, the run ends its command with everything it started and says so.
// The execution's record is kept with the report store's others, and holds
// no output, which is this run's caller's.
func runGateTurn(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	as, command, err := parseGateTurn(args)
	if err != nil {
		fmt.Fprintln(stderr, gateTurnUsage)
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: %v\n", err)
		return exitNoTurn
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: %v\n", err)
		return exitNoTurn
	}

	started := time.Now()
	signals, stop := runtime.interrupts()
	defer stop()
	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	// One reader takes every interrupt the run is sent, while it waits and
	// while its command runs, so none is lost between the two.
	var isInterrupted atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-signals:
				isInterrupted.Store(true)
				leave()
			case <-done:
				return
			}
		}
	}()
	// A run its goblin may have left in the background tells it how it
	// ended, once the turn is given back, unless it was interrupted.
	tell := func(exit int) {
		if isInterrupted.Load() || exit == statusControlCExit {
			return
		}
		what := strings.Join(command, " ")
		if as != "" {
			what = as
		}
		tellGoblinRunEnded(ctx, runtime, os.Getenv, "cfo gate turn", dir, what, exit, time.Since(started), stderr)
	}
	executor, err := measuredExecutor("cfo gate turn", gateTurnWho(as, command, dir, runtime), stderr, runtime.availableMemory)
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: %v\n", err)
		tell(exitNoTurn)
		return exitNoTurn
	}
	// The command writes to this run's own output, which a process's always
	// is a file of, so it sees what this run's caller gives it, console or
	// pipe, and the caller keeps what it writes.
	commandOutput, isOutputFile := stdout.(*os.File)
	commandErrors, isErrorsFile := stderr.(*os.File)
	if !isOutputFile || !isErrorsFile {
		fmt.Fprintln(stderr, "cfo gate turn: this run takes no turn: its standard output and error are not files, as a process's own are")
		tell(exitNoTurn)
		return exitNoTurn
	}
	executor.Stdin, executor.Stdout, executor.Stderr = os.Stdin, commandOutput, commandErrors
	executor.IsConsoleShared = true
	result, record, runError := executor.RunWithin(ctx, execx.Request{Dir: dir, Name: command[0], Args: command[1:]}, 0)
	// A command whose cleanup was not confirmed keeps the machine's line
	// until it is, and this process is its only owner.
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := executor.RetryCleanup(cleanup); err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: what its command left may not all have ended: %s\n", oneLine(err))
	}
	if evidence, err := verify.LoadExecution(record); record != "" && err == nil {
		if leftovers := evidence.Measurement.Leftovers; len(leftovers) > 0 {
			fmt.Fprintf(stderr, "cfo gate turn: its command left %s running after it exited, which the run ended\n", processNames(leftovers))
		}
		if spared := evidence.Measurement.Spared; len(spared) > 0 {
			fmt.Fprintf(stderr, "cfo gate turn: its command started %s, which serves the whole machine, so the run left it running\n", processNames(spared))
		}
	}
	if err := verify.PruneExecutions(time.Now()); err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: old execution records could not all be removed: %s\n", oneLine(err))
	}
	switch {
	case errors.Is(runError, verify.ErrNoTurn):
		why := oneLine(runError)
		if errors.Is(runError, context.Canceled) {
			why = "it was interrupted while it waited"
		}
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: %s\n", why)
		tell(exitNoTurn)
		return exitNoTurn
	case errors.Is(runError, verify.ErrNotStarted):
		fmt.Fprintf(stderr, "cfo gate turn: the command did not start: %s\n", oneLine(runError))
		tell(exitNotStarted)
		return exitNotStarted
	case runError != nil && !isInterrupted.Load():
		fmt.Fprintf(stderr, "cfo gate turn: %s\n", oneLine(runError))
	}
	exit := result.ExitCode
	// A command whose own exit status could not be read failed all the same.
	if exit < 0 {
		exit = 1
	}
	tell(exit)
	return exit
}

// oneLine is err's text on one line, the errors it joins separated by commas.
func oneLine(err error) string {
	return strings.ReplaceAll(err.Error(), "\n", ", ")
}

// parseGateTurn reads cfo gate turn's arguments: its own before --, the
// command's after it. Only -- says where the command starts, so nothing of
// the command's is ever read as the wrapper's.
func parseGateTurn(args []string) (as string, command []string, err error) {
	for at := 0; at < len(args); at++ {
		name, isNamed := strings.CutPrefix(args[at], "--as=")
		switch {
		case args[at] == "--":
			if at+1 == len(args) {
				return "", nil, errors.New("no command follows --")
			}
			return as, args[at+1:], nil
		case isNamed:
			as = name
		case args[at] == "--as" && at+1 < len(args) && args[at+1] != "--":
			at++
			as = args[at]
		case args[at] == "--as":
			return "", nil, errors.New("--as is given no name")
		default:
			return "", nil, fmt.Errorf("%q is no argument of cfo gate turn, and its command goes after --", args[at])
		}
	}
	return "", nil, errors.New("no -- comes before a command")
}

// gateTurnWho names a run to the runs that wait behind it and to cfo gate
// turns: the name its caller gave it, or else its folder, with its command,
// and the fleet task whose run it is when it is one's. A caller such as a
// gate knows its repository, branch and run, which the command and the
// folder do not say. A run whose task cannot be read stands in line all the
// same, under what is known of it.
func gateTurnWho(as string, command []string, dir string, runtime commandRuntime) string {
	who := strings.Join(command, " ")
	if as != "" {
		who = as + ": " + who
	} else {
		who += " in " + dir
	}
	if task, err := gateTask(dir, runtime); err == nil && task != "" {
		who += ", task " + taskLabel(task, runtime)
	}
	return who
}
