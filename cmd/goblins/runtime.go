package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/runtime"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

func runRuntime(args []string, stdout, stderr io.Writer, commands commandRuntime) int {
	fs := flag.NewFlagSet("runtime", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "render the typed report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "goblins runtime: unexpected arguments")
		return 2
	}
	if commands.resolveHome == nil || commands.localRuntime == nil {
		fmt.Fprintln(stderr, "goblins runtime: command runtime is incomplete")
		return 1
	}
	h, err := commands.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	inventory, err := commands.localRuntime(context.Background(), h)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	report := runtime.Build(h.Root, inventory)
	if *jsonOutput {
		err = runtime.RenderJSON(stdout, report)
	} else {
		err = runtime.RenderMarkdown(stdout, report)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// dispatchNeed is the free memory and commit, in bytes, the next goblin start
// on the home h's machine needs at now: the admission mark the scheduler
// starts by, read with processes as the machine's process list.
func dispatchNeed(h home.Home, processes func() ([]fleettree.Process, error), now time.Time) (uint64, error) {
	room, err := supervisor.ReadRoom(h, processes, now)
	return room.NextStartNeed(), err
}
