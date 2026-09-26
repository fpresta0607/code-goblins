package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// runDeliver hands the Overlord a document as a Command Center item, with a
// file icon, Open and Download, until he opens or downloads it or clears it:
//
//	cfo deliver --id <stable-id> --title "<what it is>" --file <path> [--url <link>] [--task <id>]
//
// The file is copied, so it outlives the original; --url is where Open goes
// instead of the copy, such as a hosted page.
func runDeliver(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("deliver", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	title := f.String("title", "", "what the document is, for the Overlord")
	file := f.String("file", "", "the document to deliver: a goblin's from its worktree, task scratch or data directory")
	link := f.String("url", "", "optional page Open goes to instead of the copy: https, or http on this machine; no query")
	task := f.String("task", "", "your task ID, run from your own pane; omit only from the registered primary CFO")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
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
	ctx, cancel := context.WithTimeout(context.Background(), reviewPublishTimeout)
	defer cancel()
	terminals := terminal.HerdrSessions(&herdr.Client{Commands: execx.OSRunner{}})
	if err := supervisor.DeliverDocument(ctx, h, terminals, *task, *id, *title, *file, *link); err != nil {
		fmt.Fprintln(stderr, "cfo deliver: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Document delivered:", *id)
	return 0
}
