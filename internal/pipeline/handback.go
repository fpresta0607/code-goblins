package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type handbackBinding struct {
	RepoID         string `json:"repo_id"`
	RunID          string `json:"run_id"`
	Branch         string `json:"branch"`
	CallerDir      string `json:"caller_dir"`
	CallerHead     string `json:"caller_head"`
	CurrentHead    string `json:"current_head"`
	PublishedHead  string `json:"published_head"`
	PushGeneration int64  `json:"push_generation"`
}

// Handback delegates custody and continuation to the native receipt protocol.
// Its expectation binds the command to the task evidence read here, so native
// cannot silently select a different run or publication after this read.
func (reader Reader) Handback(ctx context.Context, project, worktree, branch, runID, receipt string, shouldRevalidatePublication bool, env []string) (execx.Result, error) {
	if receipt != "" && !gateIdentity.MatchString(receipt) {
		return execx.Result{}, errors.New("pipeline: invalid native handback receipt ID")
	}
	publication := `runs.head_sha AS current_head,runs.last_pushed_sha AS published_head,runs.push_generation`
	join, receiptWhere := "", ""
	if receipt != "" {
		publication = `ci_handbacks.caller_dir,ci_handbacks.released_head AS current_head,ci_handbacks.released_head AS published_head,ci_handbacks.push_generation`
		join = ` JOIN ci_handbacks ON ci_handbacks.run_id=runs.id`
		receiptWhere = ` AND ci_handbacks.id=` + sqlString(receipt)
	}
	var rows []handbackBinding
	query := `SELECT runs.repo_id,runs.id AS run_id,runs.branch,` + publication + ` FROM runs JOIN repos ON repos.id=runs.repo_id` + join + ` WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` AND runs.id=(SELECT latest.id FROM runs latest WHERE latest.repo_id=runs.repo_id AND latest.branch=runs.branch ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1)` + receiptWhere
	if err := reader.query(ctx, query, &rows); err != nil {
		return execx.Result{}, err
	}
	if len(rows) != 1 {
		return execx.Result{}, errors.New("pipeline: expected exactly one latest published run and, for continuation, its exact native receipt")
	}
	binding := rows[0]
	if receipt == "" && gateIdentity.MatchString(binding.RunID) && binding.PublishedHead == "" && binding.PushGeneration == 0 {
		return execx.Result{}, fmt.Errorf("pipeline: run %s never published, so handback, which returns a published run's failed CI, does not apply to it; a run that failed before it published, such as one Review approved whose Test step then failed preparing, has no route on from its reviewed head that keeps its review until fpresta0607/no-mistakes#323 lands, and until then a new run from the task's branch reviews it again", binding.RunID)
	}
	if !gateIdentity.MatchString(binding.RunID) || !gateIdentity.MatchString(binding.RepoID) || binding.Branch != branch || !gateCommit.MatchString(binding.CurrentHead) || !gateCommit.MatchString(binding.PublishedHead) || binding.PushGeneration <= 0 {
		return execx.Result{}, errors.New("pipeline: handback publication identity could not be verified")
	}
	if binding.CurrentHead != binding.PublishedHead {
		return execx.Result{}, errors.New("pipeline: handback requires the exact published managed head")
	}
	if runID != "" && binding.RunID != runID {
		return execx.Result{}, fmt.Errorf("pipeline: run %s is no longer the latest run of %s", runID, branch)
	}
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		return execx.Result{}, err
	}
	if receipt != "" && !fsx.SamePath(binding.CallerDir, caller) {
		return execx.Result{}, errors.New("pipeline: native handback receipt belongs to another caller worktree")
	}
	binding.CallerDir = caller
	currentBranch, err := reader.runGit(ctx, caller, false, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || currentBranch.ExitCode != 0 || strings.TrimSpace(string(currentBranch.Stdout)) != branch {
		return execx.Result{}, errors.New("pipeline: handback caller branch changed")
	}
	head, err := reader.runGit(ctx, caller, false, "rev-parse", "HEAD")
	if err != nil || head.ExitCode != 0 || !gateCommit.MatchString(strings.TrimSpace(string(head.Stdout))) {
		return execx.Result{}, errors.New("pipeline: handback caller head could not be verified")
	}
	binding.CallerHead = strings.TrimSpace(string(head.Stdout))
	clean, err := reader.runGit(ctx, caller, false, "status", "--porcelain", "--untracked-files=all")
	if err != nil || clean.ExitCode != 0 || len(strings.TrimSpace(string(clean.Stdout))) != 0 {
		return execx.Result{}, errors.New("pipeline: handback requires a readable clean caller worktree")
	}
	file, err := os.CreateTemp("", "goblins-handback-*.json")
	if err != nil {
		return execx.Result{}, err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := json.NewEncoder(file).Encode(binding); err != nil {
		_ = file.Close()
		return execx.Result{}, err
	}
	if err := file.Close(); err != nil {
		return execx.Result{}, err
	}
	args := []string{"axi", "handback", "--expect", file.Name()}
	if shouldRevalidatePublication {
		args = append(args, "--revalidate-publication")
	}
	if receipt != "" {
		args = append(args, "--resume", "--receipt", receipt)
	}
	return reader.Commands.Run(ctx, execx.Request{Dir: caller, Env: env, Name: "no-mistakes", Args: args})
}
