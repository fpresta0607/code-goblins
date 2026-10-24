package train

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

// deployRun is a push run on the base, as gh run list reports it.
type deployRun struct {
	Workflow   string `json:"workflowName"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	HeadSHA    string `json:"headSha"`
	URL        string `json:"url"`
}

// unverifiedDeploy reads the base's newest push runs and returns the deploy
// that keeps the train from merging: a run of a deploy workflow that has not
// finished, or the newest run of one that ended otherwise than successfully.
// A skipped run deployed nothing and is passed over. It returns nil while
// every deploy on the base has finished successfully.
func (e Engine) unverifiedDeploy(ctx context.Context, t Train) (*deployRun, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "run", "list", "--repo", t.Repository, "--branch", t.Base, "--event", "push", "--limit", "50", "--json", "workflowName,status,conclusion,headSha,url")
	if err != nil {
		return nil, err
	}
	var runs []deployRun
	if err := json.Unmarshal([]byte(out), &runs); err != nil {
		return nil, fmt.Errorf("gh listed the push runs of %s in a shape it cannot read: %w", t.Repository, err)
	}
	newest := map[string]bool{}
	for _, run := range runs {
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
// successfully, and says what it waits on in its note and to the CFO, once
// for each thing it waits on: a deploy that ended otherwise than successfully
// waits for a run of it, or of a fix, that passes. The next step reads the
// base's deploys again.
func (e Engine) holdForDeploy(ctx context.Context, t *Train, deploy deployRun) error {
	note := fmt.Sprintf("waits to merge until %s's %s run at %s has finished successfully (%s)", t.Base, deploy.Workflow, short(deploy.HeadSHA), deploy.URL)
	if deploy.Status == "completed" {
		note = fmt.Sprintf("holds its merge, since %s's %s run at %s concluded %s (%s): run it again or land its fix, and the train merges once %s's deploy has finished successfully", t.Base, deploy.Workflow, short(deploy.HeadSHA), deploy.Conclusion, deploy.URL, t.Base)
	}
	if note == t.Note {
		return nil
	}
	t.Note = note
	if err := e.save(t); err != nil {
		return err
	}
	e.tellCFO(ctx, fmt.Sprintf("merge_train: %s train %s %s", t.Repository, t.ID, note))
	return nil
}
