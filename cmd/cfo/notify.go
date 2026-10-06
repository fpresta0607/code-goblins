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

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
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
//	cfo notify <task-id> --blocked "<question> options: <answer> (Recommended) | <answer>" --image a.png --image b.png
//	cfo notify <task-id> --failed "<reason>"
//	cfo notify <task-id> --working "<what>"
//	cfo notify <task-id> --waiting-on <task-id|overlord|ci|deploy|memory> "<why>"
//	cfo notify <task-id> --waiting-on overlord "<why>" --lavish <html-file>
//	cfo notify <task-id> --waiting-on overlord "<why>" --link <https-url>
//	cfo notify <task-id> --waiting-on overlord "<why>" --run <command.ps1|command.sh>
//
// A wait or a question whose answer is a command the Overlord must run, such
// as a sign-in, names the command's file with --run: his Command Center shows
// the exact command on a run card named for the goblin, and one click runs it
// in a window that stays open and usable. A .ps1 file runs in Windows
// PowerShell and a .sh file in Git Bash, in the goblin's worktree, never
// elevated; the goblin is told how it ended.
//
// A wait on the Overlord leads with one sentence, and the values he must
// enter somewhere follow it as a Markdown table, each value in backticks:
//
//	cfo notify <task-id> --waiting-on overlord "Add these DNS records in **Cloudflare**, then tell me
//	| Type | Name | Content |
//	| --- | --- | --- |
//	| CNAME | `mcp` | `mcp-precisiondocs.fly.dev` |" --link https://dash.cloudflare.com
//
// Only a question and a wait on the Overlord wake the CFO: working, and a
// wait on another task, CI, a deploy or memory, are status for the board. A wait that
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
	blocked := fs.String("blocked", "", "report a question the goblin is blocked on: one short sentence that is the question, details on lines starting with \"- \", and **bold** only on the verdict or the blocking item")
	failed := fs.String("failed", "", "report a failure reason")
	working := fs.String("working", "", "report what you are working on now")
	waitingOn := fs.String("waiting-on", "", "report what you wait on, another task's ID, overlord, ci, deploy or memory, followed by why. For overlord, lead with one sentence; values he must enter somewhere go in a Markdown table on the lines after it, a header row, a separator row and one row each (\"| Type | Name |\", \"| --- | --- |\", \"| CNAME | `mcp` |\"), each value in backticks so his card copies it with one click")
	lavish := fs.String("lavish", "", "with --waiting-on overlord, the HTML file of the Scrawl page the Overlord answers on")
	link := fs.String("link", "", "with --waiting-on overlord, the https link the Overlord goes to, which his card opens; an address only named in the text is never opened")
	run := fs.String("run", "", "with --waiting-on overlord or --blocked, a .ps1 or .sh file holding a command the Overlord must run, such as a sign-in: his card shows the exact command and runs it with one click in a window he can use")
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
		if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" || target != "overlord" && target != "ci" && target != "deploy" && target != "memory" && (state.ValidTaskID(target) != nil || target == id) {
			fmt.Fprintln(stderr, "cfo notify: --waiting-on takes another task's ID, overlord, ci, deploy or memory, then why: --waiting-on <task-id|overlord|ci|deploy|memory> \"<why>\"")
			return 2
		}
		verb, detail = "waiting on "+target, positional[0]
	}
	if _, options, asked := wake.Question(wake.Record{Kind: "notify", Detail: verb + ": " + detail}); asked {
		if err := choicesAreAnswers(options); err != nil {
			fmt.Fprintln(stderr, "cfo notify: "+err.Error())
			return 2
		}
	}
	// The page is checked and opened before anything is recorded, so a page
	// that cannot be shown fails the notify instead of leaving a wait on a
	// card the Overlord cannot answer.
	if *link != "" {
		if verb != "waiting on overlord" {
			fmt.Fprintln(stderr, "cfo notify: --link goes with --waiting-on overlord: it names where the Overlord goes")
			return 2
		}
		if problem := supervisor.PresentationURLProblem(*link); problem != "" {
			fmt.Fprintf(stderr, "cfo notify: --link %s\n", problem)
			return 2
		}
	}
	// The command is read before anything is recorded, so a file that
	// cannot be run fails the notify instead of leaving a wait on a card
	// with nothing to run.
	var runShell, runCommand string
	if *run != "" {
		switch {
		case verb != "waiting on overlord" && verb != "blocked":
			fmt.Fprintln(stderr, "cfo notify: --run goes with --waiting-on overlord or --blocked: it names a command the Overlord runs")
			return 2
		case *lavish != "":
			fmt.Fprintln(stderr, "cfo notify: --run and --lavish do not go together: he answers on the page or runs the command")
			return 2
		}
		switch strings.ToLower(filepath.Ext(*run)) {
		case ".ps1":
			runShell = "powershell"
		case ".sh":
			runShell = "bash"
		default:
			fmt.Fprintln(stderr, "cfo notify: --run takes a .ps1 file, which runs in Windows PowerShell, or a .sh file, which runs in Git Bash")
			return 2
		}
		var err error
		if runCommand, err = supervisor.ReadRunCommand(*run); err != nil {
			fmt.Fprintf(stderr, "cfo notify: --run %v\n", err)
			return 2
		}
		if strings.TrimSpace(runCommand) == "" {
			fmt.Fprintf(stderr, "cfo notify: --run %s is empty\n", *run)
			return 2
		}
	}
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
	if *link != "" {
		line += " (link " + *link + ")"
	}
	if *run != "" {
		line += " (runs " + filepath.Base(*run) + ")"
	}
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
		supervisor.Reported(h.State)
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
	// A command rides on its own card, the run card: a wait that carries one
	// is that card alone, and a question keeps its card beside it.
	if runCommand != "" {
		why, _, _ := strings.Cut(strings.TrimSpace(detail), "\n")
		if err := supervisor.PublishGoblinRun(h, id, record.Seq, why, runShell, runCommand); err != nil {
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this command, the CFO still has it: "+err.Error())
		}
	}
	switch {
	case verb == "waiting on overlord" && runCommand != "":
		// Its run card is the wait.
	case verb == "waiting on overlord":
		if err := supervisor.PublishWait(h, id, record.Seq, strings.TrimSpace(detail), pageURL, page, *link); err != nil {
			if page != "" {
				fmt.Fprintf(stderr, "cfo notify: the Command Center cannot show this wait (%v), so nothing watches the page %s and the Overlord's answer on it reaches nobody; the CFO has the wait, ask in text with --blocked instead\n", err, page)
				return 1
			}
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this wait, the CFO still has it: "+err.Error())
		}
	default:
		if err := supervisor.SurfaceNotify(h.State, id, record, verb+": "+strings.TrimSpace(detail), images); err != nil {
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this question, the CFO still has it: "+err.Error())
		}
	}
	// The board shows the report now, not at its next refresh.
	supervisor.Reported(h.State)
	fmt.Fprintf(stdout, "notified %s %s\n", id, line)
	// While AFK mode is on nothing prompts the Overlord, so a goblin that
	// waits on him is told to move to what does not depend on him.
	if switched, err := afk.Read(h.State); verb == "waiting on overlord" && err == nil && switched.On {
		fmt.Fprintf(stdout, "AFK mode is on: the Overlord is away until he turns it off, so this wait is held for him and nothing prompts him. If any of your work does not depend on it, move to that next piece now and report it with cfo notify %s --working \"<what>\".\n", id)
	}
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
