package train

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// halve answers a run whose failure stands, red a second time once its
// failed checks ran again: with one rider, that rider broke it; with more,
// the first half rides again alone and the rest wait. Cars that were waiting
// already leave the train untested, since the red half holds the culprit.
// While the base's own push CI is red at the commit the run was built on,
// the failure may be the base's, so no rider is blamed and the train stops.
func (e Engine) halve(ctx context.Context, t *Train, failed []Check) error {
	t.Moved = 0
	riders := t.carsIn(CarRiding)
	red, err := e.redOnBase(ctx, *t)
	if err != nil {
		return err
	}
	if run := t.openRun(); run != nil {
		run.Failed = failedChecks(failed)
	}
	t.endRun(RunFailed, firstLink(failed))
	if red != "" {
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("CI failed on %s (%s), and %s's own push CI is red at %s (%s), so no pull request is blamed: fix %s first", t.numbers(riders), checkNames(failed), t.Base, short(t.BaseSHA), red, t.Base))
	}
	for _, i := range t.carsIn(CarWaiting) {
		t.Cars[i].State = CarReturned
	}
	if len(riders) == 1 {
		culprit := &t.Cars[riders[0]]
		culprit.State = CarCulprit
		culprit.Note = "failed: " + checkNames(failed)
		return e.finish(ctx, t, StateStopped, fmt.Sprintf("#%d breaks CI on %s: %s", culprit.Number, t.Base, checkNames(failed)))
	}
	for _, i := range riders[(len(riders)+1)/2:] {
		t.Cars[i].State = CarWaiting
	}
	return e.rebuild(ctx, t, fmt.Sprintf("CI failed on %s (%s), so its first half rides alone next: %s", t.numbers(riders), checkNames(failed), t.numbers(t.carsIn(CarRiding))))
}

// firstLink is the page of the first failed check that has one.
func firstLink(failed []Check) string {
	for _, check := range failed {
		if link := check.link(); link != "" {
			return link
		}
	}
	return ""
}

// redOnBase names the workflows whose newest push run on the base, at the
// commit the run was built on, concluded red, or "" when none did.
func (e Engine) redOnBase(ctx context.Context, t Train) (string, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "run", "list", "--repo", t.Repository, "--branch", t.Base, "--commit", t.BaseSHA, "--event", "push", "--limit", "50", "--json", "workflowName,status,conclusion")
	if err != nil {
		return "", err
	}
	var runs []struct {
		Workflow   string `json:"workflowName"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return "", fmt.Errorf("gh listed the push runs of %s in a shape it cannot read: %w", t.Repository, err)
	}
	seen := map[string]bool{}
	var red []string
	for _, run := range runs {
		if seen[run.Workflow] {
			continue
		}
		seen[run.Workflow] = true
		switch run.Conclusion {
		case "failure", "timed_out", "startup_failure":
			if run.Status == "completed" {
				red = append(red, run.Workflow)
			}
		}
	}
	return strings.Join(red, ", "), nil
}
