package monitor

import (
	"context"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// GateSample is what a goblin's no-mistakes run looks like from the outside:
// whether a run is active, which step it is on, and how long that step has
// been the active one. NoCI records that the worktree has no GitHub Actions
// workflows at all, which is the one shape in which a ci step can never
// complete on its own.
type GateSample struct {
	Active       bool          `json:"active"`
	Step         string        `json:"step"`
	ActiveFor    time.Duration `json:"active_for_ns"`
	LastActivity string        `json:"last_activity"`
	NoCI         bool          `json:"no_ci"`
	Parked       bool          `json:"parked"`
	RunID        string        `json:"run_id"`
	Status       string        `json:"status"`
	ObservedAt   time.Time     `json:"observed_at"`
	Error        string        `json:"error,omitempty"`
	BudgetKnown  bool          `json:"budget_known"`
	ReviewUsed   int           `json:"review_repairs_used"`
	ReviewCap    int           `json:"review_repair_cap"`
	Exhausted    bool          `json:"review_budget_exhausted"`
	ActivePID    int           `json:"active_pid,omitempty"`
}

// GateProber reads validation state independently of worker liveness.
type GateProber interface {
	InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error)
}

// ExecGateProber shells out to `no-mistakes axi status` in the task worktree.
type ExecGateProber struct {
	Commands execx.Runner
	Root     string
	Now      func() time.Time
}

func (p ExecGateProber) InspectGate(ctx context.Context, meta state.TaskMeta) (GateSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	sample := GateSample{}
	if meta.Worktree == "" {
		return sample, nil
	}
	if _, err := os.Stat(filepath.Join(meta.Worktree, ".github", "workflows")); err != nil {
		sample.NoCI = true
	}
	runner := p.Commands
	if runner == nil {
		runner = execx.OSRunner{}
	}
	var env []string
	if p.Root != "" {
		env = append(os.Environ(), "NM_HOME="+p.Root)
	}
	result, err := runner.Run(ctx, execx.Request{Dir: meta.Worktree, Env: env, Name: "no-mistakes", Args: []string{"axi", "status"}})
	if err != nil || result.ExitCode != 0 {
		if err == nil {
			err = errors.New("native status command failed")
		}
		return sample, err
	}
	sample = parseGateStatus(string(result.Stdout), sample)
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	sample.ObservedAt = now().UTC()
	return sample, nil
}

// parseGateStatus reads the active_steps row out of `axi status`. Only the
// active row matters: a completed or pending step is never what a goblin is
// wedged on.
func parseGateStatus(out string, sample GateSample) GateSample {
	var columns []string
	inActive := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(trimmed, "id: "); ok && sample.RunID == "" {
			sample.RunID = strings.Trim(value, "\"")
		}
		if value, ok := strings.CutPrefix(trimmed, "status: "); ok && sample.Status == "" {
			sample.Status = value
		}
		if strings.HasPrefix(trimmed, "awaiting_agent: parked") {
			sample.Parked = true
		}
		if strings.HasPrefix(trimmed, "active_steps[") || strings.HasPrefix(trimmed, "steps[") {
			start, end := strings.Index(trimmed, "{"), strings.Index(trimmed, "}")
			if start >= 0 && end > start {
				columns = strings.Split(trimmed[start+1:end], ",")
			}
			inActive = strings.HasPrefix(trimmed, "active_steps[")
			continue
		}
		if columns == nil {
			continue
		}
		row, err := csv.NewReader(strings.NewReader(trimmed)).Read()
		if err != nil || len(row) != len(columns) {
			columns = nil
			continue
		}
		values := map[string]string{}
		for i, name := range columns {
			values[name] = row[i]
		}
		status := values["status"]
		if status == "awaiting_approval" || status == "fix_review" {
			sample.Parked = true
			sample.Step = values["step"]
		}
		if inActive {
			sample.Active = true
			sample.Step = values["step"]
			sample.LastActivity = values["last_activity"]
			sample.ActiveFor, _ = time.ParseDuration(values["active_for"])
			sample.ActivePID, _ = strconv.Atoi(strings.Trim(values["agent_pid"], `"`))
		}
	}
	if sample.Status == "completed" || sample.Status == "failed" || sample.Status == "cancelled" {
		sample.Parked = false
		sample.Active = false
	}
	return sample
}
