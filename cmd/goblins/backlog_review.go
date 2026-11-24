package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/workreview"
)

// runBacklogReview prints what the backlog's queued and parked rows and the
// paused tasks need a decision on: work done already, steps for the owner that
// were overtaken, work to fold into other work and work gone stale. It reads
// only.
func runBacklogReview(stdout, stderr io.Writer, runtime commandRuntime) int {
	if runtime.resolveHome == nil || runtime.repositoryOf == nil || runtime.githubAPI == nil {
		fmt.Fprintln(stderr, "goblins backlog review: command runtime is incomplete")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintf(stderr, "goblins backlog review: %v\n", err)
		return 1
	}
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		fmt.Fprintf(stderr, "goblins backlog review: %v\n", err)
		return 1
	}
	ctx := context.Background()
	commands := runtime.github
	if commands == nil {
		commands = execx.OSRunner{}
	}
	github := tickets.GitHub{Commands: commands, API: runtime.githubAPI}

	var problems []string
	addProblem := func(problem string) {
		if !slices.Contains(problems, problem) {
			problems = append(problems, problem)
		}
	}
	repositories := map[string]string{}
	resolve := func(project string) (string, string) {
		if project == "" {
			return "", ""
		}
		checkout, err := runtime.resolveProject(project)
		if err != nil {
			addProblem(fmt.Sprintf("project %s is not read: %v", project, err))
			return "", ""
		}
		key := strings.ToLower(filepath.Clean(checkout))
		if repository, isKnown := repositories[key]; isKnown {
			return repository, checkout
		}
		repository := ""
		if _, err := os.Stat(checkout); err == nil {
			if repository, err = issueRepository(ctx, h, runtime.repositoryOf, checkout); err != nil {
				addProblem(fmt.Sprintf("the repository of %s cannot be read: %v", checkout, err))
			}
		}
		repositories[key] = repository
		return repository, checkout
	}

	report, err := workreview.Run(ctx, h, resolve, github.WorkStates, time.Now().UTC(), time.Duration(settings.StaleWorkDays)*24*time.Hour)
	if err != nil {
		fmt.Fprintf(stderr, "goblins backlog review: %v\n", err)
		return 1
	}
	renderBacklogReview(stdout, report.Findings, append(problems, report.Problems...))
	return 0
}

// renderBacklogReview writes each finding, its evidence and its proposal, then
// what could not be read, or says there is nothing to review.
func renderBacklogReview(w io.Writer, findings []workreview.Finding, problems []string) {
	if len(findings) == 0 && len(problems) == 0 {
		fmt.Fprintln(w, "nothing to review")
		return
	}
	for _, finding := range findings {
		fmt.Fprintf(w, "%s: %s\n", finding.Kind, strings.Join(finding.Items, ", "))
		for _, line := range finding.Evidence {
			fmt.Fprintf(w, "  evidence: %s\n", line)
		}
		fmt.Fprintf(w, "  proposed: %s\n", finding.Proposal)
	}
	for _, problem := range problems {
		fmt.Fprintf(w, "not read: %s\n", problem)
	}
}
