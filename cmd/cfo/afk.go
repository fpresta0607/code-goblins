package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const afkUsage = `usage: cfo afk on [--asked "<his words>"] | off [--asked "<his words>"] | status | report | log --kind <kind> --what "<what>" --evidence "<evidence>" [--link <url>] | strike --at <when> --reason "<why>"

  on      turn AFK mode on: the Supreme Overlord's switch, made from a terminal of his own; the registered CFO makes it only at his ask, with --asked and his words quoted exactly, and a goblin never
  off     turn it off and print the report of the stretch; the same switch, made the same way
  status  whether it is on, since when and from where, what was decided so far and what is held for him
  report  print the report of the last stretch that ended
  log     the registered CFO logs a decision it made under the authority, with its evidence; kind is one of left, merge, deploy, migration, install, answer, other. A left line also takes --diagnosis "<what is wrong, found to its cause>", --tried "<what you already tried>" and two or more --option "<choice>" with an optional --recommend "<choice>": his Command Center question once he is back
  strike  the registered CFO strikes through a line it logged by mistake, named by its at in state/afk.audit, with its reason; the line stays, and the report shows it struck`

// runAFK is cfo afk: the Overlord's switch for running the fleet while he is
// away, what was decided and held under it, and the report it ends with. The
// supervisor decides who may switch and who may log; this command only asks.
func runAFK(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	usage := func() int {
		fmt.Fprintln(stderr, afkUsage)
		return 2
	}
	if len(args) == 0 || !slices.Contains([]string{"on", "off", "status", "report", "log", "strike"}, args[0]) {
		return usage()
	}
	if (args[0] == "status" || args[0] == "report") && len(args) != 1 {
		return usage()
	}
	// Only a switch takes words, and only the CFO's: his own takes none.
	asked := ""
	if args[0] == "on" || args[0] == "off" {
		var ok bool
		if asked, ok = hisAsk(args[0], args[1:], stderr); !ok {
			return 2
		}
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "cfo afk: "+err.Error())
		return 1
	}
	switch args[0] {
	case "on":
		before, err := afk.Read(h.State)
		if err != nil {
			return fail(err)
		}
		if err := runtime.afkSwitch()(h, true, asked); err != nil {
			return fail(err)
		}
		switched, err := afk.Read(h.State)
		if err != nil {
			return fail(err)
		}
		is := "is"
		if before.On {
			is = "was already"
		}
		fmt.Fprintf(stdout, "AFK mode %s on since %s, turned on %s.\n", is, switched.Since.UTC().Format("2006-01-02 15:04 UTC"), afk.SwitchedBy(switched.From, switched.Asked))
		if asked != "" {
			// The CFO reads this, having made the switch at his ask.
			fmt.Fprintln(stdout, "Say in your reply to him that AFK mode is on. You decide everything its authority covers and log each decision. What only he can do gets a backlog row and a cfo afk log --kind left line, and the work goes around it.")
			fmt.Fprintln(stdout, "His own switch, on the board or in a terminal of his own, turns it off at any time. cfo afk status shows what was decided and held.")
			return 0
		}
		fmt.Fprintln(stdout, "The CFO decides everything its authority covers and logs each decision. What only you can do waits in the backlog and in your report.")
		fmt.Fprintln(stdout, "cfo afk status shows both. cfo afk off turns it off and prints the report.")
		return 0
	case "off":
		// His off resets a switch that cannot be read, and then no stretch
		// ended that a report could be of.
		_, unread := afk.Read(h.State)
		if err := runtime.afkSwitch()(h, false, asked); err != nil {
			return fail(err)
		}
		if unread != nil {
			fmt.Fprintln(stdout, "AFK mode's switch could not be read and is reset to off. There is no report of the stretch it may have held: state/afk.audit keeps what was logged.")
			return 0
		}
		return printAFKReport(h, stdout, stderr)
	case "report":
		return printAFKReport(h, stdout, stderr)
	case "log":
		return runAFKLog(h, args[1:], stdout, stderr, runtime)
	case "strike":
		return runAFKStrike(h, args[1:], stdout, stderr, runtime)
	}
	return afkStatus(h, stdout, stderr)
}

// hisAsk reads the words a switch carries: none when the Overlord makes it
// himself, and --asked with his words when the registered CFO makes it at his
// ask. It reports whether the arguments were ones the switch takes.
func hisAsk(name string, args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet("afk "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asked := fs.String("asked", "", "the Overlord's own words asking for the switch, quoted exactly; the registered CFO passes them when it makes the switch at his ask")
	if err := fs.Parse(args); err != nil {
		return "", false
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "cfo afk %s: unexpected argument %q; quote his words after --asked\n", name, fs.Arg(0))
		return "", false
	}
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "asked" })
	if !given {
		return "", true
	}
	words, err := afk.HisWords(*asked)
	if err != nil {
		fmt.Fprintf(stderr, "cfo afk %s: --asked takes the Overlord's own words asking for it: %v\n", name, err)
		return "", false
	}
	return words, true
}

// afkSwitch is how the command asks for the switch: over the supervisor's
// pipe, unless the runtime names another way. asked is empty for the
// Overlord's own switch, and his words for one the CFO makes at his ask.
func (r commandRuntime) afkSwitch() func(h home.Home, on bool, asked string) error {
	if r.switchAFK != nil {
		return r.switchAFK
	}
	return func(h home.Home, on bool, asked string) error {
		if asked == "" {
			return supervisor.SwitchAFK(h, on)
		}
		return supervisor.SwitchAFKAtHisAsk(h, on, asked)
	}
}

// printAFKReport prints the report of the last stretch that ended, with each
// item it held as it stands now.
func printAFKReport(h home.Home, stdout, stderr io.Writer) int {
	report, found, err := supervisor.ReadAFKReport(h)
	if err != nil {
		fmt.Fprintln(stderr, "cfo afk: "+err.Error())
		return 1
	}
	if !found {
		fmt.Fprintln(stdout, "No stretch of AFK mode has ended in this home yet, so there is no report.")
		return 0
	}
	if err := afk.Render(stdout, report); err != nil {
		fmt.Fprintln(stderr, "cfo afk: "+err.Error())
		return 1
	}
	return 0
}

// runAFKLog sends one decision to AFK mode's log.
func runAFKLog(h home.Home, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("afk log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "what kind of decision: "+strings.Join(afk.DecisionKinds, ", "))
	what := fs.String("what", "", "what was decided: the deploy, the migration, the build installed")
	evidence := fs.String("evidence", "", "what the decision stands on: what you ran and what you read")
	link := fs.String("link", "", "a link to it")
	diagnosis := fs.String("diagnosis", "", "for a left line: what is wrong, found to its cause")
	tried := fs.String("tried", "", "for a left line: what you already tried")
	var options []string
	fs.Func("option", "for a left line: one choice his Command Center question offers once he is back; repeat for each", func(value string) error { options = append(options, value); return nil })
	recommend := fs.String("recommend", "", "for a left line: the choice you recommend, exactly as one --option")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	left := *kind == afk.KindLeft
	switch {
	case fs.NArg() != 0:
		fmt.Fprintf(stderr, "cfo afk log: unexpected argument %q; quote what was decided and its evidence\n", fs.Arg(0))
		return 2
	case !left && (*diagnosis != "" || *tried != "" || len(options) > 0 || *recommend != ""):
		fmt.Fprintln(stderr, "cfo afk log: --diagnosis, --tried, --option and --recommend go with --kind left")
		return 2
	case left && (strings.TrimSpace(*diagnosis) == "" || strings.TrimSpace(*tried) == "" || len(options) < 2):
		fmt.Fprintln(stderr, "cfo afk log: a left line says what is wrong with --diagnosis, what you already tried with --tried, and offers him two or more --option choices: nothing reaches him undiagnosed")
		return 2
	case !slices.Contains(afk.DecisionKinds, *kind):
		fmt.Fprintln(stderr, "cfo afk log: --kind is one of "+strings.Join(afk.DecisionKinds, ", "))
		return 2
	case strings.TrimSpace(*what) == "":
		fmt.Fprintln(stderr, "cfo afk log: --what names what was decided")
		return 2
	case strings.TrimSpace(*evidence) == "":
		fmt.Fprintln(stderr, "cfo afk log: --evidence says what the decision stands on; a decision is logged with its evidence")
		return 2
	}
	entry := afk.Entry{Kind: *kind, What: strings.TrimSpace(*what), Evidence: strings.TrimSpace(*evidence), Link: strings.TrimSpace(*link), Diagnosis: strings.TrimSpace(*diagnosis), Tried: strings.TrimSpace(*tried), Options: options, Recommendation: *recommend}
	if err := runtime.afkLog()(h, entry); err != nil {
		fmt.Fprintln(stderr, "cfo afk log: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "logged %s: %s\n", *kind, strings.TrimSpace(*what))
	return 0
}

// runAFKStrike sends the strike of one line the CFO logged by mistake.
func runAFKStrike(h home.Home, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	fs := flag.NewFlagSet("afk strike", flag.ContinueOnError)
	fs.SetOutput(stderr)
	at := fs.String("at", "", "the line's at, as state/afk.audit has it, such as 2026-10-09T00:08:18.430129Z")
	reason := fs.String("reason", "", "why the line is struck: the report shows it beside the line")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	when, err := time.Parse(time.RFC3339Nano, *at)
	switch {
	case fs.NArg() != 0:
		fmt.Fprintf(stderr, "cfo afk strike: unexpected argument %q; quote the reason\n", fs.Arg(0))
		return 2
	case err != nil:
		fmt.Fprintln(stderr, "cfo afk strike: --at names the line by its at, as state/afk.audit has it, such as 2026-10-09T00:08:18.430129Z")
		return 2
	case strings.TrimSpace(*reason) == "":
		fmt.Fprintln(stderr, "cfo afk strike: --reason says why the line is struck; a line is never struck without one")
		return 2
	}
	if err := runtime.afkStrike()(h, when, strings.TrimSpace(*reason)); err != nil {
		fmt.Fprintln(stderr, "cfo afk strike: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "struck the line logged at %s: the log keeps it, and the report shows it struck\n", when.UTC().Format(time.RFC3339Nano))
	return 0
}

// afkStrike is how the command strikes a line: over the supervisor's pipe,
// unless the runtime names another way.
func (r commandRuntime) afkStrike() func(home.Home, time.Time, string) error {
	if r.strikeAFK != nil {
		return r.strikeAFK
	}
	return supervisor.StrikeAFKLine
}

// afkStatus prints the switch and, while it is on, its terms, what the CFO
// decided so far and what is held for the Overlord. A question the CFO
// answered is a decision, so it is not listed as held.
func afkStatus(h home.Home, stdout, stderr io.Writer) int {
	switched, err := afk.Read(h.State)
	if err != nil {
		fmt.Fprintln(stderr, "cfo afk: "+err.Error())
		return 1
	}
	if !switched.On {
		fmt.Fprintln(stdout, "AFK mode is off.")
		if switched.Session != "" {
			fmt.Fprintf(stdout, "It was last on from %s to %s; cfo afk report prints that stretch's report.\n", switched.Since.UTC().Format("2006-01-02 15:04 UTC"), switched.Ended.UTC().Format("2006-01-02 15:04 UTC"))
		}
		return 0
	}
	for _, line := range afk.Notice(switched) {
		fmt.Fprintln(stdout, line)
	}
	entries, unreadable, err := afk.Entries(h.State, switched.Session)
	if err != nil {
		fmt.Fprintln(stderr, "cfo afk: "+err.Error())
		return 1
	}
	decisions := afk.Decisions(entries)
	fmt.Fprintf(stdout, "\nDecided so far (%d)\n", len(decisions))
	answered := map[string]bool{}
	for _, decision := range decisions {
		line := "- " + decision.Kind + ": "
		if decision.Task != "" {
			line += decision.Task + ": "
		}
		line += decision.What
		if decision.Outcome != "" {
			line += " (" + decision.Outcome + ")"
		}
		fmt.Fprintln(stdout, line)
		if decision.Kind == afk.KindAnswer {
			answered["question:"+decision.What] = true
		}
	}
	var held []afk.Entry
	for _, entry := range entries {
		if entry.Kind == afk.KindHeld && !answered[entry.Item] {
			held = append(held, entry)
		}
	}
	fmt.Fprintf(stdout, "\nHeld for you so far (%d)\n", len(held))
	for _, entry := range held {
		whose := "the CFO's"
		if entry.Task != "" {
			whose = entry.Task + "'s"
		}
		fmt.Fprintf(stdout, "- %s, %s: %s\n", entry.Item, whose, entry.What)
		if recommends := afk.Recommends(entry.Task, entry.Recommendation, true); recommends != "" {
			fmt.Fprintln(stdout, "  "+recommends)
		}
	}
	if unreadable > 0 {
		fmt.Fprintf(stdout, "\n%d line(s) of the log (state/afk.audit) could not be read.\n", unreadable)
	}
	return 0
}
