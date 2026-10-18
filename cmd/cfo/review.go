package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runReview reports an item that stays in the Command Center until the
// Overlord answers or clears it, withdraws the reporter's own item, or clears
// any open item for the registered primary CFO:
//
//	cfo review --id <stable-id> --title "<what to look at>" [--task <id>] [--image <path>]... [--lavish <url|html-file> [--reopen]]
//	cfo review --id <stable-id> --withdraw "<reason>" [--task <id>]
//	cfo review --clear <stable-id> --reason "<why>"
//
// A Lavish page named by its HTML file is opened without a browser, and one
// named by its Scrawl link is the page lavish-axi serves there; the supervisor
// polls either, so the Overlord's feedback on it reaches the CFO as a review
// wake; nobody runs lavish-axi poll themselves. A page whose review session
// has ended, whoever ended it, is refused by its link or by its HTML file,
// recording nothing, since its card would open a page that says so, unless
// --reopen opens it again with lavish-axi's own --reopen, which only his
// asking for another look warrants.
func runReview(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("review", flag.ContinueOnError)
	f.SetOutput(stderr)
	id := f.String("id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	task := f.String("task", "", "your task ID, run from your own pane; omit only from the registered primary CFO")
	title := f.String("title", "", "what the Overlord should look at")
	lavish := f.String("lavish", "", "the Scrawl page the supervisor polls: its link, or its HTML file")
	reopen := f.Bool("reopen", false, "open the --lavish page again though its review session has ended; only when the Overlord asked for another look")
	withdraw := f.String("withdraw", "", "withdraw your open item with this reason")
	clear := f.String("clear", "", "as the registered primary CFO, clear any open item by its ID, such as a retired goblin's")
	reason := f.String("reason", "", "why the item is cleared; kept on the item and in state/reviews.audit")
	var images []string
	f.Func("image", "an image to review; repeat for each, in order", func(v string) error {
		images = append(images, v)
		return nil
	})
	if !parseFlags(f, args) {
		return 2
	}
	if *withdraw != "" && (*title != "" || *lavish != "" || len(images) > 0 || *reopen) {
		fmt.Fprintln(stderr, "cfo review: --withdraw takes only --id and --task")
		return 2
	}
	switch {
	case *clear != "" && strings.TrimSpace(*reason) == "":
		fmt.Fprintln(stderr, "cfo review: --clear needs --reason")
		return 2
	case *clear != "" && (*id != "" || *task != "" || *title != "" || *lavish != "" || *withdraw != "" || len(images) > 0 || *reopen):
		fmt.Fprintln(stderr, "cfo review: --clear takes only --reason")
		return 2
	case *clear == "" && *reason != "":
		fmt.Fprintln(stderr, "cfo review: --reason goes with --clear")
		return 2
	case *reopen && *lavish == "":
		fmt.Fprintln(stderr, "cfo review: --reopen goes with --lavish")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *clear != "" {
		if err := supervisor.ClearReview(h, *clear, *reason); err != nil {
			fmt.Fprintln(stderr, "cfo review: "+err.Error())
			return 1
		}
		fmt.Fprintln(stdout, "Review cleared:", *clear)
		return 0
	}
	if *withdraw != "" {
		if err := supervisor.WithdrawReview(h, *task, *id, *withdraw); err != nil {
			fmt.Fprintln(stderr, "cfo review: "+err.Error())
			return 1
		}
		fmt.Fprintln(stdout, "Review withdrawn:", *id)
		return 0
	}
	// Opening the page gets its own budget, so a slow first start of
	// lavish-axi's server cannot use up the time publishing needs.
	var page string
	switch {
	case *lavish != "" && !strings.Contains(*lavish, "://"):
		if page, err = lavishPageFile(*lavish); err != nil {
			fmt.Fprintf(stderr, "cfo review: --lavish takes a link or an HTML page: %v\n", err)
			return 2
		}
		if !*reopen {
			if err := supervisor.RefuseEndedPage(page); err != nil {
				fmt.Fprintln(stderr, "cfo review: "+err.Error())
				return 1
			}
		}
	case *reopen:
		session, found, err := (axi.Lavish{}).SessionAt(*lavish)
		if err != nil {
			fmt.Fprintln(stderr, "cfo review: "+err.Error())
			return 1
		}
		if !found {
			fmt.Fprintln(stderr, "cfo review: --reopen takes the page's HTML file, or the link of a Scrawl session lavish-axi keeps")
			return 2
		}
		page = session.File
	}
	if page != "" {
		lavishAxi := axi.Lavish{Commands: execx.OSRunner{}}
		open := lavishAxi.Open
		if *reopen {
			open = lavishAxi.Reopen
		}
		openCtx, openCancel := context.WithTimeout(context.Background(), pageOpenTimeout)
		*lavish, err = open(openCtx, page)
		openCancel()
		if errors.Is(err, axi.ErrSessionEnded) {
			fmt.Fprintln(stderr, "cfo review: "+supervisor.EndedPage(page).Error())
			return 1
		}
		if err != nil {
			fmt.Fprintf(stderr, "cfo review: lavish-axi cannot show %s (%v)\n", page, err)
			return 1
		}
	}
	if err := supervisor.PublishReview(h, *task, *id, *title, *lavish, page, images); err != nil {
		fmt.Fprintln(stderr, "cfo review: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Review published:", *id)
	return 0
}
