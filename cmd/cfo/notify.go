package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/startwatch"
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
//	cfo notify <task-id> --waiting-on <task-id|run-id|overlord|ci|deploy|memory> "<why>"
//	cfo notify <task-id> --waiting-on overlord "<why>" --lavish <html-file>
//	cfo notify <task-id> --waiting-on overlord "<why>" --link <https-url>
//	cfo notify <task-id> --waiting-on overlord "<why>" --run <command.ps1|command.sh>
//	cfo notify <task-id> --waiting-on overlord "<why>" --follows <question-id> | --separate
//	cfo notify <task-id> --blocked "<question> options: <answer> | <answer>" --message-file <message.txt>
//
// While a decision for the goblin is open, its own question with choices or
// the CFO's question for it (cfo question --for), a wait on the Overlord
// declares what it is: --follows <question-id> when it is that decision, so it
// is hidden while the decision is open and closed by its answer, or
// --separate when it is a card of its own, as a wait that opens a page, a
// link, a file it delivered or a command always is. One that declares
// neither is refused, recording nothing, and names each open decision;
// nothing ever guesses it.
//
// A wait or a question whose answer is a command the Overlord must run, such
// as a sign-in, names the command's file with --run: his Command Center shows
// the exact command on a run card named for the goblin, and one click runs it
// in a terminal on that card, where he types into it. A .ps1 file runs in
// Windows PowerShell and a .sh file in Git Bash, in the goblin's worktree,
// never elevated; the goblin is told how it ended.
//
// A question with choices, or a command, can also carry a message the
// Overlord sends himself, such as an email, read from --message-file: its
// card shows the body with Copy, so he never opens a file. A question
// with choices carries it; a command's run card carries it otherwise.
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
// wait on another task, a gate run, CI, a deploy or memory, are status for
// the board. A wait on a gate run's ID makes that no-mistakes run the
// goblin's gate, such as one it started outside its worktree, whose running
// step is its work. A wait that names a Lavish page puts the page on its
// card, and the supervisor polls it: the Overlord's feedback there goes to
// the CFO, never to a poll of the goblin's own. While AFK mode is on the same
// wait is the goblin's question to the CFO, which answers the page in his
// place with cfo answer; the goblin waits for its answer as for his.
//
// A helper reports to its parent instead: its done, which needs no pull
// request, its question and its failure are typed into its parent's
// terminal, and wake the CFO only when that terminal cannot take them.
func runNotify(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
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
	waitingOn := fs.String("waiting-on", "", "report what you wait on, another task's ID, a gate run's ID, overlord, ci, deploy or memory, followed by why. For overlord, lead with one sentence; values he must enter somewhere go in a Markdown table on the lines after it, a header row, a separator row and one row each (\"| Type | Name |\", \"| --- | --- |\", \"| CNAME | `mcp` |\"), each value in backticks so his card copies it with one click")
	lavish := fs.String("lavish", "", "with --waiting-on overlord, the HTML file of the Scrawl page the Overlord answers on")
	link := fs.String("link", "", "with --waiting-on overlord, the https link the Overlord goes to, which his card opens; an address only named in the text is never opened")
	run := fs.String("run", "", "with --waiting-on overlord or --blocked, a .ps1 or .sh file holding a command the Overlord must run, such as a sign-in: his card shows the exact command and runs it with one click in a terminal on his card, where he types into it")
	follows := fs.String("follows", "", "with --waiting-on overlord, the question ID of a decision open for you, your own question with choices or the CFO's question for you, when this wait is that decision: it is hidden while the decision is open and closed by its answer; one of --follows or --separate is required while a decision for you is open")
	separate := fs.Bool("separate", false, "with --waiting-on overlord, this wait is a card of its own, not a decision open for you, as a wait with --lavish, --link or --run, or one beside a file you delivered, always is; one of --follows or --separate is required while a decision for you is open")
	messageFile := fs.String("message-file", "", "with a --blocked question with choices or with --run, "+messageFileUsage)
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
		// Only a helper is done without a pull request; whether this task
		// is one is read with its record below.
		verb = "done"
		if *pr != "" {
			detail = "PR " + *pr
		}
	case *blocked != "":
		verb, detail = "blocked", *blocked
	case *failed != "":
		verb, detail = "failed", *failed
	case *working != "":
		verb, detail = "working", *working
	default:
		target := *waitingOn
		if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" || target != "overlord" && target != "ci" && target != "deploy" && target != "memory" && (state.ValidTaskID(target) != nil || target == id) {
			fmt.Fprintln(stderr, "cfo notify: --waiting-on takes another task's ID, a gate run's ID, overlord, ci, deploy or memory, then why: --waiting-on <task-id|run-id|overlord|ci|deploy|memory> \"<why>\"")
			return 2
		}
		verb, detail = "waiting on "+target, positional[0]
	}
	// Only a question, a failure and a wait on the Overlord wake the CFO, so
	// a question with choices in any other report reaches nobody, and the
	// goblin waits on it unseen (#984).
	if (verb == "working" || strings.HasPrefix(verb, "waiting on ") && verb != "waiting on overlord") && wake.OffersChoices(detail) {
		fmt.Fprintf(stderr, "cfo notify: this report asks a question with choices, and %s wakes nobody; ask it as cfo notify %s --blocked \"<one short question> options: <answer> (Recommended) | <answer>\", or, for a choice you can undo, take it and say which with --working\n", verb, id)
		return 2
	}
	// The record joins the question's lines, so its choices are put last
	// while its line breaks still show where they end.
	if verb == "blocked" || verb == "failed" {
		var err error
		if detail, err = wake.ChoicesLast(detail); err != nil {
			fmt.Fprintf(stderr, "cfo notify: %v, as in: cfo notify %s --%s \"<one short question> options: <answer> (Recommended) | <answer>\"\n", err, id, verb)
			return 2
		}
	}
	if _, options, asked := wake.Question(wake.Record{Kind: "notify", Detail: verb + ": " + detail}); asked {
		if err := choicesAreAnswers(options); err != nil {
			fmt.Fprintln(stderr, "cfo notify: "+err.Error())
			return 2
		}
	}
	// Only a wait on the Overlord says whether it is a decision open for him,
	// and only one that opens nothing can be that decision.
	if *follows != "" || *separate {
		switch {
		case verb != "waiting on overlord":
			fmt.Fprintln(stderr, "cfo notify: --follows and --separate go with --waiting-on overlord: they say whether the wait is a decision open for him or a card of its own")
			return 2
		case *follows != "" && *separate:
			fmt.Fprintln(stderr, "cfo notify: --follows and --separate do not go together: the wait is that decision or a card of its own")
			return 2
		case *follows != "" && (*lavish != "" || *link != "" || *run != ""):
			fmt.Fprintln(stderr, "cfo notify: --follows goes with a wait that opens nothing: a wait with --lavish, --link or --run is a card of its own, so give --separate instead")
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
		if err := supervisor.CheckRunCommand(runShell, runCommand); err != nil {
			fmt.Fprintf(stderr, "cfo notify: --run %s: %v\n", *run, err)
			return 2
		}
	}
	// The message is read before anything is recorded, for the card that
	// shows it: the question's when it offers choices, else the run card's.
	var questionMail, runMail *supervisor.Mail
	if *messageFile != "" {
		_, options, _ := wake.Question(wake.Record{Kind: "notify", Detail: verb + ": " + detail})
		isQuestionCard := verb == "blocked" && len(options) > 0
		if !isQuestionCard && runCommand == "" {
			fmt.Fprintln(stderr, "cfo notify: --message-file goes with a --blocked question with choices or with --run: it puts the message on that card. For a step only he takes, such as sending an email, file cfo auth request --message-file")
			return 2
		}
		mail, err := supervisor.ReadMail(*messageFile)
		if err != nil {
			fmt.Fprintf(stderr, "cfo notify: --message-file %v\n", err)
			return 2
		}
		if isQuestionCard {
			questionMail = mail
		} else {
			runMail = mail
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

	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// While a decision for the goblin is open the Overlord sees only that
	// decision, so a wait on him declares whether it is that decision before
	// anything is recorded: a need he never sees stalls the goblin, and an
	// extra card does not, so nothing guesses it.
	if verb == "waiting on overlord" {
		decisions, err := supervisor.OpenDecisions(h.State, id)
		if err != nil {
			fmt.Fprintln(stderr, "cfo notify: the decisions open for you cannot be read, so this wait gets a card of its own: "+err.Error())
			*follows = ""
		}
		var open strings.Builder
		for _, q := range decisions {
			first, _, _ := strings.Cut(q.Text, "\n")
			fmt.Fprintf(&open, "  %s: %s\n", q.ID, strings.TrimSpace(first))
		}
		switch isOpen := slices.ContainsFunc(decisions, func(q supervisor.Question) bool { return q.ID == *follows }); {
		case *follows != "" && !isOpen && len(decisions) == 0:
			fmt.Fprintf(stderr, "cfo notify: --follows %s names no decision open for %s, and none is, so this wait is a card of its own: leave --follows out\n", *follows, id)
			return 2
		case *follows != "" && !isOpen:
			fmt.Fprintf(stderr, "cfo notify: --follows %s names no decision open for %s. The open ones are:\n%sGive --follows with one of those IDs if this wait is that decision, or --separate if it is a card of its own.\n", *follows, id, open.String())
			return 2
		case *follows == "" && !*separate && len(decisions) > 0:
			fmt.Fprintf(stderr, "cfo notify: a decision for %s is open for the Overlord, so say what this wait is:\n%sGive --follows <id> if this wait is that decision: it is hidden while the decision is open and closed by its answer. Give --separate if it is a card of its own.\n", id, open.String())
			return 2
		}
		// A wait beside a file the goblin delivered opens that file, as a
		// page or a link does, so it is a card of its own.
		if *follows != "" {
			file, err := supervisor.OpenDeliveredFile(h.State, id)
			if err != nil {
				fmt.Fprintln(stderr, "cfo notify: your delivered files cannot be read, so this wait gets a card of its own: "+err.Error())
				*follows = ""
			} else if file != "" {
				fmt.Fprintf(stderr, "cfo notify: your delivered file %s is open, and a wait beside it opens that file, so it is a card of its own: give --separate instead of --follows\n", file)
				return 2
			}
		}
	}
	// While AFK mode is on the CFO answers a goblin's page in the Overlord's
	// place: the wait is the goblin's question to the CFO, with the choices
	// the page declares, and never an item that waits on him.
	if switched, err := afk.Read(h.State); page != "" && err == nil && switched.On {
		verb, detail = "blocked", detail+supervisor.PageOptions(page)
	}
	parent := ""
	if meta, err := state.ReadTaskMeta(h.State, id); err == nil {
		parent = meta.Parent
	}
	if verb == "done" && *pr == "" {
		if parent == "" {
			fmt.Fprintln(stderr, "cfo notify: --done requires --pr <url>")
			return 2
		}
		detail = "ready for " + parent + " to merge"
	}
	line := verb + ": " + state.NormalizeStatusDetail(detail)
	if *link != "" {
		line += " (link " + *link + ")"
	}
	if *run != "" {
		line += " (runs " + filepath.Base(*run) + ")"
	}
	// A done or failed report names what the goblin left running, so the
	// CFO reads it where it learns the goblin finished, and the goblin is
	// told to end what it no longer needs.
	if (verb == "done" || verb == "failed") && runtime.leftRunning != nil {
		switch left, err := runtime.leftRunning(h, id); {
		case err != nil:
			line += " (what it left running could not be read: " + state.NormalizeStatusDetail(err.Error()) + ")"
		case len(left) > 0:
			named := state.NormalizeStatusDetail(startwatch.Named(left))
			line += " (left running: " + named + ")"
			defer fmt.Fprintf(stdout, "You left running %s. End each you no longer need by its pid now, with Stop-Process -Id <pid>, or taskkill /PID <pid> /T /F to end one with what runs under it, and never by its name.\n", named)
		}
	}
	// A helper's question goes to its parent's terminal, where no card shows
	// a message.
	if *messageFile != "" && page == "" && helperReport(id, parent, verb, line) != "" {
		fmt.Fprintf(stderr, "cfo notify: --message-file puts a message on a card, and %s's report goes to its parent %s's terminal instead\n", id, parent)
		return 2
	}
	// Images are checked before anything is recorded, so a bad path fails the
	// whole notify instead of waking the CFO with a question the Overlord
	// cannot see.
	if len(images) > 0 {
		_, options, _ := wake.Question(wake.Record{Kind: "notify", Detail: line})
		if *blocked == "" || len(images) != len(options) {
			fmt.Fprintf(stderr, "cfo notify: --image needs a --blocked question with choices, one image for each choice in order (%d images, %d choices)\n", len(images), len(options))
			return 2
		}
		if images, err = supervisor.ReviewImages(h, id, images); err != nil {
			fmt.Fprintln(stderr, "cfo notify: "+err.Error())
			return 1
		}
	}
	// A wait beside the goblin's own open step card is that card, whose close
	// the goblin hears through its own wait on it: a second card would ask him
	// for the same step again. A wait on a page is its own card, since only its
	// card gets the page polled.
	stepCard := ""
	if verb == "waiting on overlord" && runCommand == "" && page == "" {
		if stepCard, err = supervisor.OpenStepCard(h.State, id); err != nil {
			fmt.Fprintln(stderr, "cfo notify: your open step cards cannot be read, so none stands for this wait: "+err.Error())
		}
	}
	// A card in his Command Center asks him something or lets him act, so a
	// wait on him that names nothing he acts on, no page, link, command, step
	// card, delivered file or open decision, gets none: it reaches the CFO
	// alone, which says so on its line. On 2026-10-18 he met such waits as
	// cards with nothing to press but Skip.
	isBare := false
	if verb == "waiting on overlord" && runCommand == "" && page == "" && *link == "" && *follows == "" && stepCard == "" {
		file, err := supervisor.OpenDeliveredFile(h.State, id)
		if err != nil {
			fmt.Fprintln(stderr, "cfo notify: your delivered files cannot be read, so none stands for this wait: "+err.Error())
		}
		isBare = file == ""
	}
	if isBare {
		line += " (no card: it names nothing he acts on)"
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
	// A helper's page is the Overlord's, or the CFO's in his place, never its
	// parent's to answer.
	if report := helperReport(id, parent, verb, line); report != "" && page == "" {
		err := runtime.sendText(context.Background(), h, parent, report)
		if err == nil || errors.Is(err, fleet.ErrQueuedForToolCall) {
			supervisor.Reported(h.State)
			fmt.Fprintf(stdout, "notified %s's parent %s %s\n", id, parent, line)
			return 0
		}
		fmt.Fprintf(stderr, "cfo notify: %s's terminal did not take the report, so the CFO is told instead: %v\n", parent, err)
		line += "; it could not reach its parent " + parent + ": " + state.NormalizeStatusDetail(err.Error())
	}
	// A pull request's head is reported done once: a goblin asked to wrap up
	// often reports the same finished head again, sometimes with more words
	// after its link. The head is the pull request's own, read from GitHub,
	// never the task worktree's HEAD: a task's worktree holds one branch, so
	// its HEAD stays put while a pull request from another branch or worktree
	// is pushed, and every later report of it would look like a repeat.
	var record wake.Record
	isNew := true
	head, prLink := "", ""
	if verb == "done" && *pr != "" {
		prLink, _, _ = strings.Cut(strings.TrimSpace(*pr), " ")
		var headErr error
		if head, headErr = notifyPRHead(runtime, prLink); headErr != nil {
			fmt.Fprintf(stderr, "cfo notify: %s's pull request head could not be read (%v), so this done is reported without deduplication\n", prLink, headErr)
		}
	}
	switch {
	case head != "":
		record, isNew, err = wake.AppendFirst(h.State, "done/"+id+"/"+prLink+"@"+head, "notify", id, line)
	case verb == "blocked" || verb == "failed":
		record, isNew, err = wake.AppendDecision(h.State, id, line)
	default:
		record, err = wake.Append(h.State, "notify", id, line)
	}
	if err != nil {
		fmt.Fprintln(stderr, "cfo notify: wake the CFO: "+err.Error())
		return 1
	}
	if !isNew && verb == "done" {
		fmt.Fprintf(stdout, "%s already reported %s done at %s, so the CFO was not woken again\n", id, *pr, head)
		return 0
	}
	if !isNew {
		fmt.Fprintf(stdout, "%s already asked this as notify %d, which still waits for the CFO, so the CFO was not woken again\n", id, record.Seq)
		return 0
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
		if err := supervisor.PublishGoblinRun(h, id, record.Seq, why, runShell, runCommand, runMail); err != nil {
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this command, the CFO still has it: "+err.Error())
		}
	}
	switch {
	case verb == "waiting on overlord" && runCommand != "":
		// Its run card is the wait.
	case verb == "waiting on overlord" && stepCard != "":
		// Its step card is the wait.
	case verb == "waiting on overlord" && isBare:
		// The CFO has it, and nothing goes to his Command Center.
	case verb == "waiting on overlord":
		if err := supervisor.PublishWait(h, id, record.Seq, strings.TrimSpace(detail), pageURL, page, *link, *follows); err != nil {
			if errors.Is(err, supervisor.ErrPageHeld) {
				fmt.Fprintf(stderr, "cfo notify: the Command Center cannot show this wait (%v); the CFO has the wait, so wait on the CFO for his answer\n", err)
				return 1
			}
			if page != "" {
				fmt.Fprintf(stderr, "cfo notify: the Command Center cannot show this wait (%v), so nothing watches the page %s and the Overlord's answer on it reaches nobody; the CFO has the wait, ask in text with --blocked instead\n", err, page)
				return 1
			}
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this wait, the CFO still has it: "+err.Error())
		}
	default:
		if err := supervisor.SurfaceNotify(h.State, id, record, verb+": "+strings.TrimSpace(detail), pageURL, page, images, questionMail); err != nil {
			fmt.Fprintln(stderr, "cfo notify: the Command Center cannot show this question, the CFO still has it: "+err.Error())
		}
	}
	// The board shows the report now, not at its next refresh.
	supervisor.Reported(h.State)
	fmt.Fprintf(stdout, "notified %s %s\n", id, line)
	if stepCard != "" {
		fmt.Fprintf(stdout, "Your step card %s is open, so it stands for this wait in the Command Center and no second card was opened. Wait on it in the background with cfo auth request --wait-on %s --task %s, which ends when the card closes. If you wait on him for something that card does not ask, ask it once the card closes.\n", stepCard, stepCard, id)
	}
	if isBare {
		fmt.Fprintf(stdout, "No card went to the Overlord's Command Center: this wait names nothing he acts on there, so the CFO alone has it. To put the step itself in front of him, notify again with --run <command.ps1|command.sh> for a command he runs, --lavish <page.html> for a page he answers, or --link <https-url> for the page he takes it on, or file cfo auth request for a sign-in, a value or another step only he takes. A decision goes to the CFO: cfo notify %s --blocked \"<question>\".\n", id)
	}
	// While AFK mode is on nothing prompts the Overlord, so a goblin that
	// waits on him is told to move to what does not depend on him.
	if switched, err := afk.Read(h.State); verb == "waiting on overlord" && err == nil && switched.On {
		fmt.Fprintf(stdout, "AFK mode is on: the Overlord is away until he turns it off and nothing is asked of him, so the CFO leaves this for him in the backlog. If any of your work does not depend on it, move to that next piece now and report it with cfo notify %s --working \"<what>\".\n", id)
	}
	return 0
}

// helperReport is what a helper's report says in its parent's terminal,
// with the command that acts on it, on one line so the terminal takes it
// whole; empty for a task that is no helper and for a report only the
// board shows.
func helperReport(id, parent, verb, line string) string {
	_, said, _ := strings.Cut(line, ": ")
	switch {
	case parent == "":
		return ""
	case verb == "done":
		return "Your helper " + id + " is done: its work is committed on its branch, " + said + ". Merge it into your branch with: cfo helper merge " + parent
	case verb == "blocked":
		return "Your helper " + id + " asks: " + said + " Answer it with: cfo send " + id + " \"<answer>\""
	case verb == "failed":
		return "Your helper " + id + " failed: " + said + ". Tell it what to do with: cfo send " + id + " \"<what>\", or stop it with: cfo kill " + id + " --reason \"<why>\""
	}
	return ""
}

// notifyHeadReadTimeout bounds reading a pull request's head from GitHub, so
// an unreachable gh wakes the CFO without deduplication instead of holding the
// report up.
const notifyHeadReadTimeout = 20 * time.Second

// notifyPRHead reads a pull request's own head commit with the runtime's gh
// runner, so a done report is keyed by the head that was pushed rather than by
// the task worktree's HEAD.
func notifyPRHead(runtime commandRuntime, url string) (string, error) {
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyHeadReadTimeout)
	defer cancel()
	return readPRHead(ctx, commands, url)
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
