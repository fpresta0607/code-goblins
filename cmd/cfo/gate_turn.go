package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
// with its name, and with the turn free and memory to spare it writes none.
// The exit code is the command's. The turn is held until the command has
// ended, through an interrupt too, which the command is sent as well and
// answers for itself; an interrupt while the run waits takes it out of the
// line.
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

	signals, stop := runtime.interrupts()
	defer stop()
	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	waited := make(chan struct{})
	go func() {
		select {
		case <-signals:
			leave()
		case <-waited:
		}
	}()
	turn, err := takeGateTurn(ctx, "cfo gate turn", stderr, stderr, runtime.availableMemory, gateTurnWho(as, command, dir, runtime), 0, verify.NoLimit)
	close(waited)
	if err != nil {
		return exitNoTurn
	}
	defer turn.Release()

	process := execx.Command(command[0], command[1:]...)
	process.Stdin, process.Stdout, process.Stderr = os.Stdin, stdout, stderr
	process.Env = append(os.Environ(), verify.TurnVariable+"="+turn.Token())
	if err := process.Start(); err != nil {
		fmt.Fprintf(stderr, "cfo gate turn: the command did not start: %v\n", err)
		return exitNotStarted
	}
	// What Wait returns for a command that ended is its exit status, which
	// the run passes on as it is.
	process.Wait()
	return process.ProcessState.ExitCode()
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
