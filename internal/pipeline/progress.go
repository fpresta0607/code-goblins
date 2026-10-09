package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// Progress is read-only, task-branch-bound gate evidence for the supervisor.
// It never changes custody, budgets, findings, or the native run.
type Progress struct {
	RunID            string         `json:"run_id"`
	RepoID           string         `json:"repo_id"`
	SubmittedHead    string         `json:"submitted_head"`
	CustodyReturned  int64          `json:"custody_returned"`
	Status           string         `json:"status"`
	Head             string         `json:"head"`
	ReviewedHead     string         `json:"reviewed_head"`
	PushedHead       string         `json:"pushed_head"`
	PR               string         `json:"pr"`
	PRState          string         `json:"pr_state"`
	CIReady          int64          `json:"ci_ready"`
	TerminalVerified int64          `json:"terminal_verified"`
	Steps            []ProgressStep `json:"steps"`
}

type ProgressStep struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

var ErrNoProgress = errors.New("no gate evidence for this task branch")

func (r Reader) Progress(ctx context.Context, project, branch string) (Progress, error) {
	p, err := r.latestRun(ctx, project, branch)
	if err != nil {
		return Progress{}, err
	}
	if err := r.query(ctx, `SELECT step_name AS name,status FROM step_results WHERE run_id=`+sqlString(p.RunID)+` ORDER BY step_order`, &p.Steps); err != nil {
		return Progress{}, err
	}
	return p, nil
}

// Run is the run with this ID without its steps, which StepDetails reads:
// a gate run its goblin names, wherever it runs.
func (r Reader) Run(ctx context.Context, runID string) (Progress, error) {
	return r.newestRun(ctx, `runs.id=`+sqlString(runID))
}

// latestRun is the branch's newest run without its steps: each query starts
// a sqlite3 process, and custody needs only the run.
func (r Reader) latestRun(ctx context.Context, project, branch string) (Progress, error) {
	return r.newestRun(ctx, `lower(replace(repos.working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`) AND runs.branch=`+sqlString(branch))
}

// newestRun is the newest run that where matches, without its steps.
func (r Reader) newestRun(ctx context.Context, where string) (Progress, error) {
	var rows []Progress
	q := `SELECT runs.id AS run_id,runs.repo_id,COALESCE(runs.submitted_head_sha,'') AS submitted_head,COALESCE(runs.custody_returned_at,0) AS custody_returned,runs.status,runs.head_sha AS head,COALESCE(runs.review_approved_head_sha,'') AS reviewed_head,COALESCE(runs.last_pushed_sha,'') AS pushed_head,COALESCE(runs.pr_url,'') AS pr,COALESCE(runs.pr_state,'') AS pr_state,COALESCE(runs.ci_ready_at,0) AS ci_ready,COALESCE(runs.terminal_head_verified_at,0) AS terminal_verified FROM runs JOIN repos ON repos.id=runs.repo_id WHERE ` + where + ` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`
	if err := r.query(ctx, q, &rows); err != nil {
		return Progress{}, err
	}
	if len(rows) != 1 {
		return Progress{}, ErrNoProgress
	}
	return rows[0], nil
}

// CanSteer is a read-only custody check. A failed or cancelled unpublished
// run remains owned until native branch-sync evidence proves user ownership.
// It never invokes recovery or repairs a branch as a side effect of feedback.
func (r Reader) CanSteer(ctx context.Context, project, worktree, branch string) error {
	p, err := r.latestRun(ctx, project, branch)
	if errors.Is(err, ErrNoProgress) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Status == "completed" {
		return nil
	}
	if (p.Status != "failed" && p.Status != "cancelled") || p.PushedHead != "" {
		return errors.New("pipeline owns this task; use cfo pipeline respond or inspect its delivery evidence")
	}
	status, _, err := r.readRecoverySync(ctx, worktree, nil)
	if err != nil {
		return err
	}
	run := recoveryRecord{RunID: p.RunID, RepoID: p.RepoID, Branch: branch, Status: p.Status, RecordedHead: p.Head, SubmittedHead: p.SubmittedHead, PushedHead: p.PushedHead, CustodyReturnedAt: p.CustodyReturned}
	if status.userOwned(run, false) {
		return nil
	}
	if !status.custodyReturned(run, false) {
		return errors.New("pipeline custody has not been returned; use cfo pipeline recover")
	}
	bare := filepath.Join(r.Root, "repos", p.RepoID+".git")
	return r.requireNativeRecoveryHead(ctx, worktree, bare, "refs/no-mistakes/recover/"+p.RunID, p.Head)
}

func (p Progress) Ready(head string) bool {
	if head == "" || p.Head != head || p.ReviewedHead != head || p.PushedHead != head || p.PR == "" || p.CIReady == 0 || p.Status == "failed" || p.Status == "cancelled" {
		return false
	}
	required := map[string]bool{"review": false, "test": false, "lint": false, "document": false, "push": false, "pr": false}
	for _, step := range p.Steps {
		if _, ok := required[step.Name]; ok {
			if step.Status != "completed" {
				return false
			}
			required[step.Name] = true
		}
		if step.Status == "awaiting_approval" || step.Status == "fix_review" || step.Status == "failed" || step.Status == "fixing" {
			return false
		}
	}
	for _, done := range required {
		if !done {
			return false
		}
	}
	return true
}

// StepDetail is one step of a run with what it is doing: when it started,
// when its latest round started (a fix round runs the step again), its last
// activity and when, and the agent working it.
type StepDetail struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	StartedAt      int64  `json:"started_at"`
	RoundStartedAt int64  `json:"round_started_at"`
	LastActivityAt int64  `json:"last_activity_at"`
	LastActivity   string `json:"last_activity"`
	AgentPID       int    `json:"agent_pid"`
}

// StepDetails reads a run's steps in order with what each is doing. It is a
// query of its own so a no-mistakes database without these columns costs
// only this read, never Progress.
func (r Reader) StepDetails(ctx context.Context, runID string) ([]StepDetail, error) {
	var steps []StepDetail
	q := `SELECT step_name AS name,status,COALESCE(started_at,0) AS started_at,COALESCE(round_started_at,0) AS round_started_at,COALESCE(last_activity_at,0) AS last_activity_at,COALESCE(last_activity,'') AS last_activity,COALESCE(agent_pid,0) AS agent_pid FROM step_results WHERE run_id=` + sqlString(runID) + ` ORDER BY step_order`
	if err := r.query(ctx, q, &steps); err != nil {
		return nil, err
	}
	return steps, nil
}

// UsualStepTimes is how long a round of each step usually runs on this
// machine: the time nine in ten of its completed rounds finished within. A
// step that has never completed has none.
func (r Reader) UsualStepTimes(ctx context.Context) (map[string]time.Duration, error) {
	var rows []struct {
		Name    string `json:"name"`
		Seconds int64  `json:"seconds"`
	}
	q := `SELECT name,MAX(seconds) AS seconds FROM (SELECT step_name AS name,completed_at-COALESCE(NULLIF(round_started_at,0),started_at) AS seconds,PERCENT_RANK() OVER (PARTITION BY step_name ORDER BY completed_at-COALESCE(NULLIF(round_started_at,0),started_at)) AS ranked FROM step_results WHERE status='completed' AND started_at>0 AND completed_at>=COALESCE(NULLIF(round_started_at,0),started_at)) WHERE ranked<=0.9 GROUP BY name`
	if err := r.query(ctx, q, &rows); err != nil {
		return nil, err
	}
	usual := map[string]time.Duration{}
	for _, row := range rows {
		usual[row.Name] = time.Duration(row.Seconds) * time.Second
	}
	return usual, nil
}
