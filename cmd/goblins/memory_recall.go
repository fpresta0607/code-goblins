package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/memory"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runMemoryRecall answers goblins memory recall "<question>": from the running
// supervisor's index, or from one built here when --data names the memory
// or no supervisor runs. --eval scores a question set instead.
func runMemoryRecall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goblins memory recall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data", "", "a home's private data directory, indexed with its sibling state directory in this process")
	var query memory.RecallQuery
	flags.StringVar(&query.Project, "project", "", "only this project's items and fleet-wide ones")
	flags.StringVar(&query.Repo, "repo", "", "only this repository's items and fleet-wide ones, as owner/name or name")
	flags.StringVar(&query.Task, "task", "", "only this task's items and those of no task")
	flags.StringVar(&query.Role, "role", "", "only items for this role, ceo or goblin, and those for every role")
	since := flags.String("since", "", "only items dated on or after this day, YYYY-MM-DD")
	until := flags.String("until", "", "only items dated on or before this day, YYYY-MM-DD")
	flags.BoolVar(&query.IsHistory, "history", false, "include superseded facts and the memory archive")
	flags.IntVar(&query.Limit, "limit", memory.DefaultRecallLimit, "how many items, 1 to 50; with --eval the depth a hit must reach")
	eval := flags.String("eval", "", "JSON array of {question, project, expect} cases to score instead of one question")
	var question string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		question, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if question == "" && flags.NArg() == 1 {
		question = flags.Arg(0)
	} else if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "goblins memory recall: give one quoted question")
		return 2
	}
	if (question == "") == (*eval == "") {
		fmt.Fprintln(stderr, "goblins memory recall: give one quoted question, or --eval <file>")
		return 2
	}
	query.Question = question
	var err error
	if *since != "" {
		if query.Since, err = time.Parse(time.DateOnly, *since); err != nil {
			fmt.Fprintf(stderr, "goblins memory recall: --since %q is not a YYYY-MM-DD day\n", *since)
			return 2
		}
	}
	if *until != "" {
		if query.Until, err = time.Parse(time.DateOnly, *until); err != nil {
			fmt.Fprintf(stderr, "goblins memory recall: --until %q is not a YYYY-MM-DD day\n", *until)
			return 2
		}
		query.Until = query.Until.Add(24*time.Hour - time.Nanosecond)
	}
	isLocal := *dataDir != ""
	stateDir := filepath.Join(filepath.Dir(filepath.Clean(*dataDir)), "state")
	if !isLocal {
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		*dataDir, stateDir = h.Data, h.State
	}
	if *eval != "" {
		return evaluateRecall(*eval, *dataDir, stateDir, query.Limit, stdout, stderr)
	}
	origin := "index held by the supervisor"
	result, err := memory.RecallResult{}, supervisor.ErrNotRunning
	if !isLocal {
		result, err = supervisor.Recall(stateDir, query)
	}
	if errors.Is(err, supervisor.ErrNotRunning) {
		origin = "index built in this process"
		if !isLocal {
			origin += "; the supervisor is not running"
		}
		var index *memory.Index
		if index, err = memory.BuildIndex(*dataDir, stateDir, time.Now()); err == nil {
			result, err = index.Recall(query)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "goblins memory recall: %v\n", err)
		return 1
	}
	_, err = io.WriteString(stdout, memory.RenderRecall(query, result, origin+", built "+result.BuiltAt.Format(time.RFC3339)))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// evaluateRecall scores a question set against an index built here, timing
// the build and each question, and prints the report as JSON.
func evaluateRecall(path, dataDir, stateDir string, depth int, stdout, stderr io.Writer) int {
	content, err := fsx.ReadFile(path)
	var cases []memory.EvalCase
	if err == nil {
		err = memory.Decode(content, &cases)
	}
	if err != nil {
		fmt.Fprintf(stderr, "goblins memory recall: the evaluation set %s: %v\n", path, err)
		return 1
	}
	started := time.Now()
	index, err := memory.BuildIndex(dataDir, stateDir, started)
	var report memory.EvalReport
	if err == nil {
		report, err = index.Evaluate(cases, depth, time.Since(started))
	}
	if err != nil {
		fmt.Fprintf(stderr, "goblins memory recall: %v\n", err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
