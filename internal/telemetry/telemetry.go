// Package telemetry reads measured per-agent timing from the no-mistakes
// state database. The repo has no sqlite driver and takes no new
// dependencies, so reads shell out to the sqlite3 CLI with -readonly; the
// database is never written to, and every failure mode (no database, no
// sqlite3, a locked or unreadable file) degrades to a skip note instead of an
// error.
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// speedTableSQL measures count plus average and maximum invocation minutes
// per harness, recorded model, role, step, and outcome. Outcome is part of the
// grouping so a failed or cancelled call's latency can never be averaged into
// a successful one. Prefixed agent identities (kimi invocations are recorded
// as acp:kimi) are normalized to the bare harness name so one harness's rows
// group together.
const speedTableSQL = `WITH normalized AS (SELECT CASE WHEN instr(agent, ':') > 0 THEN substr(agent, instr(agent, ':') + 1) ELSE agent END AS agent, COALESCE(NULLIF(model, ''), 'unrecorded') AS model, purpose, exit_status, step_name, duration_ms FROM agent_invocations) SELECT agent, model, purpose, exit_status, step_name, COUNT(*) AS n, AVG(duration_ms)/60000.0 AS avg_min, MAX(duration_ms)/60000.0 AS max_min FROM normalized GROUP BY agent, model, purpose, exit_status, step_name ORDER BY agent, model, purpose, step_name, exit_status`

// queryTimeout bounds one sqlite3 read so a wedged CLI cannot stall spawn or
// doctor after the work is already done.
const queryTimeout = 5 * time.Second

// SpeedRow is one agent, model, and role's measured timing for one pipeline
// step and one outcome.
type SpeedRow struct {
	Agent   string
	Model   string
	Role    string
	Outcome string
	Step    string
	Count   int
	AvgMin  float64
	MaxMin  float64
}

// Querier runs read-only speed queries against one state database.
type Querier struct {
	Commands execx.Runner
	DBPath   string
}

// DefaultDBPath is the no-mistakes state database under the user's home.
func DefaultDBPath() string {
	if root := os.Getenv("NM_HOME"); root != "" {
		return filepath.Join(root, "state.sqlite")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".no-mistakes", "state.sqlite")
}

// SpeedTable returns the measured validation timing, one row per agent, model,
// role, step, and outcome. A non-empty note means the table was skipped and
// explains why.
func (q Querier) SpeedTable(ctx context.Context) ([]SpeedRow, string) {
	out, note := q.query(ctx, speedTableSQL)
	if note != "" {
		return nil, note
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, "telemetry database has no recorded invocations"
	}
	var rows []struct {
		Agent   string  `json:"agent"`
		Model   string  `json:"model"`
		Role    string  `json:"purpose"`
		Outcome string  `json:"exit_status"`
		Step    string  `json:"step_name"`
		Count   int     `json:"n"`
		AvgMin  float64 `json:"avg_min"`
		MaxMin  float64 `json:"max_min"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, "telemetry response is undecodable"
	}
	table := make([]SpeedRow, 0, len(rows))
	for _, row := range rows {
		table = append(table, SpeedRow{Agent: row.Agent, Model: row.Model, Role: row.Role, Outcome: row.Outcome, Step: row.Step, Count: row.Count, AvgMin: row.AvgMin, MaxMin: row.MaxMin})
	}
	if len(table) == 0 {
		return nil, "telemetry database has no recorded invocations"
	}
	return table, ""
}

// SpeedHint renders validation outcome counts, explicitly separating them from
// unmeasured implementation speed, including when telemetry is unavailable.
func SpeedHint(ctx context.Context, commands execx.Runner, agent string) string {
	rows, note := (Querier{Commands: commands, DBPath: DefaultDBPath()}).SpeedTable(ctx)
	if note != "" {
		return "speed hint: implementation unmeasured; validation telemetry unavailable"
	}
	return FormatHint(agent, rows)
}

// FormatHint never presents validation or provider failure latency as authoring speed.
func FormatHint(agent string, rows []SpeedRow) string {
	success, failed, cancelled := 0, 0, 0
	for _, row := range rows {
		if row.Agent != agent {
			continue
		}
		switch row.Outcome {
		case "ok":
			success += row.Count
		case "cancelled":
			cancelled += row.Count
		default:
			failed += row.Count
		}
	}
	return fmt.Sprintf("speed hint: %s validation calls: %d successful, %d failed, %d cancelled; implementation unmeasured; doctor splits timing by model and role", agent, success, failed, cancelled)
}

// query runs one read-only statement through the sqlite3 CLI. A non-empty
// note means the query was skipped or failed tolerantly and explains why.
func (q Querier) query(ctx context.Context, sql string) ([]byte, string) {
	if q.Commands == nil {
		return nil, "command runner is required"
	}
	if q.DBPath == "" {
		return nil, "no telemetry database path"
	}
	if _, err := os.Stat(q.DBPath); err != nil {
		return nil, "no telemetry database at " + q.DBPath
	}
	queryCtx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	result, err := q.Commands.Run(queryCtx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", q.DBPath, sql}})
	if err != nil {
		switch {
		case errors.Is(queryCtx.Err(), context.DeadlineExceeded):
			return nil, "telemetry query timed out"
		case errors.Is(queryCtx.Err(), context.Canceled):
			return nil, "telemetry query canceled"
		default:
			return nil, "sqlite3 is not available"
		}
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(string(result.Stderr))
		if detail == "" {
			detail = fmt.Sprintf("exit %d", result.ExitCode)
		}
		return nil, "telemetry database is unreadable: " + detail
	}
	return result.Stdout, ""
}
