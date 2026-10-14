package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runDeliver hands the Overlord a file as a Scrawl page that shows it, a
// picture inline and a document readable in the page, presented as a review
// item in the Command Center:
//
//	cfo deliver --id <stable-id> --title "<what it is>" --file <path> [--download] [--task <id>]
//
// The file is copied beside the page, so it outlives the original, and the
// supervisor watches the page, so what the Overlord writes there reaches the
// goblin or the CFO that delivered it as a review wake. --download offers the
// file to download on the page, for a generated asset or a file he asked to
// keep; a file the page cannot show needs it.
func runDeliver(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("deliver", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	title := f.String("title", "", "what the file is, for the Overlord")
	file := f.String("file", "", "the file to deliver: a goblin's from its worktree, task scratch or data directory")
	isDownload := f.Bool("download", false, "offer the file to download on its page: a generated asset or a file the Overlord asked to keep")
	task := f.String("task", "", "your task ID, run from your own pane; omit only from the registered primary CFO")
	if !parseFlags(f, args) {
		return 2
	}
	for _, required := range []struct{ flag, value string }{{"--id", *id}, {"--title", *title}, {"--file", *file}} {
		if required.value == "" {
			fmt.Fprintf(stderr, "cfo deliver: %s is required\n", required.flag)
			return 2
		}
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// Opening the page gets its own budget, as cfo review's does.
	open := func(page string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), pageOpenTimeout)
		defer cancel()
		link, err := (axi.Lavish{Commands: execx.OSRunner{}}).Open(ctx, page)
		if err != nil {
			return "", fmt.Errorf("lavish-axi cannot show %s (%w)", page, err)
		}
		return link, nil
	}
	if err := supervisor.DeliverDocument(h, *task, *id, *title, *file, *isDownload, open); err != nil {
		fmt.Fprintln(stderr, "cfo deliver: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Delivered as a Scrawl page:", *id)
	return 0
}
