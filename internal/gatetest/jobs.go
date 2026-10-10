package gatetest

import (
	"fmt"
	"slices"
	"strings"
)

// JobTable says what can change the result of each job of the CI workflow:
// the go workflow keeps it, as JSON, beside the jobs it describes.
type JobTable struct {
	// Frontend is the job that checks, builds and licenses the board, and
	// Browser the jobs that run its browser tests.
	Frontend JobRule `json:"frontend"`
	Browser  JobRule `json:"browser"`
	// Go are the jobs that test the Go packages, one to an entry.
	Go []Shard `json:"go"`
}

// JobRule names what a job reads beside the Go packages it tests: the files
// Paths name, as a policy's patterns name them, and the packages in the
// directories Packages names, written ./dir from the repository root.
type JobRule struct {
	Paths    []string `json:"paths"`
	Packages []string `json:"packages,omitempty"`
}

// Shard is one job of the Go tests: the packages it tests, as directories
// written ./dir and separated by spaces, or for the job that tests every
// other package the directories it leaves to the others, and the pattern
// naming the tests it runs or skips when it shares a package.
type Shard struct {
	Shard    string `json:"shard"`
	Packages string `json:"packages,omitempty"`
	Except   string `json:"except,omitempty"`
	Run      string `json:"run,omitempty"`
	Skip     string `json:"skip,omitempty"`
}

// Jobs are the jobs a pull request's own CI run starts for its change, and
// why. A merge train's run and a run on the default branch start every job
// and ask no one.
type Jobs struct {
	// Everything says the change can alter any job, so all of them run, and
	// Why says what about the change decides the run's breadth.
	Everything bool
	Why        string
	Frontend   bool
	Browser    bool
	Go         []Shard
}

// workflowFolder holds the workflows: one decides what every run does.
const workflowFolder = ".github/"

// ChooseJobs names the jobs of table that plan's change can alter, for a
// plan made at the affected level. planner is the directory, written ./dir,
// of the program that asks, whose own change is a change to the rule.
//
// Every job runs when the rule cannot be trusted to narrow the run: the
// change requires the full level (a module file changed, the policy cannot be
// read, or a changed file is one the policy does not account for), or it
// changes the rule itself, which is a workflow, the policy, or the planner or
// a package it imports. What the rule cannot be sure of widens the run.
//
// Otherwise a Go job runs when the change reaches one of its packages, and
// the job that tests every other package runs when the change reaches any
// package at all, since it also vets and builds all of them. The frontend
// and browser jobs run when a file their rule names changed or the change
// reaches a package their rule names.
func ChooseJobs(plan Plan, table JobTable, planner string) Jobs {
	reached := map[string]bool{}
	for _, choice := range plan.Choices {
		reached["."+strings.TrimPrefix(choice.ImportPath, plan.Module)] = true
	}
	everything := func(why string) Jobs {
		return Jobs{Everything: true, Why: why, Frontend: true, Browser: true, Go: slices.Clone(table.Go)}
	}
	switch {
	case plan.Required == Full:
		return everything(plan.Why)
	case slices.Contains(plan.Changed, PolicyPath):
		return everything(PolicyPath + " changed, which is the policy this rule reads")
	case slices.ContainsFunc(plan.Changed, func(file string) bool { return strings.HasPrefix(file, workflowFolder) }):
		return everything("a workflow changed, which decides what every run does")
	case reached[planner]:
		return everything("the change reaches " + planner + ", which is this rule's own code")
	}
	jobs := Jobs{
		Why:      fmt.Sprintf("the change reaches %d of the module's packages", len(reached)),
		Frontend: table.Frontend.reads(plan.Changed, reached),
		Browser:  table.Browser.reads(plan.Changed, reached),
	}
	for _, shard := range table.Go {
		own := strings.Fields(shard.Packages)
		if shard.Except != "" && len(reached) > 0 || slices.ContainsFunc(own, func(dir string) bool { return reached[dir] }) {
			jobs.Go = append(jobs.Go, shard)
		}
	}
	return jobs
}

// reads reports whether a change to changed that reaches the packages in
// reached can alter the job.
func (r JobRule) reads(changed []string, reached map[string]bool) bool {
	return slices.ContainsFunc(r.Packages, func(dir string) bool { return reached[dir] }) ||
		slices.ContainsFunc(changed, func(file string) bool {
			return slices.ContainsFunc(r.Paths, func(pattern string) bool { return matches(pattern, file) })
		})
}
