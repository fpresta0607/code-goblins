package train

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// A push to the base starts its deploy workflows, those whose names say
// deploy, as the supervisor reads them. Until each has finished successfully
// the deploy is not verified, and a train that merged then would start a
// second deploy on top of it: a failed deploy could no longer be pinned to one
// change, and the second could overlap or cancel the first. On 2026-10-22 a
// train landed at 01:52Z, its deploy ran from 01:56Z, and the supervisor
// started the next train at 02:02Z, which merged as soon as its run passed
// whatever that deploy did (#1335). So a train still tests while the base
// deploys, and merges only once every deploy on the base has finished
// successfully.

// deployRun is a push run on the base, as GitHub's API lists it.
type deployRun struct {
	Workflow   string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"head_sha"`
	URL        string `json:"html_url"`
}

// DeployHold is the base deploy keeping a green train from merging, with
// the last reminder time kept across supervisor restarts.
type DeployHold struct {
	Workflow   string    `json:"workflow"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	URL        string    `json:"url"`
	Notified   time.Time `json:"notified"`
}

const DEPLOY_REMINDER_INTERVAL = 30 * time.Minute

// unverifiedDeploy reads the base's newest push runs and returns the deploy
// that keeps the train from merging: a run of a deploy workflow that has not
// finished, or the newest run of one that ended otherwise than successfully.
// A skipped run deployed nothing and is passed over. It returns nil while
// every deploy on the base has finished successfully.
func (e Engine) unverifiedDeploy(ctx context.Context, t Train) (*deployRun, error) {
	var listed struct {
		Runs []deployRun `json:"workflow_runs"`
	}
	if err := e.get(ctx, fmt.Sprintf("repos/%s/actions/runs?%s", t.Repository, url.Values{"branch": {t.Base}, "event": {"push"}, "per_page": {"50"}}.Encode()), &listed); err != nil {
		return nil, fmt.Errorf("list the push runs of %s: %w", t.Repository, err)
	}
	newest := map[string]bool{}
	for _, run := range listed.Runs {
		if !strings.Contains(strings.ToLower(run.Workflow), "deploy") || run.Conclusion == "skipped" {
			continue
		}
		if run.Status != "completed" {
			return &run, nil
		}
		if newest[run.Workflow] {
			continue
		}
		newest[run.Workflow] = true
		if run.Conclusion != "success" {
			return &run, nil
		}
	}
	return nil, nil
}

// holdForDeploy keeps the train from merging while deploy has not finished
// successfully, and says what it waits on in its note and to the CEO, again
// every half hour: a deploy that ended otherwise than successfully
// waits for a run of it, or of a fix, that passes. The next step reads the
// base's deploys again.
func (e Engine) holdForDeploy(ctx context.Context, t *Train, deploy deployRun) error {
	note := fmt.Sprintf("waits to merge until %s's %s run at %s has finished successfully (%s)", t.Base, deploy.Workflow, short(deploy.HeadSHA), deploy.URL)
	if deploy.Status == "completed" {
		note = fmt.Sprintf("holds its merge, since %s's %s run at %s concluded %s (%s): run it again or land its fix, and the train merges once %s's deploy has finished successfully", t.Base, deploy.Workflow, short(deploy.HeadSHA), deploy.Conclusion, deploy.URL, t.Base)
	}
	if note == t.Note && t.DeployHold != nil && t.DeployHold.Status == deploy.Status && e.Now().Sub(t.DeployHold.Notified) < DEPLOY_REMINDER_INTERVAL {
		return nil
	}
	t.Note = note
	t.DeployHold = &DeployHold{Workflow: deploy.Workflow, Status: deploy.Status, Conclusion: deploy.Conclusion, URL: deploy.URL, Notified: e.Now().UTC()}
	if err := e.save(t); err != nil {
		return err
	}
	e.tellCEO(ctx, fmt.Sprintf("merge_train: %s train %s %s; held pull requests: %s", t.Repository, t.ID, note, t.numbers(t.carsIn(CarRiding))))
	return nil
}
