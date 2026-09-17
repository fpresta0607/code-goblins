package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/runtime"
)

func runRuntime(args []string, stdout, stderr io.Writer, commands commandRuntime) int {
	fs := flag.NewFlagSet("runtime", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "render the typed report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "cfo runtime: unexpected arguments")
		return 2
	}
	if commands.resolveHome == nil || commands.localRuntime == nil {
		fmt.Fprintln(stderr, "cfo runtime: command runtime is incomplete")
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
