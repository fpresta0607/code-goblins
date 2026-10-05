package pipeline

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type RunSource struct {
	Project  string `json:"project"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
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
