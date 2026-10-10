// Command ciplan says which jobs a pull request's own CI run starts: the ones
// its change can alter. A merge train's run and a run on the default branch
// start every job without asking, so nothing reaches the default branch that
// the full set did not test, and a pull request's run can leave a job out
// when nothing in its change reaches what that job tests.
//
// The rule is internal/gatetest's ChooseJobs over the plan the gate's own
// test step makes: the packages the change reaches under config/verify.json,
// as the base has it. The jobs and what each reads beside its Go packages
// come from JOBS in the environment, which the go workflow keeps beside its
// jobs.
//
// It writes the workflow's outputs to standard output, go as the JSON list
// of the Go jobs and frontend and browser as true or false, and what it
// decided and why to standard error. Run it from the repository, on the
// merge of a pull request into its base:
//
//	go run ./tools/ciplan -base HEAD^1 >> $GITHUB_OUTPUT
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatetest"
)

// planner is this program's own directory, as the rule names packages.
const planner = "./tools/ciplan"

func main() {
	base := flag.String("base", "HEAD^1", "the commit the change is measured from")
	flag.Parse()
	if err := run(*base, os.Getenv("JOBS"), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ciplan:", err)
		os.Exit(1)
	}
}

func run(base, table string, stdout, stderr io.Writer) error {
	jobTable, err := parseTable(table)
	if err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	plan, err := gatetest.ReadSince(ctx, execx.OSRunner{}, dir, base, gatetest.Affected)
	if err != nil {
		return err
	}
	return write(gatetest.ChooseJobs(plan, jobTable, planner), plan, jobTable, stdout, stderr)
}

// parseTable reads the workflow's table of jobs. A table with no Go job, or
// with a field this build does not know, is refused: a rule read wrong would
// leave jobs out.
func parseTable(table string) (gatetest.JobTable, error) {
	var jobTable gatetest.JobTable
	decoder := json.NewDecoder(strings.NewReader(table))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&jobTable); err != nil {
		return gatetest.JobTable{}, fmt.Errorf("JOBS in the environment is not the workflow's table of jobs: %w", err)
	}
	if len(jobTable.Go) == 0 || len(jobTable.Frontend.Paths) == 0 || len(jobTable.Browser.Paths) == 0 {
		return gatetest.JobTable{}, errors.New("JOBS in the environment names no Go job, or no path for the frontend or browser jobs")
	}
	return jobTable, nil
}

// write prints the outputs and the reasons.
func write(jobs gatetest.Jobs, plan gatetest.Plan, table gatetest.JobTable, stdout, stderr io.Writer) error {
	shards, err := json.Marshal(append([]gatetest.Shard{}, jobs.Go...))
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "ci plan: %d changed files since %.8s, policy %s\n", len(plan.Changed), plan.Base, plan.Policy)
	if jobs.Everything {
		fmt.Fprintf(stderr, "ci plan: every job runs: %s\n", jobs.Why)
	} else {
		fmt.Fprintf(stderr, "ci plan: %s\n", jobs.Why)
		for _, choice := range plan.Choices {
			fmt.Fprintf(stderr, "  %s\n", choice)
		}
		for _, set := range plan.Outside {
			fmt.Fprintf(stderr, "  outside the Go checks: %s\n", set)
		}
	}
	running := map[string]bool{}
	for _, shard := range jobs.Go {
		running[shard.Shard] = true
	}
	for _, shard := range table.Go {
		fmt.Fprintf(stderr, "ci plan: go (%s): %s\n", shard.Shard, verdict(running[shard.Shard]))
	}
	fmt.Fprintf(stderr, "ci plan: frontend: %s\nci plan: browser: %s\n", verdict(jobs.Frontend), verdict(jobs.Browser))
	_, err = fmt.Fprintf(stdout, "go=%s\nfrontend=%t\nbrowser=%t\n", shards, jobs.Frontend, jobs.Browser)
	return err
}

func verdict(runs bool) string {
	if runs {
		return "runs"
	}
	return "left out, nothing in the change reaches it"
}
