package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const afkUsage = `usage: cfo afk on | off | status | report | log --kind <kind> --what "<what>" --evidence "<evidence>" [--link <url>]

  on      turn AFK mode on: the Supreme Overlord's switch, refused in a goblin's or the CFO's terminal
  off     turn it off and print the report of the stretch; his switch too
  status  whether it is on, since when and from where, what was decided so far and what is held for him
  report  print the report of the last stretch that ended
  log     the registered CFO logs a decision it made under the authority, with its evidence; kind is one of ` + "merge, deploy, migration, install, answer, other"

// runAFK is cfo afk: the Overlord's switch for running the fleet while he is
// away, what was decided and held under it, and the report it ends with. The
// supervisor decides who may switch and who may log; this command only asks.
func runAFK(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	usage := func() int {
		fmt.Fprintln(stderr, afkUsage)
		return 2
	}
	if len(args) == 0 || args[0] != "log" && len(args) != 1 {
		return usage()
	}
	if !slices.Contains([]string{"on", "off", "status", "report", "log"}, args[0]) {
		return usage()
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
		if err := runtime.afkSwitch()(h, true); err != nil {
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
		fmt.Fprintf(stdout, "AFK mode %s on since %s, turned on from %s.\n", is, switched.Since.UTC().Format("2006-01-02 15:04 UTC"), switched.From)
		fmt.Fprintln(stdout, "The CFO decides what its authority covers and logs each decision; what stays yours is held for you without a prompt.")
		fmt.Fprintln(stdout, "cfo afk status shows both. cfo afk off turns it off and prints the report.")
		return 0
	case "off":
		if err := runtime.afkSwitch()(h, false); err != nil {
			return fail(err)
		}
		return printAFKReport(h, stdout, stderr)
	case "report":
		return printAFKReport(h, stdout, stderr)
	case "log":
		return runAFKLog(h, args[1:], stdout, stderr, runtime)
	}
	return afkStatus(h, stdout, stderr)
}

// afkSwitch is how the command asks for the switch: over the supervisor's
// pipe, unless the runtime names another way.
func (r commandRuntime) afkSwitch() func(home.Home, bool) error {
	if r.switchAFK != nil {
		return r.switchAFK
	}
	return supervisor.SwitchAFK
}

// printAFKReport prints the report of the last stretch that ended.
func printAFKReport(h home.Home, stdout, stderr io.Writer) int {
	report, found, err := afk.ReadReport(h.State)
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
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch {
	case fs.NArg() != 0:
		fmt.Fprintf(stderr, "cfo afk log: unexpected argument %q; quote what was decided and its evidence\n", fs.Arg(0))
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
	if err := runtime.afkLog()(h, afk.Entry{Kind: *kind, What: strings.TrimSpace(*what), Evidence: strings.TrimSpace(*evidence), Link: strings.TrimSpace(*link)}); err != nil {
		fmt.Fprintln(stderr, "cfo afk log: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "logged %s: %s\n", *kind, strings.TrimSpace(*what))
	return 0
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
	}
	if unreadable > 0 {
		fmt.Fprintf(stdout, "\n%d line(s) of the log (state/afk.audit) could not be read.\n", unreadable)
	}
	return 0
}

// afkAllowance is quota-axi's reading as AFK mode's report keeps it: every
// window's use and any credit balance of each provider it measured, in
// provider order.
func afkAllowance(report quota.Report) []afk.Allowance {
	var readings []afk.Allowance
	for _, name := range slices.Sorted(maps.Keys(report.Providers)) {
		provider := report.Providers[name]
		// Numbers quota-axi itself calls old are no reading.
		if provider.Stale {
			continue
		}
		for _, window := range provider.Windows {
			label := window.Label
			if label == "" {
				label = window.ID
			}
			readings = append(readings, afk.Allowance{Provider: name, Window: label, PercentUsed: window.PercentUsed, ResetsAt: window.ResetsAt})
		}
		if credits := provider.Credits; credits != nil {
			readings = append(readings, afk.Allowance{Provider: name, Window: "credits", Credits: true, Remaining: credits.Remaining, Unit: credits.Unit, Unlimited: credits.Unlimited})
		}
	}
	return readings
}

// readAFKAllowance reads the allowance through the runtime's quota-axi, or
// says why it could not.
func readAFKAllowance(runtime commandRuntime) func(context.Context) ([]afk.Allowance, string) {
	return func(ctx context.Context) ([]afk.Allowance, string) {
		if runtime.quota == nil {
			return nil, "this build reads no quota-axi"
		}
		report, skipped := runtime.quota(ctx)
		if skipped != "" {
			return nil, skipped
		}
		return afkAllowance(report), ""
	}
}
