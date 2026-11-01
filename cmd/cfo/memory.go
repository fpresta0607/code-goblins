package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

func runMemory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "cfo memory: retrieve, routes, budget or migration-plan is required")
		return 2
	}
	flags := flag.NewFlagSet("cfo memory "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "private data directory; defaults to the resolved home")
	var query memory.Query
	var packages string
	switch args[0] {
	case "retrieve":
		flags.StringVar(&query.ID, "id", "", "one exact memory id")
		flags.StringVar(&query.Project, "project", "", "project routing key")
		flags.StringVar(&query.Task, "task", "", "task routing key")
		flags.StringVar(&query.Topic, "topic", "", "topic routing key")
	case "budget":
		flags.StringVar(&packages, "packages", "", "JSON array of actual startup inputs for codex, claude and pi")
	case "routes", "migration-plan":
	default:
		fmt.Fprintf(stderr, "cfo memory: unknown operation %q\n", args[0])
		return 2
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if query.ID != "" && (query.Project != "" || query.Task != "" || query.Topic != "") {
		fmt.Fprintln(stderr, "cfo memory: --id is an exact lookup; use scope flags separately")
		return 2
	}
	if *dataDir == "" {
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		*dataDir = h.Data
	}
	encode := func(value any) int {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if args[0] == "migration-plan" {
		plan, err := memory.PlanMigration(*dataDir)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return encode(plan)
	}
	if args[0] == "budget" && packages != "" {
		data, err := fsx.ReadFile(packages)
		var inputs []memory.Package
		if err == nil {
			err = memory.Decode(data, &inputs)
		}
		var reports []memory.PackageReport
		if err == nil {
			var budget int
			budget, err = memory.ReadBudget(*dataDir, "startup-context-budget", 20000)
			if err == nil {
				reports, err = memory.MeasurePackages(inputs, filepath.Dir(packages), budget)
			}
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return encode(reports)
	}
	if args[0] == "budget" {
		report, err := memory.MemoryReport(*dataDir, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "MEMORY BUDGET UNREADABLE: %s\n", err)
			return 1
		}
		return encode(report)
	}
	catalog, err := memory.Load(*dataDir, time.Now())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "MEMORY ABSENT: catalog or source missing; legacy authority remains in its original files; run cfo memory migration-plan")
		} else {
			fmt.Fprintf(stderr, "MEMORY UNREADABLE: %s\n", err)
		}
		return 1
	}
	if args[0] == "routes" {
		_, err = io.WriteString(stdout, catalog.Routes(int(^uint(0)>>1)))
	} else {
		var text string
		text, err = catalog.Retrieve(query, time.Now())
		if err == nil {
			_, err = io.WriteString(stdout, text)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
