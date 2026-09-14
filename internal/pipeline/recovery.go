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
	if status, output, err := r.readRecoverySync(ctx, worktree, env); err == nil {
		switch {
		case status.recoverableUserOwned(run):
			return r.alignReturned(ctx, worktree, branch, env, run, status, output, false)
		case status.recoverableCustodyReturned(run):
			return r.alignReturned(ctx, worktree, branch, env, run, status, output, true)
		}
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
		if err := r.requireCommit(ctx, bare, head, true); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: recovery commit is unavailable: %w", err)
		}
	}

	if run.RecordedHead != run.SubmittedHead {
		safe, err := r.safeRecoveryRelation(ctx, bare, run.RecordedHead, run.SubmittedHead)
		if err != nil {
			return RecoveryResult{}, err
		}
		if !safe {
			return RecoveryResult{}, errors.New("pipeline: recorded head is not an ancestor of the submitted head")
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
	if err := r.requireReturnedCustody(ctx, worktree, bare, env, run); err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{RunID: run.RunID, Head: run.SubmittedHead, NativeOutput: output}, nil
}

func (r Reader) alignReturned(ctx context.Context, worktree, branch string, env []string, run recoveryRecord, status recoverySync, output []byte, custodyReturned bool) (RecoveryResult, error) {
	if err := r.requireCleanHead(ctx, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if !custodyReturned && run.RecordedHead != run.SubmittedHead {
		return RecoveryResult{}, errors.New("pipeline: user-owned recovery has inconsistent recorded and submitted heads")
	}
	bare := filepath.Join(r.Root, "repos", run.RepoID+".git")
	gateRef := "refs/heads/" + branch
	anchor := "refs/no-mistakes/recovery/" + run.RunID + "/gate"
	headsAligned := status.Local.Head == run.RecordedHead && status.Local.Head == run.SubmittedHead
	var cfoGateHead string
	var cfoSwap bool
	if headsAligned {
		var err error
		cfoGateHead, cfoSwap, err = r.recoveryHead(ctx, bare, anchor, true)
		if err != nil {
			return RecoveryResult{}, err
		}
		if cfoSwap {
			if cfoGateHead == status.Local.Head {
				return RecoveryResult{}, errors.New("pipeline: CFO recovery gate anchor does not preserve a stale head")
			}
			if err := r.requireCommit(ctx, bare, cfoGateHead, true); err != nil {
				return RecoveryResult{}, errors.New("pipeline: CFO recovery gate anchor commit is unavailable")
			}
		}
	}
	if custodyReturned {
		nativeAnchor := "refs/no-mistakes/recover/" + run.RunID
		if headsAligned {
			nativeHead, err := r.nativeRecoveryHead(ctx, worktree, bare, nativeAnchor)
			if err != nil {
				return RecoveryResult{}, fmt.Errorf("pipeline: returned pipeline head is not anchored: %w", err)
			}
			if !cfoSwap && nativeHead != run.RecordedHead {
				return RecoveryResult{}, errors.New("pipeline: native-aligned returned custody has conflicting recovery evidence")
			}
		} else if err := r.requireNativeRecoveryHead(ctx, worktree, bare, nativeAnchor, run.RecordedHead); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: returned pipeline head is not anchored: %w", err)
		}
	}
	if headsAligned {
		if err := r.requireRef(ctx, bare, gateRef, status.Local.Head); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: recovered gate branch is unsafe: %w", err)
		}
		return RecoveryResult{RunID: run.RunID, Head: status.Local.Head, NativeOutput: output}, nil
	}
	for _, head := range []string{run.SubmittedHead, status.Local.Head} {
		if err := r.requireCommit(ctx, worktree, head, false); err != nil {
			return RecoveryResult{}, fmt.Errorf("pipeline: local recovery commit is unavailable: %w", err)
		}
	}
	gateHead, err := r.readRef(ctx, bare, gateRef)
	if err != nil {
		return RecoveryResult{}, err
	}
	switch gateHead {
	case run.SubmittedHead:
		if err := r.preserveRecoveryHead(ctx, bare, anchor, run.SubmittedHead); err != nil {
			return RecoveryResult{}, err
		}
	case status.Local.Head:
		if err := r.requireRecoveryHead(ctx, bare, anchor, run.SubmittedHead); err != nil {
			return RecoveryResult{}, errors.New("pipeline: advanced recovery gate lacks its stale-head anchor")
		}
	default:
		return RecoveryResult{}, errors.New("pipeline: recovered gate branch is unsafe")
	}
	if err := r.fetchRecoveryHead(ctx, bare, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.requireCleanHead(ctx, worktree, status.Local.Head); err != nil {
		return RecoveryResult{}, err
	}
	if err := r.requireRef(ctx, bare, gateRef, gateHead); err != nil {
		return RecoveryResult{}, fmt.Errorf("pipeline: gate branch changed during recovery: %w", err)
	}
	if err := r.requireRecoveryHead(ctx, bare, anchor, run.SubmittedHead); err != nil {
		return RecoveryResult{}, errors.New("pipeline: CFO recovery gate anchor changed before head alignment")
	}
	movedGate := gateHead == run.SubmittedHead
	if movedGate {
		if err := r.moveRef(ctx, bare, gateRef, status.Local.Head, run.SubmittedHead); err != nil {
			return RecoveryResult{}, err
		}
	}
	if swapErr := r.swapRunHeads(ctx, run, run.RecordedHead, run.SubmittedHead, status.Local.Head, status.Local.Head, false); swapErr != nil {
		committed, verifyErr := r.runHeadSwapCommitted(ctx, run, status.Local.Head)
		if verifyErr != nil {
			return RecoveryResult{}, errors.Join(swapErr, verifyErr)
		}
		if !committed {
			if movedGate {
				return RecoveryResult{}, errors.Join(swapErr, r.moveRef(ctx, bare, gateRef, run.SubmittedHead, status.Local.Head))
			}
			return RecoveryResult{}, swapErr
		}
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
	recovered := post.userOwned(aligned, true)
	if custodyReturned {
		recovered = post.custodyReturned(aligned, true)
	}
	if err != nil || !recovered {
		if err == nil {
			err = errors.New("pipeline: native engine did not recognize the aligned recovery state")
		}
		return RecoveryResult{}, errors.Join(err, r.rollbackUserOwned(ctx, bare, gateRef, run, status.Local.Head))
	}
	return RecoveryResult{RunID: run.RunID, Head: status.Local.Head, NativeOutput: postOutput}, nil
}

func (r Reader) requireReturnedCustody(ctx context.Context, worktree, bare string, env []string, run recoveryRecord) error {
	status, _, err := r.readRecoverySync(ctx, worktree, env)
	if err != nil {
		return errors.New("pipeline: native custody status check failed")
	}
	expected := run
	expected.RecordedHead = run.SubmittedHead
	if status.userOwned(expected, true) {
		return nil
	}
	var rows []recoveryRecord
	query := `SELECT id AS run_id, repo_id, branch, status, head_sha AS recorded_head, COALESCE(submitted_head_sha,'') AS submitted_head, COALESCE(last_pushed_sha,'') AS pushed_head, COALESCE(custody_returned_at,0) AS custody_returned_at FROM runs WHERE id=` + sqlString(run.RunID) + ` AND repo_id=` + sqlString(run.RepoID) + ` AND branch=` + sqlString(run.Branch)
	if err := r.query(ctx, query, &rows); err != nil || len(rows) != 1 {
		return errors.New("pipeline: native engine did not prove returned custody")
	}
	current := rows[0]
	if current.Status != run.Status || current.RecordedHead != expected.RecordedHead || current.SubmittedHead != expected.SubmittedHead || current.PushedHead != "" || !status.custodyReturned(current, true) {
		return errors.New("pipeline: native engine did not prove returned custody")
	}
	if _, exists, err := r.recoveryHead(ctx, bare, "refs/no-mistakes/recovery/"+run.RunID+"/gate", true); err != nil {
		return err
	} else if exists {
		return errors.New("pipeline: native custody return conflicts with CFO swap evidence")
	}
	if err := r.requireNativeRecoveryHead(ctx, worktree, bare, "refs/no-mistakes/recover/"+run.RunID, current.RecordedHead); err != nil {
		return fmt.Errorf("pipeline: returned pipeline head is not anchored: %w", err)
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

func (s recoverySync) recoverableUserOwned(run recoveryRecord) bool {
	return s.State == "user_owned" && s.Safety == "user_owned" && s.Local.Clean && s.Local.Head != "" && s.Pipeline.Run == run.RunID && s.Pipeline.SubmittedHead == run.SubmittedHead && (s.Pipeline.CurrentHead == run.SubmittedHead || s.Pipeline.CurrentHead == s.Local.Head)
}

func (s recoverySync) custodyReturned(run recoveryRecord, exactLocal bool) bool {
	if s.State != "custody_returned" || s.Safety != "custody_returned" || !s.Local.Clean || s.Local.Head == "" || s.Pipeline.Run != run.RunID || s.Pipeline.SubmittedHead != run.SubmittedHead || s.Pipeline.CurrentHead != run.RecordedHead || run.CustodyReturnedAt == 0 {
		return false
	}
	return !exactLocal || s.Local.Head == run.SubmittedHead && s.Local.Head == run.RecordedHead
}

func (s recoverySync) recoverableCustodyReturned(run recoveryRecord) bool {
	return s.custodyReturned(run, false)
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

func (r Reader) runGit(ctx context.Context, repo string, bare bool, args ...string) (execx.Result, error) {
	if bare {
		args = append([]string{"--git-dir=" + repo}, args...)
	}
	return r.Commands.Run(ctx, execx.Request{Dir: repo, Name: "git", Args: args})
}

func (r Reader) requireRef(ctx context.Context, repo, ref, want string) error {
	got, err := r.readRef(ctx, repo, ref)
	if err != nil || got != want {
		return fmt.Errorf("%s does not equal %s", ref, want)
	}
	return nil
}

func (r Reader) readRef(ctx context.Context, repo, ref string) (string, error) {
	result, err := r.runGit(ctx, repo, true, "rev-parse", ref)
	if err != nil || result.ExitCode != 0 {
		return "", fmt.Errorf("pipeline: cannot read %s", ref)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func (r Reader) requireCommit(ctx context.Context, repo, head string, bare bool) error {
	result, err := r.runGit(ctx, repo, bare, "cat-file", "-e", head+"^{commit}")
	if err != nil || result.ExitCode != 0 {
		return errors.New(head)
	}
	return nil
}

func (r Reader) safeRecoveryRelation(ctx context.Context, repo, recorded, submitted string) (bool, error) {
	ancestry, err := r.runGit(ctx, repo, true, "merge-base", "--is-ancestor", recorded, submitted)
	if err != nil {
		return false, err
	}
	if ancestry.ExitCode == 0 {
		return true, nil
	}
	if ancestry.ExitCode != 1 {
		return false, errors.New("pipeline: could not establish recovery ancestry")
	}
	return false, nil
}

func (r Reader) fetchRecoveryHead(ctx context.Context, bare, worktree, head string) error {
	result, err := r.runGit(ctx, bare, true, "fetch", "--no-tags", "--no-write-fetch-head", worktree, head)
	if err != nil || result.ExitCode != 0 {
		return errors.New("pipeline: could not import the preserved local commit into the gate repository")
	}
	return r.requireCommit(ctx, bare, head, true)
}

func (r Reader) moveRef(ctx context.Context, repo, ref, next, previous string) error {
	result, err := r.runGit(ctx, repo, true, "update-ref", ref, next, previous)
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("pipeline: compare-and-swap failed for %s", ref)
	}
	return nil
}

func (r Reader) rollbackUserOwned(ctx context.Context, bare, gateRef string, run recoveryRecord, alignedHead string) error {
	if err := r.swapRunHeads(ctx, run, alignedHead, alignedHead, run.RecordedHead, run.SubmittedHead, false); err != nil {
		return err
	}
	return r.moveRef(ctx, bare, gateRef, run.SubmittedHead, alignedHead)
}

func (r Reader) recoveryHead(ctx context.Context, repo, ref string, bare bool) (string, bool, error) {
	symbolic, err := r.runGit(ctx, repo, bare, "symbolic-ref", "--quiet", ref)
	if err != nil {
		return "", false, err
	}
	if symbolic.ExitCode == 0 {
		return "", false, errors.New("pipeline: recovery anchor must not be symbolic")
	}
	if symbolic.ExitCode != 1 {
		return "", false, errors.New("pipeline: could not inspect recovery anchor")
	}
	result, err := r.runGit(ctx, repo, bare, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return "", false, err
	}
	if result.ExitCode == 1 {
		return "", false, nil
	}
	head := strings.TrimSpace(string(result.Stdout))
	if result.ExitCode != 0 || head == "" {
		return "", false, errors.New("pipeline: could not inspect recovery anchor")
	}
	target, err := r.runGit(ctx, repo, bare, "cat-file", "-t", head)
	if err != nil || target.ExitCode != 0 || strings.TrimSpace(string(target.Stdout)) != "commit" {
		return "", false, errors.New("pipeline: recovery anchor must name a commit directly")
	}
	return head, true, nil
}

func (r Reader) nativeRecoveryHead(ctx context.Context, worktree, bare, ref string) (string, error) {
	var anchored string
	for i, repo := range []string{worktree, bare} {
		head, found, err := r.recoveryHead(ctx, repo, ref, i == 1)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		if anchored != "" && anchored != head {
			return "", errors.New("pipeline: native recovery anchors conflict across repositories")
		}
		anchored = head
	}
	if anchored == "" {
		return "", errors.New("pipeline: native recovery anchor is missing")
	}
	return anchored, nil
}

func (r Reader) requireNativeRecoveryHead(ctx context.Context, worktree, bare, ref, want string) error {
	head, err := r.nativeRecoveryHead(ctx, worktree, bare, ref)
	if err != nil {
		return err
	}
	if head != want {
		return fmt.Errorf("%s does not equal %s", ref, want)
	}
	return nil
}

func (r Reader) requireRecoveryHead(ctx context.Context, repo, ref, want string) error {
	head, found, err := r.recoveryHead(ctx, repo, ref, true)
	if err != nil {
		return err
	}
	if !found || head != want {
		return fmt.Errorf("%s does not equal %s", ref, want)
	}
	return nil
}

func (r Reader) preserveRecoveryHead(ctx context.Context, repo, ref, head string) error {
	existing, found, err := r.recoveryHead(ctx, repo, ref, true)
	if err != nil {
		return err
	}
	if found {
		if existing != head {
			return errors.New("pipeline: recovery anchor already names another commit")
		}
		return nil
	}
	created, err := r.runGit(ctx, repo, true, "update-ref", "--no-deref", ref, head, strings.Repeat("0", len(head)))
	if err != nil || created.ExitCode != 0 {
		return errors.New("pipeline: could not anchor the stale recorded commit")
	}
	return r.requireRecoveryHead(ctx, repo, ref, head)
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

func (r Reader) runHeadSwapCommitted(ctx context.Context, run recoveryRecord, newHead string) (bool, error) {
	var rows []recoveryRecord
	query := `SELECT id AS run_id, repo_id, branch, status, head_sha AS recorded_head, COALESCE(submitted_head_sha,'') AS submitted_head, COALESCE(last_pushed_sha,'') AS pushed_head, COALESCE(custody_returned_at,0) AS custody_returned_at FROM runs WHERE id=` + sqlString(run.RunID) + ` AND repo_id=` + sqlString(run.RepoID) + ` AND branch=` + sqlString(run.Branch)
	if err := r.query(ctx, query, &rows); err != nil || len(rows) != 1 {
		return false, errors.Join(errors.New("pipeline: recovery metadata state is uncertain; advanced gate preserved for retry"), err)
	}
	current := rows[0]
	if current.Status != run.Status || current.PushedHead != "" {
		return false, errors.New("pipeline: recovery metadata changed concurrently; advanced gate preserved for retry")
	}
	if current.RecordedHead == newHead && current.SubmittedHead == newHead {
		return true, nil
	}
	if current.RecordedHead == run.RecordedHead && current.SubmittedHead == run.SubmittedHead {
		return false, nil
	}
	return false, errors.New("pipeline: recovery metadata changed concurrently; advanced gate preserved for retry")
}
