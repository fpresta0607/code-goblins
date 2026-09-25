package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
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
//	cfo notify <task-id> --waiting-on overlord "<why>" --lavish <html-file>
//
// Only a question and a wait on the Overlord wake the CFO: working, and a
// wait on another task, CI or a deploy, are status for the board. A wait that
// names a Lavish page puts the page on its card, and the supervisor polls it:
// the Overlord's feedback there goes to the CFO, never to a poll of the
// goblin's own.
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
	lavish := fs.String("lavish", "", "with --waiting-on overlord, the HTML file of the Lavish page the Overlord answers on")
	var images []string
	fs.Func("image", "a review image for a --blocked question's choice; repeat it once for each choice, in order", func(v string) error {
		images = append(images, v)
		return nil
	})
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	// A wait's reason is its one plain argument, and flags may follow it.
	positional := fs.Args()
	if len(positional) > 0 {
		if err := fs.Parse(positional[1:]); err != nil {
			return 2
		}
		positional = append([]string{positional[0]}, fs.Args()...)
	}
	if len(positional) != 0 && (*waitingOn == "" || len(positional) != 1) {
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
		if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" || target != "overlord" && target != "ci" && target != "deploy" && (state.ValidTaskID(target) != nil || target == id) {
			fmt.Fprintln(stderr, "cfo notify: --waiting-on takes another task's ID, overlord, ci or deploy, then why: --waiting-on <task-id|overlord|ci|deploy> \"<why>\"")
			return 2
		}
		verb, detail = "waiting on "+target, positional[0]
	}
	// The page is checked and opened before anything is recorded, so a page
	// that cannot be shown fails the notify instead of leaving a wait on a
	// card the Overlord cannot answer.
	var page, pageURL string
	if *lavish != "" {
		if verb != "waiting on overlord" {
			fmt.Fprintln(stderr, "cfo notify: --lavish goes with --waiting-on overlord: it names the page the Overlord answers on")
			return 2
		}
		var err error
		if page, err = lavishPageFile(*lavish); err != nil {
			fmt.Fprintf(stderr, "cfo notify: --lavish %v\n", err)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), pageOpenTimeout)
		pageURL, err = (axi.Lavish{Commands: execx.OSRunner{}}).Open(ctx, page)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "cfo notify: lavish-axi cannot show %s (%v); ask in text with --blocked instead\n", page, err)
			return 1
		}
		detail += " (page " + pageURL + ")"
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
	// to the Overlord, so its failure is reported and never fails the notify,
	// except for a page: only its item gets the page polled.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	terminals := terminal.HerdrSessions(&herdr.Client{Commands: execx.OSRunner{}})
	if verb == "waiting on overlord" {
		if err := supervisor.PublishWait(ctx, h, terminals, id, record.Seq, state.NormalizeStatusDetail(detail), pageURL, page); err != nil {
			if page != "" {
				fmt.Fprintf(stderr, "cfo notify: the Command Center cannot show this wait (%v), so nothing watches the page %s and the Overlord's answer on it reaches nobody; the CFO has the wait, ask in text with --blocked instead\n", err, page)
				return 1
			}
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this wait, the CFO still has it: "+err.Error())
		}
	} else if err := supervisor.SurfaceNotify(ctx, h.State, terminals, id, record, images); err != nil {
		fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this question, the CFO still has it: "+err.Error())
	}
	fmt.Fprintf(stdout, "notified %s %s\n", id, line)
	return 0
}

// pageOpenTimeout bounds opening a Lavish page, which starts lavish-axi's
// server on its first run, apart from the budget of whatever follows.
var pageOpenTimeout = 30 * time.Second

// lavishPageFile returns the absolute path of a Lavish page the supervisor can
// poll: an existing HTML file.
func lavishPageFile(file string) (string, error) {
	page, err := filepath.Abs(file)
	if err != nil {
		return "", fmt.Errorf("%s cannot be resolved: %w", file, err)
	}
	extension := strings.ToLower(filepath.Ext(page))
	if info, err := os.Stat(page); err != nil || !info.Mode().IsRegular() || extension != ".html" && extension != ".htm" {
		return "", fmt.Errorf("%s is not an HTML file", page)
	}
	return page, nil
}
