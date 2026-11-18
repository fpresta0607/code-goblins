package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fleet"
)

func runFleet(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("fleet-view", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "render the typed snapshot as JSON")
	name := fs.String("name", "", "find the tasks whose goblin the board calls `name`, ignoring case, newest first")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "goblins fleet-view: unexpected arguments")
		return 2
	}
	isLookup := false
	fs.Visit(func(set *flag.Flag) { isLookup = isLookup || set.Name == "name" })
	if isLookup && strings.TrimSpace(*name) == "" {
		fmt.Fprintln(stderr, "goblins fleet-view: --name needs a goblin's name")
		return 2
	}
	if runtime.resolveHome == nil || runtime.snapshot == nil {
		fmt.Fprintln(stderr, "goblins fleet-view: command runtime is incomplete")
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
	if isLookup {
		matches := fleet.FindByName(snapshot, *name)
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "goblins fleet-view: no goblin has the name %q\n", strings.TrimSpace(*name))
			return 1
		}
		if *jsonOutput {
			encoder := json.NewEncoder(stdout)
			encoder.SetEscapeHTML(false)
			err = encoder.Encode(matches)
		} else {
			err = fleet.RenderNameMatches(stdout, matches)
		}
	} else if *jsonOutput {
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
