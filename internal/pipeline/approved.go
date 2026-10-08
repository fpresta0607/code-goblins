package pipeline

import (
	"context"
	"encoding/json"
)

// ApprovedGateSummaries is what repository gate commands printed at the
// parks a person let through, newest first: the summary of each round
// answered with approve or skip. no-mistakes records approve, skip and abort
// alike as a user_declined round with nothing selected; approve completes
// the step, skip marks it skipped, and abort leaves it unfinished, so the
// step's status tells them apart. A round whose findings are not JSON is
// passed over. The newest 200 are enough for a later run of any live branch
// and keep the heads they name within one command line.
func (r Reader) ApprovedGateSummaries(ctx context.Context) ([]string, error) {
	var rows []struct {
		Findings string `json:"findings"`
	}
	sql := `SELECT rd.findings_json AS findings FROM step_rounds rd JOIN step_results sr ON sr.id=rd.step_result_id WHERE sr.step_name LIKE 'gate.%' AND sr.status IN ('completed','skipped') AND rd.selection_source='user_declined' AND rd.findings_json IS NOT NULL ORDER BY rd.created_at DESC LIMIT 200`
	if err := r.query(ctx, sql, &rows); err != nil {
		return nil, err
	}
	var summaries []string
	for _, row := range rows {
		var findings struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal([]byte(row.Findings), &findings) == nil && findings.Summary != "" {
			summaries = append(summaries, findings.Summary)
		}
	}
	return summaries, nil
}
