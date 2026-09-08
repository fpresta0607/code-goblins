package pipeline

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
	"gopkg.in/yaml.v3"
)

var ErrUnresolved = errors.New("pipeline: unresolved; a CFO decision is required")

// terminalRunStatus is the set of native run statuses that are finished. Idle
// asks the database for the same set; CheckStart asks it about one run.
var terminalRunStatus = map[string]bool{"completed": true, "failed": true, "cancelled": true}

type Reader struct {
	Commands execx.Runner
	Root     string
}

func DefaultRoot() (string, error) {
	if root := os.Getenv("NM_HOME"); root != "" {
		return filepath.Abs(root)
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".no-mistakes"), nil
}

func (r Reader) query(ctx context.Context, sql string, target interface{}) error {
	path := filepath.Join(r.Root, "state.sqlite")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("pipeline: cannot inspect state database: %w", err)
	}
	if r.Commands == nil {
		return errors.New("pipeline: command runner required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := r.Commands.Run(ctx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", path, sql}})
	if err != nil || result.ExitCode != 0 {
		return errors.New("pipeline: state query failed; sqlite3 and a readable compatible database are required")
	}
	if strings.TrimSpace(string(result.Stdout)) == "" {
		return json.Unmarshal([]byte("[]"), target)
	}
	if err := json.Unmarshal(result.Stdout, target); err != nil {
		return errors.New("pipeline: invalid state query response")
	}
	return nil
}

// Idle takes the native singleton lock first, then proves that no durable run
// is active. It neither stops a daemon nor repairs stale state on the operator's behalf.
func (r Reader) Idle(ctx context.Context) (func() error, error) {
	release, err := lockDaemon(filepath.Join(r.Root, "daemon.lock"))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		Count int `json:"n"`
	}
	err = r.query(ctx, `SELECT COUNT(*) AS n FROM runs WHERE status IS NULL OR status NOT IN ('completed','failed','cancelled')`, &rows)
	if err != nil || len(rows) != 1 || rows[0].Count != 0 {
		releaseErr := release()
		if err == nil {
			err = ErrBusy
		}
		return nil, errors.Join(err, releaseErr)
	}
	return release, nil
}

type Gate struct {
	RunID        string `json:"run_id"`
	StepID       string `json:"step_id"`
	Step         string `json:"step"`
	Status       string `json:"status"`
	Round        int    `json:"round"`
	Selected     string `json:"selected"`
	AutoFixLimit *int   `json:"auto_fix_limit"`
	Findings     string `json:"findings"`
}

func sqlString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// CheckStart refuses a restart over unresolved work and checks repository
// overrides before the native engine could spend an automatic repair cycle.
func (r Reader) CheckStart(ctx context.Context, project, worktree, branch string, policy Policy) error {
	var repos []struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.query(ctx, `SELECT default_branch FROM repos WHERE lower(replace(working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`)`, &repos); err != nil {
		return err
	}
	if len(repos) != 1 || repos[0].DefaultBranch == "" || branch == repos[0].DefaultBranch {
		return errors.New("pipeline: registered repository and non-default branch required")
	}
	var previous []struct {
		Status string `json:"status"`
	}
	if err := r.query(ctx, `SELECT runs.status FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`) AND runs.branch=`+sqlString(branch)+` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`, &previous); err != nil {
		return err
	}
	// A terminal run has no budget left to reset, so only a still-live one
	// blocks a restart. The set matches Idle's.
	if len(previous) > 0 && !terminalRunStatus[previous[0].Status] {
		return fmt.Errorf("%w; an earlier run must be resolved, not restarted", ErrUnresolved)
	}
	status, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil || status.ExitCode != 0 || len(strings.TrimSpace(string(status.Stdout))) != 0 {
		return errors.New("pipeline: commit task work before starting the gate")
	}
	for _, source := range []struct{ dir, ref string }{{worktree, "HEAD"}, {project, "refs/remotes/origin/" + repos[0].DefaultBranch}} {
		result, err := r.Commands.Run(ctx, execx.Request{Dir: source.dir, Name: "git", Args: []string{"show", source.ref + ":.no-mistakes.yaml"}})
		if err != nil || result.ExitCode != 0 {
			return errors.New("pipeline: readable committed task and origin default-branch .no-mistakes.yaml required")
		}
		if err := checkRepoConfig(result.Stdout, policy, source.ref == "HEAD"); err != nil {
			return err
		}
	}
	return nil
}

func CheckRepoConfig(data []byte, p Policy) error {
	return checkRepoConfig(data, p, true)
}

func checkRepoConfig(data []byte, p Policy, checkAutomatic bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	doc, err := parseYAML(data)
	if err != nil {
		return err
	}
	var config struct {
		Agent   yaml.Node      `yaml:"agent"`
		AutoFix map[string]int `yaml:"auto_fix"`
	}
	if err := doc.Decode(&config); err != nil {
		return errors.New("pipeline: invalid repository policy override")
	}
	if config.Agent.Kind != 0 {
		agent := &config.Agent
		if agent.Kind == yaml.SequenceNode && len(agent.Content) == 1 {
			agent = agent.Content[0]
		}
		if agent.Kind != yaml.ScalarNode || agent.Value != "claude" {
			return errors.New("pipeline: repository must use Claude without reviewer fallbacks")
		}
	}
	// Native automatic-fix settings come from the submitted branch, not the
	// trusted default copy. Checking the latter would prevent a safe reduction.
	if !checkAutomatic {
		return nil
	}
	if _, hasCI := config.AutoFix["ci"]; !hasCI {
		if legacy, ok := config.AutoFix["babysit"]; ok {
			config.AutoFix["ci"] = legacy
		}
	}
	for key, want := range map[string]int{"review": p.AutoFix.Review, "test": p.AutoFix.Test, "lint": p.AutoFix.Lint, "rebase": p.AutoFix.Rebase, "ci": p.AutoFix.CI} {
		if got, ok := config.AutoFix[key]; ok && got != want {
			return fmt.Errorf("pipeline: repository auto_fix.%s differs from the task policy", key)
		}
	}
	return nil
}

// Gate reads the latest run for this exact registered project and branch.
// The latest round's selection catches a response accepted before the next round persisted.
func (r Reader) Gate(ctx context.Context, project, branch string) (Gate, error) {
	var rows []Gate
	sql := `WITH latest AS (SELECT runs.id FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at DESC, runs.id DESC LIMIT 1)
SELECT s.run_id, s.id AS step_id, s.step_name AS step, s.status, s.auto_fix_limit, COALESCE(s.findings_json,'') AS findings,
COALESCE((SELECT MAX(round) FROM step_rounds WHERE step_result_id=s.id),0) AS round,
COALESCE((SELECT selection_source FROM step_rounds WHERE step_result_id=s.id ORDER BY round DESC LIMIT 1),'') AS selected
FROM step_results s JOIN latest ON latest.id=s.run_id WHERE s.status IN ('awaiting_approval','fix_review','fixing','running') ORDER BY s.step_order`
	if err := r.query(ctx, sql, &rows); err != nil {
		return Gate{}, err
	}
	if len(rows) != 1 {
		return Gate{}, errors.New("pipeline: expected exactly one current decision gate for this task branch")
	}
	return rows[0], nil
}

type Response struct{ Action, Findings, Instructions string }
type finding struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

func ResponseArgs(s Selection, gate Gate, response Response) ([]string, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if gate.RunID == "" || gate.StepID == "" || gate.Step == "" || gate.Status != "awaiting_approval" && gate.Status != "fix_review" || gate.Round < 1 || gate.Selected != "" {
		return nil, errors.New("pipeline: gate is active, already answered, or lacks durable round evidence")
	}
	// The native engine persists a disabled budget as SQL NULL rather than 0,
	// and Gate only returns a step the engine has already started, so a null
	// limit on a parked review gate means the step is not auto-fixing.
	if gate.Step == "review" && gate.AutoFixLimit != nil && *gate.AutoFixLimit != 0 {
		return nil, errors.New("pipeline: active review limit differs from policy; leave this gate unresolved for the CFO")
	}
	var report struct {
		Findings *[]finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(gate.Findings), &report); err != nil || report.Findings == nil {
		return nil, errors.New("pipeline: missing or invalid findings evidence")
	}
	actions := map[string]string{}
	unresolved := false
	for _, f := range *report.Findings {
		if f.ID == "" || actions[f.ID] != "" {
			return nil, errors.New("pipeline: invalid or duplicate finding ID")
		}
		switch f.Action {
		case "no-op":
		case "auto-fix", "ask-user":
			unresolved = true
		default:
			return nil, ErrUnresolved
		}
		actions[f.ID] = f.Action
	}
	args := []string{"axi", "respond", "--step", gate.Step, "--action", response.Action}
	switch response.Action {
	case "approve":
		if unresolved {
			return nil, ErrUnresolved
		}
		if response.Findings != "" || response.Instructions != "" {
			return nil, errors.New("pipeline: approve does not accept fix arguments")
		}
	case "fix":
		if gate.Step == "review" && gate.Round-1 >= s.ReviewCycles {
			return nil, ErrUnresolved
		}
		if response.Findings == "" {
			return nil, errors.New("pipeline: select concrete finding IDs")
		}
		seen := map[string]bool{}
		for _, id := range strings.Split(response.Findings, ",") {
			action := actions[id]
			if id == "" || seen[id] || action != "auto-fix" && action != "ask-user" {
				return nil, errors.New("pipeline: selected finding is absent, duplicated or not actionable")
			}
			seen[id] = true
		}
		args = append(args, "--findings", response.Findings)
		if response.Instructions != "" {
			args = append(args, "--instructions", response.Instructions)
		}
	default:
		return nil, errors.New("pipeline: only explicit fix or clean approve is supported; no skip or blanket approval")
	}
	return args, nil
}
