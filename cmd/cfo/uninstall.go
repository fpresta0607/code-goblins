package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/uninstall"
)

// runUninstall removes Code Goblins from this machine and leaves it as the
// install found it. It shows its plan first and changes nothing until the
// person says yes:
//
//	goblins uninstall
//	goblins uninstall --dry-run [--json]
//	goblins uninstall --yes [--remove-work] [--remove-tools]
func runUninstall(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "print the whole plan and change nothing")
	asJSON := fs.Bool("json", false, "with --dry-run, print the plan for a program")
	yes := fs.Bool("yes", false, "uninstall without asking")
	removeWork := fs.Bool("remove-work", false, "remove the goblins' work that has not landed and your fleet data too, instead of keeping them")
	removeTools := fs.Bool("remove-tools", false, "remove the tools the install fetched too, instead of leaving them")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *asJSON && !*dryRun {
		fmt.Fprintln(stderr, "usage: goblins uninstall [--dry-run [--json]] [--yes] [--remove-work] [--remove-tools]")
		return 2
	}
	// The uninstall stops every Code Goblins terminal, this one among them,
	// which would end it part way. Its dry run stops nothing.
	if os.Getenv("CFO_HOST_ID") != "" && !*dryRun {
		fmt.Fprintln(stderr, "goblins uninstall: this terminal is Code Goblins' own and stops with it, so run the uninstall in a terminal of your own")
		return 1
	}
	machine, err := uninstall.ThisMachine()
	if err != nil {
		fmt.Fprintln(stderr, "goblins uninstall: "+err.Error())
		return 1
	}
	ctx := context.Background()
	choices := uninstall.Choices{RemoveWork: *removeWork, RemoveTools: *removeTools}
	plan, err := uninstall.NewPlan(ctx, machine, choices)
	if err != nil {
		fmt.Fprintln(stderr, "goblins uninstall: "+err.Error())
		return 1
	}
	if *dryRun {
		if *asJSON {
			data, err := json.MarshalIndent(struct {
				Home  string           `json:"home"`
				Kept  string           `json:"kept"`
				Items []uninstall.Item `json:"items"`
			}{plan.Home, plan.Kept, plan.Items}, "", "  ")
			if err != nil {
				fmt.Fprintln(stderr, "goblins uninstall: "+err.Error())
				return 1
			}
			fmt.Fprintln(stdout, string(data))
			return 0
		}
		plan.WriteDryRun(stdout)
		return 0
	}
	if !plan.Acts() {
		fmt.Fprintln(stdout, "Code Goblins is not installed here, so there is nothing to remove.")
		return 0
	}
	plan.WriteSummary(stdout)
	if !*yes {
		if !*removeWork || !*removeTools {
			fmt.Fprintln(stdout, "To remove the kept work or the tools too: goblins uninstall --remove-work --remove-tools")
		}
		fmt.Fprint(stdout, "Uninstall Code Goblins now? Type yes: ")
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(answer), "yes") {
			fmt.Fprintln(stdout, "Nothing was changed.")
			return 1
		}
		fmt.Fprintln(stdout)
	}
	outcome := plan.Run(ctx, func(group uninstall.Group, done []uninstall.Item, left []uninstall.Left, finished bool) {
		if !finished {
			return
		}
		if line := plan.GroupDone(group, done); line != "" {
			fmt.Fprintln(stdout, line)
		}
		for _, l := range left {
			fmt.Fprintln(stdout, l.Sentence())
		}
	})
	if outcome.Stopped {
		fmt.Fprintln(stdout, "Nothing that holds your work was removed, since the work could not be kept.")
		return 1
	}
	if aside := plan.Aside(); aside != "" {
		if err := uninstall.RemoveOnceEnded(aside); err != nil {
			fmt.Fprintf(stdout, "Left the uninstall's own program in %s, %v, so delete that folder once this window closes.\n", aside, err)
		}
	}
	// The last read back is a plan made again from what the machine holds
	// now: anything of Code Goblins' it still finds is named, but for this
	// program, which goes once it ends.
	machine.Aside = plan.Aside()
	again, err := uninstall.NewPlan(ctx, machine, choices)
	if err != nil {
		fmt.Fprintln(stdout, "The check that nothing is left could not run: "+err.Error()+".")
		return 1
	}
	if remaining := again.Remaining(); len(remaining) > 0 || len(outcome.Left) > 0 {
		fmt.Fprintf(stdout, "Checked: %d things of Code Goblins are left, named above.\n", max(len(remaining), len(outcome.Left)))
		for _, item := range remaining {
			if !outcome.Named(item) {
				fmt.Fprintln(stdout, uninstall.Left{Item: item, Why: "it is still there"}.Sentence())
			}
		}
		return 1
	}
	fmt.Fprintln(stdout, "Checked: nothing of Code Goblins is left on this PC.")
	fmt.Fprintln(stdout, "Open a new terminal for the PATH to update.")
	return 0
}
