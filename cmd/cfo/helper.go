package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runHelper is a goblin's way to a helper goblin of its own:
//
//	cfo helper start <parent-id> --brief <file> [--title "<short title>"]
//
// start hands the brief's text to the supervisor, which alone starts
// helpers: one per goblin at a time, never a helper's, and only when memory
// allows. It prints the helper and its branch, or why not and when to ask
// again.
func runHelper(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, `usage: cfo helper start <parent-id> --brief <file> [--title "<short title>"]`)
		return 2
	}
	switch args[0] {
	case "start":
		return runHelperStart(args[1:], stdout, stderr, runtime)
	}
	fmt.Fprintf(stderr, "cfo helper: unknown subcommand %q\n", args[0])
	return 2
}

func runHelperStart(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || state.ValidTaskID(args[0]) != nil {
		fmt.Fprintln(stderr, "cfo helper start: your task ID comes first: cfo helper start <parent-id> --brief <file>")
		return 2
	}
	parent := args[0]
	flags := flag.NewFlagSet("helper start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	brief := flags.String("brief", "", "the file holding the helper's brief: what it does, and how it knows it is done")
	givenTitle := flags.String("title", "", "the helper's short title for the board")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if *brief == "" {
		fmt.Fprintln(stderr, "cfo helper start: --brief <file> is required")
		return 2
	}
	title := ""
	if *givenTitle != "" {
		var err error
		if title, err = shortTitle(*givenTitle); err != nil {
			fmt.Fprintf(stderr, "cfo helper start: --title: %v\n", err)
			return 2
		}
	}
	text, err := fsx.ReadFile(*brief)
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper start: %v\n", err)
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	started, err := runtime.requestHelper(h, supervisor.HelperRequest{Parent: parent, Brief: string(text), Title: title})
	if err != nil {
		fmt.Fprintf(stderr, "cfo helper start: not started: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "starting helper %s on branch %s, cut from your last commit; you are told here when it is up, and it reports to you here. While you only wait on it, run: cfo notify %s --waiting-on %s \"<why>\"\n", started.ID, started.Branch, parent, started.ID)
	return 0
}
