package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// stopTimeout bounds the wait for a supervisor asked to stop, and stopPoll is
// how often goblins stop looks.
var (
	stopTimeout = 30 * time.Second
	stopPoll    = 250 * time.Millisecond
)

// runStatus says whether the supervisor runs and, when it does, where its
// board is, what the fleet is doing and its pid. It exits 1 when none runs,
// so a script can test for it.
func runStatus(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: goblins status")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	record, err := readBoardRecord(h.State)
	if err != nil {
		fmt.Fprintln(stdout, "The supervisor is not running; goblins starts it.")
		return 1
	}
	status, err := boardStatus(context.Background(), record.URL)
	if err != nil {
		fmt.Fprintln(stdout, "The supervisor is not running; goblins starts it.")
		return 1
	}
	fmt.Fprintf(stdout, "  board   %s\n  status  %s\n  pid     %d\n", record.URL, status, record.PID)
	return 0
}

// runStop stops the supervisor: it asks the one its board record names to
// stop, as Ctrl-C would, and waits until its board no longer answers. With
// --force it ends the supervisor's whole process tree instead, for one that
// does not stop when asked.
func runStop(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("stop", flag.ContinueOnError)
	f.SetOutput(stderr)
	force := f.Bool("force", false, "end the supervisor's process tree instead of asking it to stop")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	record, err := readBoardRecord(h.State)
	if err != nil {
		fmt.Fprintln(stdout, "The supervisor is not running.")
		return 0
	}
	if _, err := boardStatus(ctx, record.URL); err != nil {
		// Nothing answers where the record points: its supervisor ended
		// without removing it.
		removeBoardRecord(h.State, record.PID)
		fmt.Fprintln(stdout, "The supervisor is not running.")
		return 0
	}
	if *force {
		if err := runtime.killTree(record.PID); err != nil {
			fmt.Fprintf(stderr, "goblins: the supervisor (pid %d) could not be ended: %v\n", record.PID, err)
			return 1
		}
		removeBoardRecord(h.State, record.PID)
		fmt.Fprintf(stdout, "The supervisor (pid %d) was ended.\n", record.PID)
		return 0
	}
	if err := supervisor.RequestStop(h.State, record.PID); err != nil {
		fmt.Fprintf(stderr, "goblins: the supervisor could not be asked to stop: %v\n", err)
		return 1
	}
	for deadline := time.Now().Add(stopTimeout); time.Now().Before(deadline); time.Sleep(stopPoll) {
		if _, err := boardStatus(ctx, record.URL); err != nil {
			fmt.Fprintf(stdout, "The supervisor (pid %d) stopped.\n", record.PID)
			return 0
		}
	}
	fmt.Fprintf(stderr, "goblins: the supervisor (pid %d) did not stop within %s; goblins stop --force ends it\n", record.PID, stopTimeout)
	return 1
}
