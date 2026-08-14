package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/fleet"
)

func runFleet(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("fleet-view", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "render the typed snapshot as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo fleet-view: unexpected arguments")
		return 2
	}
	if runtime.resolveHome == nil || runtime.snapshot == nil {
		fmt.Fprintln(stderr, "cfo fleet-view: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	snapshot, err := runtime.snapshot(context.Background(), h)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOutput {
		err = fleet.RenderJSON(stdout, snapshot)
	} else {
		err = fleet.RenderMarkdown(stdout, snapshot)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
