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
// per harness and pipeline step. Prefixed agent identities (kimi invocations
// are recorded as acp:kimi) are normalized to the bare harness name so one
// harness's rows group together.
const speedTableSQL = `WITH normalized AS (SELECT CASE WHEN instr(agent, ':') > 0 THEN substr(agent, instr(agent, ':') + 1) ELSE agent END AS agent, step_name, duration_ms FROM agent_invocations) SELECT agent, step_name, COUNT(*) AS n, AVG(duration_ms)/60000.0 AS avg_min, MAX(duration_ms)/60000.0 AS max_min FROM normalized GROUP BY agent, step_name ORDER BY agent, step_name`

// queryTimeout bounds one sqlite3 read so a wedged CLI cannot stall spawn or
// doctor after the work is already done.
const queryTimeout = 5 * time.Second

// harnessAvgSQL measures the average invocation minutes for one agent. The
// agent value is single-quote escaped by the caller. Prefixed identities
// (kimi invocations are recorded as acp:kimi) count toward the same harness.
const harnessAvgSQL = `SELECT COUNT(*) AS n, AVG(duration_ms)/60000.0 AS avg_min FROM agent_invocations WHERE agent = '%[1]s' OR agent LIKE '%%:%[1]s'`

// SpeedRow is one agent's measured timing for one pipeline step.
type SpeedRow struct {
	Agent  string
	Step   string
	Count  int
	AvgMin float64
	MaxMin float64
}

// Querier runs read-only speed queries against one state database.
type Querier struct {
	Commands execx.Runner
	DBPath   string
}

// DefaultDBPath is the no-mistakes state database under the user's home.
func DefaultDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".no-mistakes", "state.sqlite")
}

// SpeedTable returns the measured per-agent per-step timing. A non-empty note
// means the table was skipped and explains why.
func (q Querier) SpeedTable(ctx context.Context) ([]SpeedRow, string) {
	out, note := q.query(ctx, speedTableSQL)
	if note != "" {
		return nil, note
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return nil, "telemetry database has no recorded invocations"
	}
	var rows []struct {
		Agent  string  `json:"agent"`
		Step   string  `json:"step_name"`
		Count  int     `json:"n"`
		AvgMin float64 `json:"avg_min"`
		MaxMin float64 `json:"max_min"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, "telemetry response is undecodable"
	}
	table := make([]SpeedRow, 0, len(rows))
	for _, row := range rows {
		table = append(table, SpeedRow{Agent: row.Agent, Step: row.Step, Count: row.Count, AvgMin: row.AvgMin, MaxMin: row.MaxMin})
	}
	if len(table) == 0 {
		return nil, "telemetry database has no recorded invocations"
	}
	return table, ""
}

// HarnessAverage returns the measured average invocation minutes and sample
// count for one agent. ok is false when telemetry is unavailable or the agent
// has no recorded invocations.
func (q Querier) HarnessAverage(ctx context.Context, agent string) (avgMin float64, count int, ok bool) {
	if agent == "" {
		return 0, 0, false
	}
	sql := fmt.Sprintf(harnessAvgSQL, strings.ReplaceAll(agent, "'", "''"))
	out, note := q.query(ctx, sql)
	if note != "" {
		return 0, 0, false
	}
	var rows []struct {
		Count  int      `json:"n"`
		AvgMin *float64 `json:"avg_min"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) != 1 || rows[0].AvgMin == nil || rows[0].Count == 0 {
		return 0, 0, false
	}
	return *rows[0].AvgMin, rows[0].Count, true
}

// SpeedHint renders the one-line measured speed hint for a freshly spawned
// harness, or "" when no measurement is available.
func SpeedHint(ctx context.Context, commands execx.Runner, agent string) string {
	avgMin, count, ok := Querier{Commands: commands, DBPath: DefaultDBPath()}.HarnessAverage(ctx, agent)
	if !ok {
		return ""
	}
	return fmt.Sprintf("speed hint: %s avg %.1f min/invocation across %d measured invocations", agent, avgMin, count)
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
