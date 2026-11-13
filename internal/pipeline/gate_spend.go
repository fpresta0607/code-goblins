package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// GateCalls is what the agents of one purpose in one gate run spent on one
// harness and model, as the gate's database records each call: its time,
// its tokens, every input token with the cache reads among them, and the
// findings it reported. Untallied counts the calls that recorded no tokens.
type GateCalls struct {
	Run             string `json:"run"`
	Purpose         string `json:"purpose"`
	Agent           string `json:"agent"`
	Model           string `json:"model"`
	Calls           int    `json:"calls"`
	DurationMS      int64  `json:"duration_ms"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	Untallied       int    `json:"untallied"`
	Findings        int    `json:"findings"`
}

// GateTest is one gate run's Test step: its status, empty for a run that
// has none, and the harness and model of the step's last test agent, empty
// when none ran.
type GateTest struct {
	Run    string `json:"run"`
	Status string `json:"status"`
	Agent  string `json:"agent"`
	Model  string `json:"model"`
}

// GateSpend reads what the gate's agents spent in project's runs of branch
// that were created after from and by to, and each of those runs' Test
// step, oldest run first. An agent recorded with a prefix, as acp:kimi, is
// named by its bare harness.
func (r Reader) GateSpend(ctx context.Context, project, branch string, from, to time.Time) ([]GateCalls, []GateTest, error) {
	runs := `SELECT runs.id, runs.created_at FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + fmt.Sprintf(` AND runs.created_at>%d AND runs.created_at<=%d`, from.Unix(), to.Unix())
	agent := `CASE WHEN instr(i.agent, ':') > 0 THEN substr(i.agent, instr(i.agent, ':') + 1) ELSE i.agent END`
	var calls []GateCalls
	if err := r.query(ctx, `WITH measured AS (`+runs+`) SELECT i.run_id AS run, i.purpose AS purpose, `+agent+` AS agent, COALESCE(i.model,'') AS model, COUNT(*) AS calls, SUM(i.duration_ms) AS duration_ms, SUM(COALESCE(i.input_tokens,0)) AS input_tokens, SUM(COALESCE(i.output_tokens,0)) AS output_tokens, SUM(COALESCE(i.cache_read_tokens,0)) AS cache_read_tokens, SUM(i.input_tokens IS NULL) AS untallied, SUM(COALESCE(i.finding_count,0)) AS findings FROM agent_invocations i JOIN measured ON measured.id=i.run_id GROUP BY i.run_id, i.purpose, `+agent+`, COALESCE(i.model,'') ORDER BY MIN(measured.created_at), i.run_id, i.purpose, `+agent+`, COALESCE(i.model,'')`, &calls); err != nil {
		return nil, nil, err
	}
	last := func(column string) string {
		return `COALESCE((SELECT ` + column + ` FROM agent_invocations i WHERE i.run_id=measured.id AND i.purpose='test' ORDER BY i.started_at DESC, i.id DESC LIMIT 1),'')`
	}
	var tests []GateTest
	if err := r.query(ctx, `WITH measured AS (`+runs+`) SELECT measured.id AS run, COALESCE((SELECT s.status FROM step_results s WHERE s.run_id=measured.id AND s.step_name='test' ORDER BY s.step_order DESC, s.id DESC LIMIT 1),'') AS status, `+last(agent)+` AS agent, `+last(`i.model`)+` AS model FROM measured ORDER BY measured.created_at, measured.id`, &tests); err != nil {
		return nil, nil, err
	}
	return calls, tests, nil
}
