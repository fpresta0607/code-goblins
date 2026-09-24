package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// runNotify is the goblin-to-CFO direct ping: it writes a task's outcome
// straight into the wake queue with the actual payload (PR URL, question, or
// failure reason), so the CFO is woken with the real thing instead of the
// watcher guessing from pane text. Identical for claude, codex, and pi.
//
//	cfo notify <task-id> --done --pr <url>
//	cfo notify <task-id> --blocked "<question>"
//	cfo notify <task-id> --blocked "<question> options: a | b" --image a.png --image b.png
//	cfo notify <task-id> --failed "<reason>"
//	cfo notify <task-id> --working "<what>"
//	cfo notify <task-id> --waiting-on <task-id|overlord|ci|deploy> "<why>"
//
// Only a question and a wait on the Overlord wake the CFO: working, and a
// wait on another task, CI or a deploy, are status for the board.
func runNotify(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo notify: task ID is required")
		return 2
	}
	id := args[0]
	if err := state.ValidTaskID(id); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	done := fs.Bool("done", false, "report completion")
	pr := fs.String("pr", "", "PR URL, required with --done")
	blocked := fs.String("blocked", "", "report a question the goblin is blocked on")
	failed := fs.String("failed", "", "report a failure reason")
	working := fs.String("working", "", "report what you are working on now")
	waitingOn := fs.String("waiting-on", "", "report what you wait on, another task's ID, overlord, ci or deploy, followed by why")
	var images []string
	fs.Func("image", "a review image for a --blocked question's choice; repeat it once for each choice, in order", func(v string) error {
		images = append(images, v)
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 && (*waitingOn == "" || fs.NArg() != 1) {
		fmt.Fprintln(stderr, "cfo notify: unexpected arguments")
		return 2
	}
	outcomes := 0
	for _, set := range []bool{*done, *blocked != "", *failed != "", *working != "", *waitingOn != ""} {
		if set {
			outcomes++
		}
	}

	var verb, detail string
	switch {
	case outcomes != 1:
		fmt.Fprintln(stderr, "cfo notify: exactly one of --done, --blocked, --failed, --working or --waiting-on is required")
		return 2
	case *done:
		if *pr == "" {
			fmt.Fprintln(stderr, "cfo notify: --done requires --pr <url>")
			return 2
		}
		verb, detail = "done", "PR "+*pr
	case *blocked != "":
		verb, detail = "blocked", *blocked
	case *failed != "":
		verb, detail = "failed", *failed
	case *working != "":
		verb, detail = "working", *working
	default:
		target := *waitingOn
		if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" || target != "overlord" && target != "ci" && target != "deploy" && (state.ValidTaskID(target) != nil || target == id) {
			fmt.Fprintln(stderr, "cfo notify: --waiting-on takes another task's ID, overlord, ci or deploy, then why: --waiting-on <task-id|overlord|ci|deploy> \"<why>\"")
			return 2
		}
		verb, detail = "waiting on "+target, fs.Arg(0)
	}

	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	line := verb + ": " + state.NormalizeStatusDetail(detail)
	// Images are checked before anything is recorded, so a bad path fails the
	// whole notify instead of waking the CFO with a question the Overlord
	// cannot see.
	if len(images) > 0 {
		_, options, _ := wake.Question(wake.Record{Kind: "notify", Detail: line})
		if verb != "blocked" || len(images) != len(options) {
			fmt.Fprintf(stderr, "cfo notify: --image needs a --blocked question with choices, one image for each choice in order (%d images, %d choices)\n", len(images), len(options))
			return 2
		}
		if images, err = supervisor.ReviewImages(h, id, images); err != nil {
			fmt.Fprintln(stderr, "cfo notify: "+err.Error())
			return 1
		}
	}
	if err := state.AppendStatus(h.State, id, line); err != nil {
		fmt.Fprintln(stderr, "cfo notify: record status: "+err.Error())
		return 1
	}
	if verb == "working" || strings.HasPrefix(verb, "waiting on ") && verb != "waiting on overlord" {
		fmt.Fprintf(stdout, "notified %s %s\n", id, line)
		return 0
	}
	record, err := wake.Append(h.State, "notify", id, line)
	if err != nil {
		fmt.Fprintln(stderr, "cfo notify: wake the CFO: "+err.Error())
		return 1
	}
	if _, err := wake.PublishEpisode(h.State); err != nil {
		fmt.Fprintln(stderr, "cfo notify: publish recovery episode: "+err.Error())
		return 1
	}
	// The CFO is already woken; the Command Center copy is a second route
	// to the Overlord, so its failure is reported and never fails the notify.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &herdr.Client{Commands: execx.OSRunner{}}
	if verb == "waiting on overlord" {
		// A wait on the Overlord is an item for him until he answers or
		// clears it, or the goblin reports again.
		if err := supervisor.PublishReview(ctx, h, client, id, fmt.Sprintf("waiting-%s-%d", id, record.Seq), "Waiting on you: "+state.NormalizeStatusDetail(detail), "", nil); err != nil {
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this wait, the CFO still has it: "+err.Error())
		}
	} else if err := supervisor.SurfaceNotify(ctx, h.State, client, id, record, images); err != nil {
		fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this question, the CFO still has it: "+err.Error())
	}
	fmt.Fprintf(stdout, "notified %s %s\n", id, line)
	return 0
}
