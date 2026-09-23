package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
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
	var rows []Progress
	q := `SELECT runs.id AS run_id,runs.repo_id,COALESCE(runs.submitted_head_sha,'') AS submitted_head,COALESCE(runs.custody_returned_at,0) AS custody_returned,runs.status,runs.head_sha AS head,COALESCE(runs.review_approved_head_sha,'') AS reviewed_head,COALESCE(runs.last_pushed_sha,'') AS pushed_head,COALESCE(runs.pr_url,'') AS pr,COALESCE(runs.pr_state,'') AS pr_state,COALESCE(runs.ci_ready_at,0) AS ci_ready,COALESCE(runs.terminal_head_verified_at,0) AS terminal_verified FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`
	if err := r.query(ctx, q, &rows); err != nil {
		return Progress{}, err
	}
	if len(rows) != 1 {
		return Progress{}, ErrNoProgress
	}
	p := rows[0]
	if err := r.query(ctx, `SELECT step_name AS name,status FROM step_results WHERE run_id=`+sqlString(p.RunID)+` ORDER BY step_order`, &p.Steps); err != nil {
		return Progress{}, err
	}
	return p, nil
}

// MergedPR is one pull request the gate saw merged.
type MergedPR struct {
	PR      string `json:"pr"`
	Branch  string `json:"branch"`
	Project string `json:"project"`
	At      int64  `json:"at"`
}

// Merged lists the pull requests the gate observed merged since the given
// time, newest first and at most limit, one row per pull request. It reads
// the gate's own state database only, never the forge.
func (r Reader) Merged(ctx context.Context, since time.Time, limit int) ([]MergedPR, error) {
	var rows []MergedPR
	q := `SELECT runs.pr_url AS pr,runs.branch,repos.working_path AS project,MAX(COALESCE(runs.pr_state_observed_at,runs.updated_at)) AS at FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.pr_state='merged' AND COALESCE(runs.pr_url,'')<>'' GROUP BY runs.pr_url HAVING at>=` + strconv.FormatInt(since.Unix(), 10) + ` ORDER BY at DESC LIMIT ` + strconv.Itoa(limit)
	if err := r.query(ctx, q, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// CanSteer is a read-only custody check. A failed or cancelled unpublished
// run remains owned until native branch-sync evidence proves user ownership.
// It never invokes recovery or repairs a branch as a side effect of feedback.
func (r Reader) CanSteer(ctx context.Context, project, worktree, branch string) error {
	p, err := r.Progress(ctx, project, branch)
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
