package pipeline

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
)

// ClosingIssues are the issues every gate run of branch in project was
// started to close, as goblins pipeline run --closes handed them to the gate
// and the gate keeps them: each N or owner/name#N, in the order the runs
// started, each once. A branch whose runs closed none has none.
func (r Reader) ClosingIssues(ctx context.Context, project, branch string) ([]string, error) {
	var rows []struct {
		Refs string `json:"refs"`
	}
	sql := `SELECT COALESCE(runs.closing_issue_refs,'') AS refs FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at, runs.id`
	if err := r.query(ctx, sql, &rows); err != nil {
		return nil, err
	}
	var issues []string
	for _, row := range rows {
		for _, issue := range strings.Split(row.Refs, "\n") {
			if issue = strings.TrimSpace(issue); issue != "" && !slices.Contains(issues, issue) {
				issues = append(issues, issue)
			}
		}
	}
	return issues, nil
}
