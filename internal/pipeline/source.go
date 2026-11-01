package pipeline

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type RunSource struct {
	Project  string `json:"project"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
	HeadSHA  string `json:"head,omitempty"`
}

var commandObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// CommandSource verifies the daemon's run identity for either its managed
// checkout or its separate clone of the native-selected Test comparison.
func (reader Reader) CommandSource(ctx context.Context, directory, runID, comparison string) (RunSource, error) {
	if !gateIdentity.MatchString(runID) {
		return RunSource{}, errors.New("pipeline: invalid configured-command run identity")
	}
	if !commandObjectID.MatchString(comparison) || strings.Trim(comparison, "0") == "" {
		return RunSource{}, errors.New("pipeline: the native Test comparison must be a full nonzero 40- or 64-character lowercase hexadecimal object ID")
	}
	var rows []struct {
		RunSource
		Repository string `json:"repository"`
	}
	query := `SELECT repos.working_path AS project,runs.branch,runs.worktree_dir AS worktree,runs.repo_id AS repository,runs.head_sha AS head FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.id=` + sqlString(runID)
	if err := reader.query(ctx, query, &rows); err != nil {
		return RunSource{}, err
	}
	if len(rows) != 1 || !gateIdentity.MatchString(rows[0].Repository) || !commandObjectID.MatchString(rows[0].HeadSHA) || !filepath.IsAbs(rows[0].Project) || rows[0].Branch == "" ||
		!fsx.SamePath(rows[0].Worktree, filepath.Join(reader.Root, "worktrees", rows[0].Repository, runID)) {
		return RunSource{}, errors.New("pipeline: configured-command run source could not be verified")
	}
	source := rows[0]
	managed, err := reader.gateRunWorktree(ctx, runID, source.Repository)
	if err != nil || !fsx.SamePath(managed, source.Worktree) {
		return RunSource{}, fmt.Errorf("pipeline: configured-command managed checkout could not be verified for run %s", runID)
	}
	origin, err := reader.runGit(ctx, source.Project, false, "remote", "get-url", "origin")
	originURL := strings.TrimSpace(string(origin.Stdout))
	if err != nil || origin.ExitCode != 0 || originURL == "" {
		return RunSource{}, errors.New("pipeline: configured-command source origin could not be verified")
	}
	type sourceCheck struct {
		dir    string
		args   []string
		want   string
		isPath bool
	}
	checks := []sourceCheck{
		{managed, []string{"remote", "get-url", "origin"}, originURL, false},
		{managed, []string{"rev-parse", "--verify", "HEAD^{commit}"}, source.HeadSHA, false},
	}
	if !fsx.SamePath(directory, managed) {
		checks = append(checks,
			sourceCheck{directory, []string{"rev-parse", "--show-toplevel"}, directory, true},
			sourceCheck{directory, []string{"remote", "get-url", "origin"}, managed, true},
			sourceCheck{directory, []string{"rev-parse", "--verify", "HEAD^{commit}"}, comparison, false},
		)
	}
	for _, check := range checks {
		result, err := reader.runGit(ctx, check.dir, false, check.args...)
		actual := strings.TrimSpace(string(result.Stdout))
		isMatch := actual != "" && actual == check.want
		if check.isPath {
			isMatch = filepath.IsAbs(actual) && fsx.SamePath(actual, check.want)
		}
		if err != nil || result.ExitCode != 0 || !isMatch {
			return RunSource{}, fmt.Errorf("pipeline: configured-command checkout provenance does not match run %s (%s)", runID, strings.Join(check.args, " "))
		}
	}
	return source.RunSource, nil
}

func (reader Reader) WorktreeSource(ctx context.Context, worktree string) (RunSource, error) {
	runID, repoID := filepath.Base(worktree), filepath.Base(filepath.Dir(worktree))
	if !gateIdentity.MatchString(runID) || !gateIdentity.MatchString(repoID) || !fsx.SamePath(worktree, filepath.Join(reader.Root, "worktrees", repoID, runID)) {
		return RunSource{}, errors.New("pipeline: worktree does not identify a gate run")
	}
	var sources []RunSource
	query := `SELECT repos.working_path AS project,runs.branch,runs.worktree_dir AS worktree FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.id=` + sqlString(runID) + ` AND runs.repo_id=` + sqlString(repoID)
	if err := reader.query(ctx, query, &sources); err != nil {
		return RunSource{}, err
	}
	if len(sources) != 1 || !filepath.IsAbs(sources[0].Project) || sources[0].Branch == "" || !fsx.SamePath(sources[0].Worktree, worktree) {
		return RunSource{}, errors.New("pipeline: source of this gate worktree could not be verified")
	}
	return sources[0], nil
}
