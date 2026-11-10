package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

// runMemorySync derives the startup catalog again from the memory files,
// which the CEO runs after memory changes.
//
//	cfo memory sync
func runMemorySync(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "cfo memory sync takes no argument")
		return 2
	}
	h, exit := catalogWriter("sync", stderr)
	if exit != 0 {
		return exit
	}
	report, err := memory.Sync(h.Data, h.State, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "cfo memory sync: "+err.Error())
		return 1
	}
	return printSyncReport(h, report, stdout, stderr)
}

// runMemoryScope rules whose a memory entry is: a project's, which leaves
// startup and comes with that project's scope, or the whole fleet's, which
// loads at every start. It also lists the rulings no scope is ruled for yet.
//
//	cfo memory scope --project <name>... [--ids <file>] [<id>...]
//	cfo memory scope --fleet [--ids <file>] [<id>...]
//	cfo memory scope --expiry "<words>" <id>...
//	cfo memory scope --list
func runMemoryScope(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cfo memory scope", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var tag memory.Tag
	flags.Func("project", "the project the entries are ruled for, which they then come with; repeat it for each", func(name string) error {
		tag.Projects = append(tag.Projects, name)
		return nil
	})
	flags.BoolVar(&tag.IsFleet, "fleet", false, "the entries are the whole fleet's and load at every start")
	flags.StringVar(&tag.Expiry, "expiry", "", "words of a perishable entry that say when it ends")
	idsFile := flags.String("ids", "", "a file naming one entry id on each line, bare or in the brackets the startup map prints it in")
	isList := flags.Bool("list", false, "print the rulings no scope is ruled for yet, each under its id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *isList {
		if flags.NArg() != 0 || *idsFile != "" || tag.IsFleet || len(tag.Projects) > 0 || tag.Expiry != "" {
			fmt.Fprintln(stderr, "cfo memory scope: --list only lists; tag in a command of its own")
			return 2
		}
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		catalog, err := memory.Load(h.Data, time.Now())
		text := ""
		if err == nil {
			text, err = catalog.Unclassified()
		}
		if err != nil {
			fmt.Fprintln(stderr, "cfo memory scope: "+memoryUnreadable(err))
			return 1
		}
		if text == "" {
			text = "Every ruling has a scope.\n"
		}
		fmt.Fprint(stdout, text)
		return 0
	}
	tag.IDs = flags.Args()
	if *idsFile != "" {
		content, err := fsx.ReadFile(*idsFile)
		if err != nil {
			fmt.Fprintln(stderr, "cfo memory scope: "+err.Error())
			return 1
		}
		for _, line := range strings.Split(string(content), "\n") {
			if fields := strings.Fields(line); len(fields) > 0 {
				tag.IDs = append(tag.IDs, fields[0])
			}
		}
	}
	for position, id := range tag.IDs {
		tag.IDs[position] = strings.Trim(id, "[]")
	}
	h, exit := catalogWriter("scope", stderr)
	if exit != 0 {
		return exit
	}
	report, err := memory.Scope(h.Data, h.State, time.Now(), tag)
	if err != nil {
		fmt.Fprintln(stderr, "cfo memory scope: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "Tagged %d.\n", report.Tagged)
	return printSyncReport(h, report, stdout, stderr)
}

// catalogWriter resolves the home for a command that writes the memory
// catalog, which decides what a CEO session reads at its start.
func catalogWriter(operation string, stderr io.Writer) (home.Home, int) {
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		fmt.Fprintf(stderr, "cfo memory %s: the memory catalog is the CEO's to write, never a goblin's or a gate agent's\n", operation)
		return home.Home{}, 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return home.Home{}, 1
	}
	return h, 0
}

func printSyncReport(h home.Home, report memory.SyncReport, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "The catalog lists %d entries: %d for startup and %d by scope.\n", report.Entries, report.Warm, report.Cold)
	if report.Added > 0 {
		fmt.Fprintf(stdout, "New to it: %d. A new ruling loads until a scope is ruled for it: cfo memory scope --list shows them.\n", report.Added)
	}
	if len(report.Dropped) > 0 {
		fmt.Fprintf(stdout, "No longer in memory, with the scope each carried: %s.\n", strings.Join(report.Dropped, ", "))
	}
	budget, err := memory.MemoryReport(h.Data, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "MEMORY BUDGET UNREADABLE: %s\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Startup memory is %d estimated tokens of a budget of %d.\n", budget.EstimatedTokens, budget.Budget)
	return 0
}

// memoryUnreadable says why a catalog did not load and what puts it right.
func memoryUnreadable(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "MEMORY ABSENT: catalog or source missing; legacy authority remains in its original files; cfo memory sync derives the catalog from them"
	}
	return "MEMORY UNREADABLE: " + err.Error() + "; when memory changed after the catalog was written, cfo memory sync derives it again"
}
