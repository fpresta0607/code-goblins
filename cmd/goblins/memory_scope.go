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
//	goblins memory sync
func runMemorySync(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "goblins memory sync takes no argument")
		return 2
	}
	h, exit := catalogWriter("sync", stderr)
	if exit != 0 {
		return exit
	}
	report, err := memory.Sync(h.Data, h.State, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "goblins memory sync: "+err.Error())
		return 1
	}
	return printSyncReport(h, report, stdout, stderr)
}

// runMemoryScope rules whose a memory entry is: a project's, which leaves
// startup and comes with that project's scope, or the whole fleet's, which
// loads at every start. It also names when a perishable entry ends, and lists
// the rulings no scope is ruled for yet.
//
//	goblins memory scope --project <name>... [--ids <file>] [<id>...]
//	goblins memory scope --fleet [--ids <file>] [<id>...]
//	goblins memory scope --expiry <id>="<words>"...
//	goblins memory scope --list
func runMemoryScope(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goblins memory scope", flag.ContinueOnError)
	flags.SetOutput(stderr)
	tag := memory.Tag{Expiries: map[string]string{}}
	flags.Func("project", "the project the entries are ruled for, which they then come with; repeat it for each", func(name string) error {
		tag.Projects = append(tag.Projects, name)
		return nil
	})
	flags.BoolVar(&tag.IsFleet, "fleet", false, "the entries are the whole fleet's and load at every start")
	flags.Func("expiry", "a perishable entry's id, =, and the words of that entry that say when it ends; repeat it for each entry, which needs no id argument besides", func(pair string) error {
		id, words, isPair := strings.Cut(pair, "=")
		if !isPair || strings.TrimSpace(id) == "" || strings.TrimSpace(words) == "" {
			return errors.New("an expiry reads <id>=<words>, with the entry's id and words that entry holds")
		}
		if _, isNamed := tag.Expiries[id]; isNamed {
			return fmt.Errorf("entry %s is given two expiries", id)
		}
		tag.Expiries[id] = words
		return nil
	})
	idsFile := flags.String("ids", "", "a file naming one bare entry id on each line")
	isList := flags.Bool("list", false, "print the rulings no scope is ruled for yet, each under its id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *isList {
		if flags.NArg() != 0 || *idsFile != "" || tag.IsFleet || len(tag.Projects) > 0 || len(tag.Expiries) > 0 {
			fmt.Fprintln(stderr, "goblins memory scope: --list only lists; tag in a command of its own")
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
			fmt.Fprintln(stderr, "goblins memory scope: "+memoryUnreadable(err))
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
			fmt.Fprintln(stderr, "goblins memory scope: "+err.Error())
			return 1
		}
		for number, line := range strings.Split(string(content), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 {
				fmt.Fprintf(stderr, "goblins memory scope: line %d of %s holds more than one bare id, so nothing was tagged\n", number+1, *idsFile)
				return 2
			}
			tag.IDs = append(tag.IDs, fields...)
		}
	}
	h, exit := catalogWriter("scope", stderr)
	if exit != 0 {
		return exit
	}
	report, err := memory.Scope(h.Data, h.State, time.Now(), tag)
	if err != nil {
		fmt.Fprintln(stderr, "goblins memory scope: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "Tagged %d.\n", report.Tagged)
	return printSyncReport(h, report, stdout, stderr)
}

// catalogWriter resolves the home for a command that writes the memory
// catalog, which decides what a CEO session reads at its start.
func catalogWriter(operation string, stderr io.Writer) (home.Home, int) {
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		fmt.Fprintf(stderr, "goblins memory %s: the memory catalog is the CEO's to write, never a goblin's or a gate agent's\n", operation)
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
		fmt.Fprintf(stdout, "New to it: %d. A new ruling loads until a scope is ruled for it: goblins memory scope --list shows them.\n", report.Added)
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
		return "MEMORY ABSENT: catalog or source missing; legacy authority remains in its original files; goblins memory sync derives the catalog from them"
	}
	return "MEMORY UNREADABLE: " + err.Error() + "; when memory changed after the catalog was written, goblins memory sync derives it again"
}
