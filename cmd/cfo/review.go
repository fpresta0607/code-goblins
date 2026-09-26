package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// reviewPublishTimeout bounds publishing or withdrawing an item, the Herdr
// identity proof included.
var reviewPublishTimeout = 20 * time.Second

// runReview reports an item that stays in the Command Center until the
// Overlord answers or clears it, withdraws the reporter's own item, or clears
// any open item for the registered primary CFO:
//
//	cfo review --id <stable-id> --title "<what to look at>" [--task <id>] [--image <path>]... [--lavish <url|html-file>]
//	cfo review --id <stable-id> --withdraw "<reason>" [--task <id>]
//	cfo review --clear <stable-id> --reason "<why>"
//
// A Lavish page named by its HTML file is opened without a browser and polled
// by the supervisor, so the Overlord's feedback on it reaches the CFO as a
// review wake; nobody runs lavish-axi poll themselves.
func runReview(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("review", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	task := f.String("task", "", "your task ID, run from your own pane; omit only from the registered primary CFO")
	title := f.String("title", "", "what the Overlord should look at")
	lavish := f.String("lavish", "", "the Lavish page: its link, or its HTML file for the supervisor to poll")
	withdraw := f.String("withdraw", "", "withdraw your open item with this reason")
	clear := f.String("clear", "", "as the registered primary CFO, clear any open item by its ID, such as a retired goblin's")
	reason := f.String("reason", "", "why the item is cleared; kept on the item and in state/reviews.audit")
	var images []string
	f.Func("image", "an image to review; repeat for each, in order", func(v string) error {
		images = append(images, v)
		return nil
	})
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	if *withdraw != "" && (*title != "" || *lavish != "" || len(images) > 0) {
		fmt.Fprintln(stderr, "cfo review: --withdraw takes only --id and --task")
		return 2
	}
	switch {
	case *clear != "" && strings.TrimSpace(*reason) == "":
		fmt.Fprintln(stderr, "cfo review: --clear needs --reason")
		return 2
	case *clear != "" && (*id != "" || *task != "" || *title != "" || *lavish != "" || *withdraw != "" || len(images) > 0):
		fmt.Fprintln(stderr, "cfo review: --clear takes only --reason")
		return 2
	case *clear == "" && *reason != "":
		fmt.Fprintln(stderr, "cfo review: --reason goes with --clear")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	terminals := terminal.HerdrSessions(&herdr.Client{Commands: execx.OSRunner{}})
	if *clear != "" {
		ctx, cancel := context.WithTimeout(context.Background(), reviewPublishTimeout)
		defer cancel()
		if err := supervisor.ClearReview(ctx, h, terminals, *clear, *reason); err != nil {
			fmt.Fprintln(stderr, "cfo review: "+err.Error())
			return 1
		}
		fmt.Fprintln(stdout, "Review cleared:", *clear)
		return 0
	}
	if *withdraw != "" {
		ctx, cancel := context.WithTimeout(context.Background(), reviewPublishTimeout)
		defer cancel()
		if err := supervisor.WithdrawReview(ctx, h, terminals, *task, *id, *withdraw); err != nil {
			fmt.Fprintln(stderr, "cfo review: "+err.Error())
			return 1
		}
		fmt.Fprintln(stdout, "Review withdrawn:", *id)
		return 0
	}
	// Opening the page gets its own budget, so a slow first start of
	// lavish-axi's server cannot use up the time publishing needs.
	var page string
	if *lavish != "" && !strings.Contains(*lavish, "://") {
		if page, err = lavishPageFile(*lavish); err != nil {
			fmt.Fprintf(stderr, "cfo review: --lavish takes a link or an HTML page: %v\n", err)
			return 2
		}
		openCtx, openCancel := context.WithTimeout(context.Background(), pageOpenTimeout)
		*lavish, err = (axi.Lavish{Commands: execx.OSRunner{}}).Open(openCtx, page)
		openCancel()
		if err != nil {
			fmt.Fprintf(stderr, "cfo review: lavish-axi cannot show %s (%v)\n", page, err)
			return 1
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), reviewPublishTimeout)
	defer cancel()
	if err := supervisor.PublishReview(ctx, h, terminals, *task, *id, *title, *lavish, page, images); err != nil {
		fmt.Fprintln(stderr, "cfo review: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Review published:", *id)
	return 0
}
