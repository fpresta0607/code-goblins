package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/update"
)

const backlogUsage = "usage: goblins backlog done <id> | accept <id> --generation <generation> --report <file> --receipt <file> | review\n"

func runBacklog(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, backlogUsage)
		return 0
	}
	if len(args) == 1 && args[0] == "review" {
		return runBacklogReview(stdout, stderr, runtime)
	}
	if len(args) >= 2 && args[0] == "accept" {
		return runBacklogAccept(args[1], args[2:], stdout, stderr, runtime)
	}
	if len(args) != 2 || args[0] != "done" {
		fmt.Fprint(stderr, backlogUsage)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "goblins backlog: not a primary home")
		return 1
	}
	if err := fleet.CompleteQueuedTask(h, args[1]); err != nil {
		fmt.Fprintf(stderr, "goblins backlog: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "closed backlog row %s; delivery outcome and archived source kept\n", args[1])
	return 0
}

// runBacklogAccept is the registered primary CEO's word that one generation of
// a task delivered what it installed, for a task that reports no pull request.
// Before cleanup the accepted delivery is kept beside the task and survives its
// retirement; after it, the task's queued row closes too.
func runBacklogAccept(id string, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := flag.NewFlagSet("backlog accept", flag.ContinueOnError)
	flags.SetOutput(stderr)
	generation := flags.String("generation", "", "the task's exact spawn generation, from its record or, once cleaned up, its outcome")
	report := flags.String("report", "", "the saved acceptance report, a file in the home's data folder")
	receipt := flags.String("receipt", "", "the saved installation receipt, a JSON file in the home's data folder naming the task, its generation, the installed image's path and sha256 and the daemon's pid and creation time")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *generation == "" || *report == "" || *receipt == "" {
		fmt.Fprint(stderr, backlogUsage)
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !home.IsPrimary(h) {
		fmt.Fprintln(stderr, "goblins backlog: not a primary home")
		return 1
	}
	identity, err := primaryApproval(h.State)
	if err != nil {
		fmt.Fprintf(stderr, "goblins backlog: a delivery is accepted only by the registered primary CEO: %v\n", err)
		return 1
	}
	request := fleet.Acceptance{ID: id, Generation: *generation, AcceptedBy: identity}
	if request.Report, err = filepath.Abs(*report); err != nil {
		fmt.Fprintf(stderr, "goblins backlog: --report %v\n", err)
		return 2
	}
	if request.Receipt, err = filepath.Abs(*receipt); err != nil {
		fmt.Fprintf(stderr, "goblins backlog: --receipt %v\n", err)
		return 2
	}
	isNew, err := fleet.AcceptDelivery(h, request, runtime.installation)
	if err != nil {
		fmt.Fprintf(stderr, "goblins backlog: %v\n", err)
		return 1
	}
	supervisor.Reported(h.State)
	verb := "accepted"
	if !isNew {
		verb = "already accepted"
	}
	fmt.Fprintf(stdout, "%s the delivery of %s at generation %s; what waits on it no longer does\n", verb, id, *generation)
	if _, err := os.Stat(state.TaskMetaPath(h.State, id)); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			fmt.Fprintf(stderr, "goblins backlog: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s is not cleaned up yet; goblins cleanup keeps this delivery and closes its backlog row as done\n", id)
		return 0
	}
	switch err := fleet.CompleteQueuedTask(h, id); {
	case errors.Is(err, fleet.ErrNotQueued):
	case err != nil:
		fmt.Fprintf(stderr, "goblins backlog: the delivery is accepted, but its backlog row did not close: %v\n", err)
		return 1
	default:
		fmt.Fprintf(stdout, "its backlog row is under ## Done; the retirement outcome is kept as cleanup wrote it\n")
	}
	return 0
}

// machineInstallation reads installation receipts against this machine.
func machineInstallation() fleet.Installation {
	return fleet.Installation{
		Hash: update.HashFile,
		DaemonImage: func(pid int, started time.Time) (string, error) {
			identity, err := proc.Identify(pid, started)
			return identity.Image, err
		},
	}
}
