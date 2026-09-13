package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"gopkg.in/yaml.v3"
)

type RecoveryResult struct {
	RunID        string
	Head         string
	NativeOutput []byte
}

type recoveryRecord struct {
	RunID             string `json:"run_id"`
	RepoID            string `json:"repo_id"`
	Branch            string `json:"branch"`
	Status            string `json:"status"`
	RecordedHead      string `json:"recorded_head"`
	SubmittedHead     string `json:"submitted_head"`
	PushedHead        string `json:"pushed_head"`
	CustodyReturnedAt int64  `json:"custody_returned_at"`
}

type recoverySync struct {
	State  string `yaml:"state"`
	Safety string `yaml:"safety"`
	Local  struct {
		Head  string `yaml:"head"`
		Clean bool   `yaml:"clean"`
	} `yaml:"local"`
	Pipeline struct {
		Run           string `yaml:"run"`
		SubmittedHead string `yaml:"submitted_head"`
		CurrentHead   string `yaml:"current_head"`
	} `yaml:"pipeline"`
}

func (r Reader) RecoverKeepLocal(ctx context.Context, project, worktree, branch string, env []string) (RecoveryResult, error) {
	var runs []recoveryRecord
	query := `SELECT runs.id AS run_id, runs.repo_id, runs.branch, runs.status, runs.head_sha AS recorded_head, COALESCE(runs.submitted_head_sha,'') AS submitted_head, COALESCE(runs.last_pushed_sha,'') AS pushed_head, COALESCE(runs.custody_returned_at,0) AS custody_returned_at FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`
	if err := r.query(ctx, query, &runs); err != nil {
		return RecoveryResult{}, err
	}
	if len(runs) != 1 {
		return RecoveryResult{}, errors.New("pipeline: exactly one latest run is required for custody recovery")
	}
	run := runs[0]
	if run.Branch != branch || run.RecordedHead == "" || run.SubmittedHead == "" || run.PushedHead != "" || run.Status != "failed" && run.Status != "cancelled" {
		return RecoveryResult{}, errors.New("pipeline: latest run is not an unpublished failed or cancelled custody recovery")
	}
	if status, output, err := r.readRecoverySync(ctx, worktree, env); err == nil && status.userOwned(run, false) {
		return r.alignUserOwned(ctx, worktree, branch, env, run, status, output)
	}
	if run.CustodyReturnedAt != 0 {
		return RecoveryResult{}, errors.New("pipeline: recorded custody return does not match native branch state")
	}

	if err := r.requireCleanHead(ctx, worktree, run.SubmittedHead); err != nil {
		return RecoveryResult{}, err
	}
	bare := filepath.Join(r.Root, "repos", run.RepoID+".git")
	if err := r.requireRef(ctx, bare, "refs/heads/"+branch, run.SubmittedHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: preserved gate branch is unsafe: %w", err)
	}
	for _, head := range []string{run.RecordedHead, run.SubmittedHead} {
		if err := r.requireCommit(ctx, bare, head); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: recovery commit is unavailable: %w", err)
		}
	}

	if run.RecordedHead != run.SubmittedHead {
		safe, err := r.safeRecoveryRelation(ctx, bare, run.RecordedHead, run.SubmittedHead)
		if err != nil {
			return RecoveryResult{}, err
		}
		if !safe {
			return RecoveryResult{}, errors.New("pipeline: recorded and submitted heads are neither ancestral nor stable patch equivalents")
		}
		anchor := "refs/no-mistakes/recovery/" + run.RunID + "/recorded"
		if err := r.preserveRecoveryHead(ctx, bare, anchor, run.RecordedHead); err != nil {
			return RecoveryResult{}, err
		}
		if err := r.requireCleanHead(ctx, worktree, run.SubmittedHead); err != nil {
			return RecoveryResult{}, err
		}
		if err := r.requireRef(ctx, bare, "refs/heads/"+branch, run.SubmittedHead); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: preserved gate branch changed during recovery: %w", err)
		}
		if err := r.replaceRecordedHead(ctx, run); err != nil {
			return RecoveryResult{}, err
		}
	}

	native, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "sync", "--recover", "--keep-local"}})
	output := append(append([]byte(nil), native.Stdout...), native.Stderr...)
	if err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: native custody recovery failed: %w", err)
	}
	if native.ExitCode != 0 {
		return RecoveryResult{}, fmt.Errorf("pipeline: native custody recovery exited %d: %s", native.ExitCode, strings.TrimSpace(string(output)))
	}
	if err := r.requireCleanHead(ctx, worktree, run.SubmittedHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: keep-local recovery changed the task branch: %w", err)
	}
	if err := r.requireRef(ctx, bare, "refs/heads/"+branch, run.SubmittedHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: keep-local recovery changed the gate branch: %w", err)
	}
	if err := r.requireUserOwned(ctx, worktree, env, run); err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{RunID: run.RunID, Head: run.SubmittedHead, NativeOutput: output}, nil
}

func (r Reader) alignUserOwned(ctx context.Context, worktree, branch string, env []string, run recoveryRecord, status recoverySync, output []byte) (RecoveryResult, error) {
	if err := r.requireCleanHead(ctx, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if run.RecordedHead != run.SubmittedHead {
		return RecoveryResult{}, errors.New("pipeline: user-owned recovery has inconsistent recorded and submitted heads")
	}
	bare := filepath.Join(r.Root, "repos", run.RepoID+".git")
	gateRef := "refs/heads/" + branch
	if err := r.requireRef(ctx, bare, gateRef, run.SubmittedHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: recovered gate branch is unsafe: %w", err)
	}
	if status.Local.Head == run.SubmittedHead {
		return RecoveryResult{RunID: run.RunID, Head: status.Local.Head, NativeOutput: output}, nil
	}
	for _, head := range []string{run.SubmittedHead, status.Local.Head} {
		if err := r.requireCommit(ctx, worktree, head); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: local recovery commit is unavailable: %w", err)
		}
	}
	anchor := "refs/no-mistakes/recovery/" + run.RunID + "/gate"
	if err := r.preserveRecoveryHead(ctx, bare, anchor, run.SubmittedHead); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.fetchRecoveryHead(ctx, bare, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.requireCleanHead(ctx, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.requireRef(ctx, bare, gateRef, run.SubmittedHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: gate branch changed during recovery: %w", err)
	}
	if err := r.moveRef(ctx, bare, gateRef, status.Local.Head, run.SubmittedHead); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.swapRunHeads(ctx, run, run.RecordedHead, run.SubmittedHead, status.Local.Head, status.Local.Head, false); err != nil {
		rollbackErr := r.moveRef(ctx, bare, gateRef, run.SubmittedHead, status.Local.Head)
		return RecoveryResult{}, errors.Join(err, rollbackErr)
	}
	aligned := run
	aligned.RecordedHead = status.Local.Head
	aligned.SubmittedHead = status.Local.Head
	if err := r.requireCleanHead(ctx, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, errors.Join(err, r.rollbackUserOwned(ctx, bare, gateRef, run, status.Local.Head))
	}
	if err := r.requireRef(ctx, bare, gateRef, status.Local.Head); err != nil {
		return RecoveryResult{}, errors.Join(err, r.rollbackUserOwned(ctx, bare, gateRef, run, status.Local.Head))
	}
	post, postOutput, err := r.readRecoverySync(ctx, worktree, env)
	if err != nil || !post.userOwned(aligned, true) {
		if err == nil {
			err = errors.New("pipeline: native engine did not recognize the aligned recovery state")
		}
		return RecoveryResult{}, errors.Join(err, r.rollbackUserOwned(ctx, bare, gateRef, run, status.Local.Head))
	}
	return RecoveryResult{RunID: run.RunID, Head: status.Local.Head, NativeOutput: postOutput}, nil
}

func (r Reader) requireUserOwned(ctx context.Context, worktree string, env []string, run recoveryRecord) error {
	status, _, err := r.readRecoverySync(ctx, worktree, env)
	if err != nil {
		return errors.New("pipeline: native custody status check failed")
	}
	if !status.userOwned(run, true) {
		return errors.New("pipeline: native engine did not prove clean user-owned custody")
	}
	return nil
}

func (r Reader) readRecoverySync(ctx context.Context, worktree string, env []string) (recoverySync, []byte, error) {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "sync", "--check"}})
	if err != nil || result.ExitCode != 0 {
		return recoverySync{}, nil, errors.New("pipeline: native custody status check failed")
	}
	var status struct {
		BranchSync recoverySync `yaml:"branch_sync"`
	}
	if yaml.Unmarshal(result.Stdout, &status) != nil {
		return recoverySync{}, nil, errors.New("pipeline: invalid native custody status")
	}
	return status.BranchSync, append(append([]byte(nil), result.Stdout...), result.Stderr...), nil
}

func (s recoverySync) userOwned(run recoveryRecord, exactLocal bool) bool {
	if s.State != "user_owned" || s.Safety != "user_owned" || !s.Local.Clean || s.Local.Head == "" || s.Pipeline.Run != run.RunID || s.Pipeline.SubmittedHead != run.SubmittedHead || s.Pipeline.CurrentHead != run.SubmittedHead {
		return false
	}
	return !exactLocal || s.Local.Head == run.SubmittedHead
}

func (r Reader) requireCleanHead(ctx context.Context, worktree, want string) error {
	status, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil || status.ExitCode != 0 || len(bytes.TrimSpace(status.Stdout)) != 0 {
		return errors.New("pipeline: custody recovery requires a clean task worktree")
	}
	head, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"rev-parse", "HEAD"}})
	if err != nil || head.ExitCode != 0 || strings.TrimSpace(string(head.Stdout)) != want {
		return errors.New("pipeline: local head does not exactly match the expected recovery head")
	}
	return nil
}

func (r Reader) requireRef(ctx context.Context, repo, ref, want string) error {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"rev-parse", ref}})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != want {
		return fmt.Errorf("%s does not equal %s", ref, want)
	}
	return nil
}

func (r Reader) requireCommit(ctx context.Context, repo, head string) error {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"cat-file", "-e", head + "^{commit}"}})
	if err != nil || result.ExitCode != 0 {
		return errors.New(head)
	}
	return nil
}

func (r Reader) safeRecoveryRelation(ctx context.Context, repo, recorded, submitted string) (bool, error) {
	ancestry, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"merge-base", "--is-ancestor", recorded, submitted}})
	if err != nil {
		return false, err
	}
	if ancestry.ExitCode == 0 {
		return true, nil
	}
	if ancestry.ExitCode != 1 {
		return false, errors.New("pipeline: could not establish recovery ancestry")
	}
	recordedParent, err := r.singleParent(ctx, repo, recorded)
	if err != nil {
		return false, err
	}
	submittedParent, err := r.singleParent(ctx, repo, submitted)
	if err != nil {
		return false, err
	}
	recordedPatch, err := r.commitPatchID(ctx, repo, recordedParent, recorded)
	if err != nil {
		return false, err
	}
	submittedPatch, err := r.commitPatchID(ctx, repo, submittedParent, submitted)
	if err != nil {
		return false, err
	}
	return recordedPatch == submittedPatch, nil
}

func (r Reader) singleParent(ctx context.Context, repo, head string) (string, error) {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"rev-list", "--parents", "-n", "1", head}})
	fields := strings.Fields(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || len(fields) != 2 || fields[0] != head {
		return "", errors.New("pipeline: content-equivalent recovery requires single-parent commits")
	}
	return fields[1], nil
}

func (r Reader) commitPatchID(ctx context.Context, repo, parent, head string) (string, error) {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"diff", "--binary", "--full-index", "--no-ext-diff", "--no-renames", parent, head}})
	if err != nil || result.ExitCode != 0 {
		return "", errors.New("pipeline: could not compare recovery commit content")
	}
	patchID, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Stdin: result.Stdout, Name: "git", Args: []string{"patch-id", "--stable"}})
	fields := strings.Fields(string(patchID.Stdout))
	if err != nil || patchID.ExitCode != 0 || len(fields) != 2 {
		return "", errors.New("pipeline: could not identify recovery commit content")
	}
	return fields[0], nil
}

func (r Reader) fetchRecoveryHead(ctx context.Context, bare, worktree, head string) error {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: bare, Name: "git", Args: []string{"fetch", "--no-tags", "--no-write-fetch-head", worktree, head}})
	if err != nil || result.ExitCode != 0 {
		return errors.New("pipeline: could not import the preserved local commit into the gate repository")
	}
	return r.requireCommit(ctx, bare, head)
}

func (r Reader) moveRef(ctx context.Context, repo, ref, next, previous string) error {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"update-ref", ref, next, previous}})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("pipeline: compare-and-swap failed for %s", ref)
	}
	return nil
}

func (r Reader) rollbackUserOwned(ctx context.Context, bare, gateRef string, run recoveryRecord, alignedHead string) error {
	databaseErr := r.swapRunHeads(ctx, run, alignedHead, alignedHead, run.RecordedHead, run.SubmittedHead, false)
	gateErr := r.moveRef(ctx, bare, gateRef, run.SubmittedHead, alignedHead)
	return errors.Join(databaseErr, gateErr)
}

func (r Reader) preserveRecoveryHead(ctx context.Context, repo, ref, head string) error {
	existing, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"rev-parse", "--verify", "--quiet", ref}})
	if err != nil {
		return err
	}
	if existing.ExitCode == 0 {
		if strings.TrimSpace(string(existing.Stdout)) != head {
			return errors.New("pipeline: recovery anchor already names another commit")
		}
		return nil
	}
	if existing.ExitCode != 1 {
		return errors.New("pipeline: could not inspect recovery anchor")
	}
	created, err := r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: []string{"update-ref", ref, head, strings.Repeat("0", 40)}})
	if err != nil || created.ExitCode != 0 {
		return errors.New("pipeline: could not anchor the stale recorded commit")
	}
	return nil
}

func (r Reader) replaceRecordedHead(ctx context.Context, run recoveryRecord) error {
	return r.swapRunHeads(ctx, run, run.RecordedHead, run.SubmittedHead, run.SubmittedHead, run.SubmittedHead, true)
}

func (r Reader) swapRunHeads(ctx context.Context, run recoveryRecord, oldHead, oldSubmitted, newHead, newSubmitted string, requireUnreturned bool) error {
	path := filepath.Join(r.Root, "state.sqlite")
	custody := ""
	if requireUnreturned {
		custody = " AND custody_returned_at IS NULL"
	}
	sql := `BEGIN IMMEDIATE; UPDATE runs SET head_sha=` + sqlString(newHead) + `, submitted_head_sha=` + sqlString(newSubmitted) + `, updated_at=strftime('%s','now') WHERE id=` + sqlString(run.RunID) + ` AND repo_id=` + sqlString(run.RepoID) + ` AND branch=` + sqlString(run.Branch) + ` AND head_sha=` + sqlString(oldHead) + ` AND submitted_head_sha=` + sqlString(oldSubmitted) + ` AND status IN ('failed','cancelled')` + custody + ` AND COALESCE(last_pushed_sha,'')=''; SELECT changes() AS n; COMMIT;`
	result, err := r.Commands.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-cmd", ".timeout 5000", "-json", path, sql}})
	if err != nil || result.ExitCode != 0 {
		return errors.New("pipeline: recovery metadata compare-and-swap failed")
	}
	var rows []struct {
		Count int `json:"n"`
	}
	if json.Unmarshal(result.Stdout, &rows) != nil || len(rows) != 1 || rows[0].Count != 1 {
		return errors.New("pipeline: recovery metadata changed concurrently")
	}
	return nil
}
