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
		fmt.Fprintln(stderr, "ceo memory: retrieve, routes, budget, migration-plan or retention-plan is required")
		return 2
	}
	flags := flag.NewFlagSet("ceo memory "+args[0], flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "private data directory; defaults to the resolved home")
	var query memory.Query
	var packages, seen, manifest string
	switch args[0] {
	case "retrieve":
		flags.StringVar(&query.ID, "id", "", "one exact memory id")
		flags.StringVar(&query.Project, "project", "", "project routing key")
		flags.StringVar(&query.Task, "task", "", "task routing key")
		flags.StringVar(&query.Topic, "topic", "", "topic routing key")
		flags.StringVar(&query.Search, "search", "", "keyword discovery within supplied scope")
		flags.IntVar(&query.Limit, "limit", 5, "maximum cold search results, 1 to 50; warm authority is never capped")
		flags.BoolVar(&query.IncludeCompleted, "include-completed", false, "include completed task history in keyword discovery")
		flags.StringVar(&seen, "seen", "", "JSON array of delivery fingerprints actually read and still held in this conversation")
	case "budget":
		flags.StringVar(&packages, "packages", "", "JSON array of actual startup inputs for codex, claude and pi")
	case "routes", "migration-plan":
	case "retention-plan":
		flags.StringVar(&manifest, "manifest", "", "JSON array of local recovery proposals; no apply operation")
	default:
		fmt.Fprintf(stderr, "ceo memory: unknown operation %q\n", args[0])
		return 2
	}
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if query.ID != "" && (query.Project != "" || query.Task != "" || query.Topic != "" || query.Search != "") {
		fmt.Fprintln(stderr, "ceo memory: --id is an exact lookup; use scope flags separately")
		return 2
	}
	if args[0] == "retrieve" && (query.Limit < 1 || query.Limit > 50) {
		fmt.Fprintln(stderr, "ceo memory: --limit must be between 1 and 50")
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
	if args[0] == "retention-plan" {
		if manifest == "" {
			fmt.Fprintln(stderr, "ceo memory: retention-plan requires --manifest")
			return 2
		}
		content, err := fsx.ReadFile(manifest)
		var proposals []memory.RetentionProposal
		if err == nil {
			err = memory.Decode(content, &proposals)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		plan, err := memory.PlanRetention(*dataDir, proposals)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return encode(plan)
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
	if seen != "" {
		content, err := fsx.ReadFile(seen)
		if err == nil {
			err = memory.Decode(content, &query.Delivered)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	catalog, err := memory.Load(*dataDir, time.Now())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "MEMORY ABSENT: catalog or source missing; legacy authority remains in its original files; run ceo memory migration-plan")
		} else {
			fmt.Fprintf(stderr, "MEMORY UNREADABLE: %s\n", err)
		}
		return 1
	}
	if args[0] == "routes" {
		_, err = io.WriteString(stdout, catalog.Routes(int(^uint(0)>>1)))
	} else {
		var text string
		text, err = catalog.Retrieve(query)
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
