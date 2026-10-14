package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/verify"
)

// cfo gate turn's own exit codes, for a run whose command gave none. Neither
// is a test result, and a command that itself exits with one is told apart
// by the last line on standard error, which then is not cfo gate turn's.
const (
	// exitNoTurn says the run gave no result of its command's: the command
	// never started, as no turn was taken or the run was interrupted before
	// it could start, or the line ended the command for the machine's
	// memory.
	exitNoTurn = 125
	// exitNotStarted says a turn was taken and given back, and the command
	// could not be started.
	exitNotStarted = 126
)

// statusControlCExit is the exit status of a console program Ctrl+C ended.
const statusControlCExit = 0xC000013A

const gateTurnUsage = "usage: cfo gate turn [--as <name>] [--detach <log-file>] -- <program> [<argument>...]"

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
//
// With --detach the run is started again as a process of its own, hidden and
// detached (detachGateTurn), and this one returns at once. The detached run
// says on its last line how it ended.
func runGateTurn(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	request, err := parseGateTurn(args)
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
	if request.detachLog != "" {
		return detachGateTurn(request, dir, stdout, stderr, runtime)
	}
	started := time.Now()
	exit := runGateTurnCommand(request, dir, started, stdout, stderr, runtime)
	if request.isDetached {
		fmt.Fprintf(stderr, "cfo gate turn: the detached run ended with exit %d after %s\n", exit, time.Since(started).Round(time.Second))
	}
	return exit
}

// runGateTurnCommand waits for the turn and runs the request's command in
// dir, as runGateTurn says, and returns the run's exit code.
func runGateTurnCommand(request gateTurnRequest, dir string, started time.Time, stdout, stderr io.Writer, runtime commandRuntime) int {
	as, command := request.as, request.command
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
		tellGoblinRunEnded(ctx, runtime, os.Getenv, "cfo gate turn", dir, what, request.isDetached, exit, time.Since(started), stderr)
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
	if !isOutputFile || !isErrorsFile || commandOutput == nil || commandErrors == nil {
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
	if record != "" {
		if evidence, err := verify.LoadExecution(record); err == nil {
			if leftovers := evidence.Measurement.Leftovers; len(leftovers) > 0 {
				fmt.Fprintf(stderr, "cfo gate turn: its command left %s running after it exited, which the run ended\n", processNames(leftovers))
			}
			if spared := evidence.Measurement.Spared; len(spared) > 0 {
				fmt.Fprintf(stderr, "cfo gate turn: its command started %s, which serves the whole machine, so the run left it running\n", processNames(spared))
			}
		}
	}
	if err := verify.PruneExecutions(time.Now()); err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: old execution records could not all be removed: %s\n", oneLine(err))
	}
	switch {
	case errors.Is(runError, verify.ErrNoTurn):
		why := oneLine(runError)
		if errors.Is(runError, context.Canceled) {
			why = "it was interrupted before its command started"
		}
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: %s\n", why)
		tell(exitNoTurn)
		return exitNoTurn
	case errors.Is(runError, verify.ErrNotStarted):
		fmt.Fprintf(stderr, "cfo gate turn: the command did not start: %s\n", oneLine(runError))
		tell(exitNotStarted)
		return exitNotStarted
	// What the command was ended at is no result of its own, so a gate parks
	// the run on 125 and this last line rather than ask for a repair.
	case errors.Is(runError, verify.ErrPressure) && !isInterrupted.Load():
		fmt.Fprintf(stderr, "cfo gate turn: the run ended its command, since free memory or commit stayed under the desktop reserve: %s\n", oneLine(runError))
		tell(exitNoTurn)
		return exitNoTurn
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

// gateTurnRequest is what cfo gate turn's arguments ask for.
type gateTurnRequest struct {
	// as is the name the run goes by in the line, empty for its command's.
	as string
	// detachLog, when set, asks for the run to be started detached and
	// hidden, its output appended to this file.
	detachLog string
	// isDetached says this process is a run --detach started.
	isDetached bool
	command    []string
}

// parseGateTurn reads cfo gate turn's arguments: its own before --, the
// command's after it. Only -- says where the command starts, so nothing of
// the command's is ever read as the wrapper's. --detached is not for people:
// it is how detachGateTurn starts the run it detaches.
func parseGateTurn(args []string) (gateTurnRequest, error) {
	var request gateTurnRequest
	for at := 0; at < len(args); at++ {
		name, isNamed := strings.CutPrefix(args[at], "--as=")
		switch {
		case args[at] == "--":
			if at+1 == len(args) {
				return gateTurnRequest{}, errors.New("no command follows --")
			}
			if args[at+1] == "" {
				return gateTurnRequest{}, errors.New("the command after -- names no program")
			}
			request.command = args[at+1:]
			return request, nil
		case isNamed:
			request.as = name
		case args[at] == "--as" && at+1 < len(args) && args[at+1] != "--":
			at++
			request.as = args[at]
		case args[at] == "--as":
			return gateTurnRequest{}, errors.New("--as is given no name")
		case args[at] == "--detach" && at+1 < len(args) && args[at+1] != "--" && args[at+1] != "":
			at++
			request.detachLog = args[at]
		case args[at] == "--detach":
			return gateTurnRequest{}, errors.New("--detach is given no log file")
		case args[at] == "--detached":
			request.isDetached = true
		default:
			return gateTurnRequest{}, fmt.Errorf("%q is no argument of cfo gate turn, and its command goes after --", args[at])
		}
	}
	return gateTurnRequest{}, errors.New("no -- comes before a command")
}

// detachGateTurn starts the run request asks for as a process of its own in
// dir, the way the supervisor is started (startDetached): with a console of
// its own that has no window, which its command shares, so nothing of the
// run ever shows on the desktop, in a process group of its own and out of
// its caller's job where the job allows it, so it outlives its caller as a
// background run must. Its output and its errors are appended to the log,
// and its pid is written to the log's name with .pid added, for its goblin
// to end it by. It returns once the run has started, saying on standard
// output where to find it, and 125 when it could not start it.
func detachGateTurn(request gateTurnRequest, dir string, stdout, stderr io.Writer, runtime commandRuntime) int {
	self := runtime.gateTurnSelf
	if self == nil {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: it could not be started detached: %v\n", err)
			return exitNoTurn
		}
		self = []string{executable, "gate", "turn"}
	}
	log, err := filepath.Abs(request.detachLog)
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: it could not be started detached: %v\n", err)
		return exitNoTurn
	}
	args := append(append([]string(nil), self[1:]...), "--detached")
	if request.as != "" {
		args = append(args, "--as", request.as)
	}
	args = append(append(args, "--"), request.command...)
	command, err := startDetached(self[0], dir, log, args...)
	if err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: this run takes no turn: it could not be started detached: %v\n", err)
		return exitNoTurn
	}
	pid := command.Process.Pid
	_ = command.Process.Release()
	pidFile := log + ".pid"
	if err := fsx.AtomicWriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n")); err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: started the run hidden and detached as pid %d, its output going to %s, but its pid could not be written to %s: %v\n", pid, log, pidFile, err)
		return 1
	}
	fmt.Fprintf(stdout, "cfo gate turn: started the run hidden and detached as pid %d: its output goes to %s, its pid is in %s, and its last line there says how it ended\n", pid, log, pidFile)
	return 0
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
