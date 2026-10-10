package train

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// actionsRun finds the workflow run behind a check's page, as in
// https://github.com/owner/name/actions/runs/123/job/456.
var actionsRun = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/actions/runs/([0-9]+)`)

// secondTry answers a red run before the train acts on it. A check can fail
// by chance, so the failed jobs of each workflow run behind a failed check
// run again, once, and only a check red a second time is a failure. It
// returns "failed" once the failure stands, and "pending" while a second try
// is asked for or awaited.
//
// Whether a workflow run was tried again is read from GitHub's own count of
// its attempts and never from the train's record: a step cut short after it
// asked, or whose ask never arrived, takes the run on as GitHub has it, so no
// run is tried a third time. The pull request's checks can still show the
// first try after the second began or passed, so a failure stands only once
// its workflow run itself ended red on a later attempt. A check no workflow
// run stands behind, such as another service's, cannot be run again and
// stands as it is.
func (e Engine) secondTry(ctx context.Context, t *Train, failed []Check) (string, error) {
	var read, due []string
	for _, check := range failed {
		found := actionsRun.FindStringSubmatch(check.link())
		if found == nil {
			return "failed", nil
		}
		id := found[1]
		if slices.Contains(read, id) {
			continue
		}
		read = append(read, id)
		out, err := e.run(ctx, t.Checkout, "gh", "run", "view", id, "--repo", t.Repository, "--json", "attempt,status,conclusion")
		if err != nil {
			return "", fmt.Errorf("merge train %s: read workflow run %s: %w", t.ID, id, err)
		}
		var workflow struct {
			Attempt    int    `json:"attempt"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		if err := json.Unmarshal([]byte(out), &workflow); err != nil {
			return "", fmt.Errorf("merge train %s: gh reported workflow run %s in a shape it cannot read: %w", t.ID, id, err)
		}
		switch {
		case workflow.Attempt < 1:
			// An answer without the attempt would read as a first try for ever.
			return "", fmt.Errorf("merge train %s: gh did not say which attempt workflow run %s is at", t.ID, id)
		case workflow.Status != "completed" || workflow.Conclusion == "success":
			// The checks read are an earlier try's: this one still runs, or
			// passed and GitHub has yet to show it.
		case workflow.Attempt > 1:
			return "failed", nil
		default:
			due = append(due, id)
		}
	}
	if len(due) == 0 {
		return "pending", nil
	}
	if run := t.openRun(); run != nil && len(run.FailedOnce) == 0 {
		run.FailedOnce = failedChecks(failed)
	}
	t.Note = fmt.Sprintf("CI failed once on %s (%s), so its failed checks run again before any pull request is blamed", t.numbers(t.carsIn(CarRiding)), checkNames(failed))
	if err := e.save(t); err != nil {
		return "", err
	}
	for _, id := range due {
		if _, err := e.run(ctx, t.Checkout, "gh", "run", "rerun", id, "--failed", "--repo", t.Repository); err != nil {
			return "", fmt.Errorf("merge train %s: run the failed jobs of workflow run %s again: %w", t.ID, id, err)
		}
	}
	return "pending", nil
}

// failedChecks names each failed check with its page, as a run's record
// keeps them.
func failedChecks(failed []Check) []FailedCheck {
	var named []FailedCheck
	for _, check := range failed {
		named = append(named, FailedCheck{Name: check.name(), Link: check.link()})
	}
	return named
}

// String names the check with its own page.
func (c FailedCheck) String() string {
	if c.Link == "" {
		return c.Name
	}
	return c.Name + " (" + c.Link + ")"
}

// passedOnSecondTry names the checks that failed once in a run of t and
// passed when they ran again, each with its page and its run, so a test that
// fails by chance is counted and never hidden. A run that ended before its
// second try did names none.
func (t Train) passedOnSecondTry() string {
	var passed []string
	for _, run := range t.History {
		switch run.Result {
		case RunLanded, RunMoved, RunChanged, RunFailed:
		default:
			continue
		}
		for _, check := range run.FailedOnce {
			if !slices.ContainsFunc(run.Failed, func(again FailedCheck) bool { return again.Name == check.Name }) {
				passed = append(passed, fmt.Sprintf("%s in run %d", check, run.Number))
			}
		}
	}
	return strings.Join(passed, ", ")
}
