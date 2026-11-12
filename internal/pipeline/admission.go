package pipeline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type admission struct {
	PredecessorID         string `json:"predecessor_id"`
	SnapshotDigest        string `json:"snapshot_digest"`
	RepoID                string `json:"repo_id"`
	Branch                string `json:"branch"`
	CallerDir             string `json:"caller_dir"`
	CandidateSHA          string `json:"candidate_sha"`
	CandidateTree         string `json:"candidate_tree"`
	LaunchNonce           string `json:"launch_nonce"`
	ValidationGeneration  string `json:"validation_generation"`
	IntentDigest          string `json:"intent_digest"`
	PublishedHeadSHA      string `json:"published_head_sha"`
	PushGeneration        int64  `json:"push_generation"`
	PushTargetFingerprint string `json:"push_target_fingerprint"`
	ControllerPolicy      struct {
		SHA256       string `json:"sha256"`
		Class        string `json:"class"`
		ReviewCycles int    `json:"review_cycles"`
	} `json:"controller_policy"`
	LaunchAssertion              json.RawMessage   `json:"launch_assertion"`
	HistoricalSourceCompleteness string            `json:"historical_source_completeness"`
	Reason                       string            `json:"reason"`
	TerminalGeneration           string            `json:"terminal_generation,omitempty"`
	AcceptUnknownSource          bool              `json:"accept_unknown_source,omitempty"`
	SourceAnchors                map[string]string `json:"source_anchors,omitempty"`
}

// AdmissionBranch binds an exact predecessor to the registered project. Unlike
// RunBranch it permits observing an admitted child's predecessor again; native
// admission owns latest-owner and identical-replay checks atomically.
func (reader Reader) AdmissionBranch(ctx context.Context, project, predecessor string) (string, string, error) {
	if !gateIdentity.MatchString(predecessor) {
		return "", "", errors.New("pipeline: invalid admission predecessor identity")
	}
	var rows []struct {
		RepoID string `json:"repo_id"`
		ID     string `json:"id"`
		Branch string `json:"branch"`
	}
	query := `SELECT runs.repo_id,runs.id,runs.branch FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.id=` + sqlString(predecessor)
	if err := reader.query(ctx, query, &rows); err != nil {
		return "", "", err
	}
	if len(rows) != 1 || rows[0].ID != predecessor || !gateIdentity.MatchString(rows[0].RepoID) || rows[0].Branch == "" {
		return "", "", errors.New("pipeline: admission predecessor does not identify one run in the task project")
	}
	return rows[0].Branch, rows[0].RepoID, nil
}

// Admit passes a byte-identical private snapshot to native validation, so a
// replaced controller file cannot change the policy or source after our check.
// Native owns immutable history, launch/publication proof and replay semantics.
func (reader Reader) Admit(ctx context.Context, project, worktree, branch, operation, path string, selection Selection, env []string) (execx.Result, string, error) {
	if operation != "validate-authored" && operation != "validate-terminal" {
		return execx.Result{}, "", errors.New("pipeline: unsupported admission operation")
	}
	file, err := fsx.Open(path)
	if err != nil {
		return execx.Result{}, "", fmt.Errorf("pipeline: read admission: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 {
		return execx.Result{}, "", errors.New("pipeline: admission must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return execx.Result{}, "", err
	}
	if len(data) > 65536 || !utf8.Valid(data) {
		return execx.Result{}, "", errors.New("pipeline: admission must be UTF-8 JSON of at most 64 KiB")
	}
	var request admission
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return execx.Result{}, "", fmt.Errorf("pipeline: invalid admission: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return execx.Result{}, "", errors.New("pipeline: admission has trailing input")
	}
	policy := request.ControllerPolicy
	if policy.SHA256 != selection.Hash || policy.Class != selection.Class || policy.ReviewCycles != selection.ReviewCycles {
		return execx.Result{}, "", errors.New("pipeline: admission controller policy differs from the task's frozen assertion")
	}
	if request.HistoricalSourceCompleteness != "unknown" {
		return execx.Result{}, "", errors.New("pipeline: historical source completeness must remain unknown")
	}
	if (operation == "validate-terminal") != (request.TerminalGeneration != "") || (operation == "validate-terminal" && (!request.AcceptUnknownSource || request.TerminalGeneration == request.ValidationGeneration)) {
		return execx.Result{}, "", errors.New("pipeline: terminal admission requires explicit unknown-source acceptance and an independent terminal generation; authored admission cannot select terminal validation")
	}
	predecessorBranch, repository, err := reader.AdmissionBranch(ctx, project, request.PredecessorID)
	if err != nil {
		return execx.Result{}, "", err
	}
	if predecessorBranch != branch || request.Branch != branch || request.RepoID != repository {
		return execx.Result{}, "", errors.New("pipeline: admission repository or branch differs from the task predecessor")
	}
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		return execx.Result{}, "", err
	}
	if request.CallerDir != caller {
		return execx.Result{}, "", errors.New("pipeline: admission caller must be the exact canonical task worktree")
	}
	checks := []struct {
		args []string
		want string
	}{
		{[]string{"symbolic-ref", "--quiet", "--short", "HEAD"}, branch},
		{[]string{"rev-parse", "HEAD"}, request.CandidateSHA},
		{[]string{"rev-parse", "HEAD^{tree}"}, request.CandidateTree},
		{[]string{"status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=none"}, ""},
	}
	if !gateCommit.MatchString(request.CandidateSHA) || !gateCommit.MatchString(request.CandidateTree) {
		return execx.Result{}, "", errors.New("pipeline: admission requires full candidate commit and tree identities")
	}
	for _, check := range checks {
		result, err := reader.runGit(ctx, caller, false, check.args...)
		if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != check.want {
			return execx.Result{}, "", fmt.Errorf("pipeline: admission candidate changed or is not clean (%s)", strings.Join(check.args, " "))
		}
	}
	snapshot, err := os.CreateTemp("", "goblins-admission-*.json")
	if err != nil {
		return execx.Result{}, "", err
	}
	defer func() { _ = os.Remove(snapshot.Name()) }()
	if _, err := snapshot.Write(data); err != nil {
		_ = snapshot.Close()
		return execx.Result{}, "", err
	}
	if err := snapshot.Close(); err != nil {
		return execx.Result{}, "", err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	result, err := reader.Commands.Run(ctx, execx.Request{Dir: caller, Env: env, Name: "no-mistakes", Args: []string{"axi", operation, "--admission-file", snapshot.Name()}})
	return result, digest, err
}
