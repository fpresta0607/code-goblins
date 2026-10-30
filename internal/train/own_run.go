package train

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A train of one pull request may land it on that pull request's own CI run,
// with no train run of its own, in a repository whose pull request runs test
// what a train's run would (Repository.LandsLoneRider). On one week of
// October 2026, 44 of a private repository's 99 trains carried one pull
// request and tested again the very tree its own run had just passed.
//
// The pull request's own run proves the tree only when it tested the merge
// GitHub would land now: each of its checks is a GitHub Actions check that
// passed, and every workflow run behind them checked out the pull request's
// merge commit as GitHub has it now, whose first parent is the base's tip
// and whose second is the head that rides. A run that tested an older base,
// a merge GitHub made again since, or a check no workflow run stands behind
// proves nothing, and the train runs CI as for any other.

// pullMergeFetched is the fetch of a pull request's merge commit in the log
// of a job actions/checkout checked it out in: +<sha>:refs/remotes/pull/<n>/merge.
var pullMergeFetched = regexp.MustCompile(`\+([0-9a-f]{40}):refs/remotes/pull/([0-9]+)/merge\b`)

// maxJobsRead bounds the logs read of one workflow run for the merge commit it
// checked out: the first job that checks out names it, and that is usually
// the first job.
const maxJobsRead = 3

// ownRun is the proof that car's own CI run tested car's head merged onto
// base as GitHub would land it now: the pages of the workflow runs behind its
// checks, and the merge commit they tested, which the checkout then holds.
// isProven is false when anything about it does not hold, or could not be
// read, so the train then runs CI as it would without.
func (e Engine) ownRun(ctx context.Context, t Train, car Car, base string) (proof, merge string, isProven bool) {
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "view", car.URL, "--json", "headRefOid,statusCheckRollup")
	if err != nil {
		return "", "", false
	}
	var view struct {
		HeadRefOid string `json:"headRefOid"`
		Checks     Checks `json:"statusCheckRollup"`
	}
	if json.Unmarshal([]byte(out), &view) != nil || view.HeadRefOid != car.Head {
		return "", "", false
	}
	if outcome, _ := checksOutcome(view.Checks); outcome != "passed" {
		return "", "", false
	}
	var runs []string
	for _, check := range view.Checks {
		found := actionsRun.FindStringSubmatch(check.link())
		if check.Kind != "CheckRun" || found == nil {
			return "", "", false
		}
		if !slices.Contains(runs, found[1]) {
			runs = append(runs, found[1])
		}
	}
	ref := fmt.Sprintf("refs/pull/%d/merge", car.Number)
	listed, err := e.run(ctx, t.Checkout, "git", "ls-remote", "origin", ref)
	if err != nil {
		return "", "", false
	}
	merge, _, _ = strings.Cut(listed, "\t")
	if merge == "" {
		return "", "", false
	}
	if _, err := e.run(ctx, t.Checkout, "git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", ref); err != nil {
		return "", "", false
	}
	parents, err := e.run(ctx, t.Checkout, "git", "rev-list", "--parents", "-n", "1", merge)
	if err != nil || parents != merge+" "+base+" "+car.Head {
		return "", "", false
	}
	var pages []string
	for _, id := range runs {
		tested, err := e.checkedOut(ctx, t, id, car.Number)
		if err != nil || tested != merge {
			return "", "", false
		}
		pages = append(pages, "https://github.com/"+t.Repository+"/actions/runs/"+id)
	}
	return strings.Join(pages, ", "), merge, true
}

// StandsOnOwnRun says whether car, riding a train alone onto repo's base as
// it is now, would land on its own CI run with no train run, where repo
// allows that: a lone rider that would costs nothing to land, so it need not
// wait for a second to share a train run with.
func (e Engine) StandsOnOwnRun(ctx context.Context, repo Repository, car Car) bool {
	if !repo.LandsLoneRider {
		return false
	}
	t := Train{Repository: repo.Slug, Checkout: repo.Checkout, Base: repo.Base}
	base, err := e.baseSHA(ctx, t)
	if err != nil {
		return false
	}
	_, _, isProven := e.ownRun(ctx, t, car, base)
	return isProven
}

// checkedOut reads the merge commit of pull request number that workflow
// run id checked out, from the log of the first of its jobs that passed and
// fetched one, among the first maxJobsRead.
func (e Engine) checkedOut(ctx context.Context, t Train, id string, number int) (string, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "api", fmt.Sprintf("repos/%s/actions/runs/%s/jobs?per_page=100", t.Repository, id))
	if err != nil {
		return "", err
	}
	var listed struct {
		Jobs []struct {
			ID         int64  `json:"id"`
			Conclusion string `json:"conclusion"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		return "", fmt.Errorf("gh listed the jobs of workflow run %s in a shape it cannot read: %w", id, err)
	}
	read := 0
	for _, job := range listed.Jobs {
		if job.Conclusion != "success" {
			continue
		}
		if read++; read > maxJobsRead {
			break
		}
		log, err := e.run(ctx, t.Checkout, "gh", "api", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", t.Repository, job.ID))
		if err != nil {
			return "", err
		}
		found := pullMergeFetched.FindStringSubmatch(log)
		if found == nil {
			continue
		}
		if found[2] != strconv.Itoa(number) {
			return "", fmt.Errorf("workflow run %s checked out the merge of pull request #%s, not #%d", id, found[2], number)
		}
		return found[1], nil
	}
	return "", fmt.Errorf("no log of workflow run %s names the merge commit it checked out", id)
}

// standOnOwnRun records the run of a train of one rider, car, that stands on
// car's own CI run, which tested merge, car's head merged onto base: no
// branch is pushed and no pull request opened, and the next step lands it.
func (e Engine) standOnOwnRun(t *Train, car Car, base, merge, proof string) error {
	t.BaseSHA, t.Head, t.Pushed = base, merge, e.Now().UTC()
	t.Runs++
	t.endRun(RunRepushed, "")
	t.History = append(t.History, Run{Number: t.Runs, Riders: []int{car.Number}, Base: base, Head: merge, Pushed: t.Pushed, PR: car.URL, OwnRun: proof})
	return e.save(t)
}

// mergeOwnRun merges the one rider of a run that stands on its own CI run,
// with a merge commit, only while its head is the one that run tested. A
// merge GitHub refuses landed nothing, so the landing is over and the next
// step reads the base and the rider again; one that went through although
// its answer was lost is found on the base.
func (e Engine) mergeOwnRun(ctx context.Context, t *Train, car Car, run Run) error {
	_, err := e.runWithBody(ctx, t.Checkout, t.ownRunEvidence(run, car), "pr", "merge", car.URL, "--merge", "--match-head-commit", car.Head)
	if err == nil {
		return nil
	}
	t.openRun().MergeFailures = append(t.openRun().MergeFailures, err.Error())
	if saveErr := e.save(t); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	onBase, checkErr := e.isOnBase(ctx, *t, car.Head)
	switch {
	case checkErr != nil:
		return errors.Join(err, checkErr)
	case onBase:
		return nil
	}
	t.Landing = false
	return errors.Join(fmt.Errorf("%w: its pull request %s did not merge: %w", errMergeRefused, car.URL, err), e.save(t))
}

// ownRunEvidence says what the landing of car on its own CI run stands on.
func (t Train) ownRunEvidence(run Run, car Car) string {
	return fmt.Sprintf("merge train %s: CI passed on %s, which tested %s at %s merged onto %s at %s, the tree that lands, so no train run tested it again", t.ID, run.OwnRun, car.URL, short(car.Head), t.Base, short(run.Base))
}
