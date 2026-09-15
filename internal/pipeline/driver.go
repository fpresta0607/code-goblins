package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
var ErrNoNativeRunAtWorktree = errors.New("pipeline: no active native run owns the agent worktree")

// terminalRunStatus is the set of native run statuses that are finished. Idle
// asks the database for the same set; CheckStart asks it about one run.
var terminalRunStatus = map[string]bool{"completed": true, "failed": true, "cancelled": true}

type Reader struct {
	Commands   execx.Runner
	Root       string
	SQLitePath string
}

const NativeVersion = "v1.75.1"
const NativeBuildSHA = "37ed232"

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
	sqlite := r.SQLitePath
	if sqlite == "" {
		sqlite = "sqlite3"
	}
	result, err := r.Commands.Run(ctx, execx.Request{Name: sqlite, Args: []string{"-readonly", "-json", path, sql}})
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

type StartEvidence struct {
	RepoID              string `json:"repo_id"`
	Branch              string `json:"branch"`
	HeadSHA             string `json:"head_sha"`
	DefaultBranch       string `json:"default_branch"`
	TrustedSHA          string `json:"trusted_sha"`
	TaskConfigSHA256    string `json:"task_config_sha256"`
	TrustedConfigSHA256 string `json:"trusted_config_sha256"`
	EffectivePrimary    string `json:"effective_primary"`
}

type NativeLaunchExpectation struct {
	RunID                string
	Project              string
	RepoID               string
	Branch               string
	SubmittedHeadSHA     string
	LaunchNonce          string
	ValidationGeneration string
	TrustedSHA           string
	Primary              string
	PrimaryModel         string
	GlobalConfigSHA256   string
}

type NativeLaunchState struct {
	InvocationCount int
}

type NativeRunContext struct {
	RunID                string `json:"run_id"`
	RepoID               string `json:"repo_id"`
	Project              string `json:"project"`
	DefaultBranch        string `json:"default_branch"`
	Branch               string `json:"branch"`
	HeadSHA              string `json:"head_sha"`
	SubmittedHeadSHA     string `json:"submitted_head_sha"`
	LaunchNonce          string `json:"launch_nonce"`
	ValidationGeneration string `json:"validation_generation"`
	Worktree             string `json:"worktree"`
	NoMistakesVersion    string `json:"no_mistakes_version"`
	NoMistakesBuildSHA   string `json:"no_mistakes_build_sha"`
	InvocationCount      int    `json:"invocation_count"`
}

type NativeAgentExpectation struct {
	RunID                string
	Project              string
	RepoID               string
	Branch               string
	SubmittedHeadSHA     string
	LaunchNonce          string
	ValidationGeneration string
	DefaultBranch        string
	TrustedSHA           string
	TaskConfigSHA256     string
	TrustedConfigSHA256  string
	GlobalConfigSHA256   string
	EffectivePrimary     string
}

func (r Reader) NativeRunAtWorktree(ctx context.Context, worktree string) (NativeRunContext, error) {
	var rows []NativeRunContext
	if err := r.query(ctx, `SELECT runs.id AS run_id, runs.repo_id, repos.working_path AS project,
repos.default_branch, runs.branch, runs.head_sha, COALESCE(runs.submitted_head_sha,'') AS submitted_head_sha,
COALESCE(runs.launch_nonce,'') AS launch_nonce, COALESCE(runs.launch_validation_generation,'') AS validation_generation,
COALESCE(runs.worktree_dir,'') AS worktree,COALESCE(runs.no_mistakes_version,'') AS no_mistakes_version,
COALESCE(runs.no_mistakes_build_sha,'') AS no_mistakes_build_sha,
(SELECT COUNT(*) FROM agent_invocations WHERE agent_invocations.run_id=runs.id) AS invocation_count
FROM runs JOIN repos ON repos.id=runs.repo_id
WHERE runs.status IS NULL OR runs.status NOT IN ('completed','failed','cancelled')
ORDER BY runs.created_at DESC, runs.id DESC`, &rows); err != nil {
		return NativeRunContext{}, err
	}
	var match *NativeRunContext
	for i := range rows {
		if strings.TrimSpace(rows[i].Worktree) == "" || !samePath(rows[i].Worktree, worktree) {
			continue
		}
		if match != nil {
			return NativeRunContext{}, errors.New("pipeline: multiple active native runs own the agent worktree")
		}
		match = &rows[i]
	}
	if match == nil {
		return NativeRunContext{}, ErrNoNativeRunAtWorktree
	}
	return *match, nil
}

func (r Reader) VerifyNativeAgent(ctx context.Context, worktree string, want NativeAgentExpectation) error {
	if want.EffectivePrimary != "codex" {
		return errors.New("pipeline: managed native primary must be codex")
	}
	run, err := r.NativeRunAtWorktree(ctx, worktree)
	if err != nil {
		return err
	}
	if run.RunID != want.RunID || run.RepoID != want.RepoID || !samePath(run.Project, want.Project) || run.DefaultBranch != want.DefaultBranch || run.Branch != want.Branch || run.SubmittedHeadSHA != want.SubmittedHeadSHA || run.LaunchNonce != want.LaunchNonce || run.ValidationGeneration != want.ValidationGeneration || !samePath(run.Worktree, worktree) {
		return errors.New("pipeline: active native run does not match the managed launch contract")
	}
	if run.NoMistakesVersion != NativeVersion || run.NoMistakesBuildSHA != NativeBuildSHA {
		return errors.New("pipeline: native version and build do not match the managed gate")
	}
	config, err := os.ReadFile(filepath.Join(r.Root, "config.yaml"))
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(config)) != want.GlobalConfigSHA256 {
		return errors.New("pipeline: shared native config changed before managed agent launch")
	}
	gitOne := func(dir string, args ...string) (string, error) {
		result, err := r.Commands.Run(ctx, execx.Request{Dir: dir, Name: "git", Args: args})
		fields := strings.Fields(string(result.Stdout))
		if err != nil || result.ExitCode != 0 || len(fields) != 1 {
			return "", errors.New("pipeline: managed agent git evidence is unavailable")
		}
		return fields[0], nil
	}
	head, err := gitOne(worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != run.HeadSHA {
		return errors.New("pipeline: native agent worktree head differs from the durable native run head")
	}
	if head != want.SubmittedHeadSHA {
		if err := r.verifyNativeHeadTransition(ctx, run, want.SubmittedHeadSHA, head); err != nil {
			return err
		}
	}
	for _, dir := range []string{run.Project, worktree} {
		tracked, err := gitOne(dir, "rev-parse", "--verify", "refs/remotes/origin/"+want.DefaultBranch)
		if err != nil || tracked != want.TrustedSHA {
			return errors.New("pipeline: trusted default-branch tracking evidence changed before managed agent launch")
		}
	}
	remote, err := r.originDefaultHead(ctx, run.Project, want.DefaultBranch)
	if err != nil || remote != want.TrustedSHA {
		return errors.New("pipeline: origin default branch changed before managed agent launch")
	}
	for _, check := range []struct {
		sha  string
		want string
		name string
	}{
		{want.SubmittedHeadSHA, want.TaskConfigSHA256, "submitted"},
		{want.TrustedSHA, want.TrustedConfigSHA256, "trusted"},
	} {
		result, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"show", check.sha + ":.no-mistakes.yaml"}})
		if err != nil || result.ExitCode != 0 || fmt.Sprintf("%x", sha256.Sum256(result.Stdout)) != check.want {
			return fmt.Errorf("pipeline: %s repository config changed before managed agent launch", check.name)
		}
	}
	latest, err := r.NativeRunAtWorktree(ctx, worktree)
	if err != nil || latest != run {
		return errors.New("pipeline: active native run changed during managed agent authorization")
	}
	latestHead, err := gitOne(worktree, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || latestHead != head || latestHead != latest.HeadSHA {
		return errors.New("pipeline: native agent worktree changed during managed agent authorization")
	}
	return nil
}

func (r Reader) verifyNativeHeadTransition(ctx context.Context, run NativeRunContext, submitted, head string) error {
	ancestor, err := r.Commands.Run(ctx, execx.Request{Dir: run.Worktree, Name: "git", Args: []string{"merge-base", "--is-ancestor", submitted, head}})
	descendant := err == nil && ancestor.ExitCode == 0
	var rows []struct {
		Fix    int `json:"fix"`
		Rebase int `json:"rebase"`
	}
	sql := `SELECT
EXISTS(SELECT 1 FROM uncertified_pipeline_ranges WHERE repo_id=` + sqlString(run.RepoID) + ` AND branch=` + sqlString(run.Branch) + ` AND source_run_id=` + sqlString(run.RunID) + ` AND to_sha=` + sqlString(head) + `) AS fix,
EXISTS(SELECT 1 FROM step_results WHERE run_id=` + sqlString(run.RunID) + ` AND step_name='rebase' AND status='completed') AND NOT EXISTS(SELECT 1 FROM agent_invocations WHERE run_id=` + sqlString(run.RunID) + ` AND step_name<>'rebase') AS rebase`
	if queryErr := r.query(ctx, sql, &rows); queryErr != nil || len(rows) != 1 {
		return errors.New("pipeline: native run head lacks an authorized transition from the managed submitted head")
	}
	authorized := descendant && rows[0].Fix == 1 || rows[0].Rebase == 1
	if !authorized {
		return errors.New("pipeline: native run head lacks an authorized transition from the managed submitted head")
	}
	return nil
}

func (r Reader) VerifyNativeLaunch(ctx context.Context, expected NativeLaunchExpectation) (NativeLaunchState, error) {
	if expected.Primary != "codex" || expected.PrimaryModel != "gpt-5.6-sol" {
		return NativeLaunchState{}, errors.New("pipeline: managed native primary must be Codex gpt-5.6-sol")
	}
	var rows []struct {
		RunID                string `json:"run_id"`
		RepoID               string `json:"repo_id"`
		WorkingPath          string `json:"working_path"`
		Branch               string `json:"branch"`
		SubmittedHeadSHA     string `json:"submitted_head_sha"`
		LaunchNonce          string `json:"launch_nonce"`
		ValidationGeneration string `json:"validation_generation"`
		TrustedSHA           string `json:"trusted_sha"`
		Agent                string `json:"agent"`
		Model                string `json:"model"`
		GlobalConfigHex      string `json:"global_config_hex"`
		NoMistakesVersion    string `json:"no_mistakes_version"`
		NoMistakesBuildSHA   string `json:"no_mistakes_build_sha"`
		InvocationCount      int    `json:"invocation_count"`
	}
	sql := `SELECT runs.id AS run_id,runs.repo_id,repos.working_path,runs.branch,COALESCE(runs.submitted_head_sha,'') AS submitted_head_sha,COALESCE(runs.launch_nonce,'') AS launch_nonce,COALESCE(runs.launch_validation_generation,'') AS validation_generation,COALESCE(runs.no_mistakes_version,'') AS no_mistakes_version,COALESCE(runs.no_mistakes_build_sha,'') AS no_mistakes_build_sha,
COALESCE((SELECT step_rounds.trusted_config_sha FROM step_rounds JOIN step_results ON step_results.id=step_rounds.step_result_id WHERE step_results.run_id=runs.id AND step_rounds.trusted_config_sha IS NOT NULL ORDER BY step_rounds.created_at,step_rounds.id LIMIT 1),'') AS trusted_sha,
COALESCE((SELECT agent_invocations.agent FROM agent_invocations WHERE agent_invocations.run_id=runs.id ORDER BY agent_invocations.started_at,agent_invocations.id LIMIT 1),'') AS agent,
COALESCE((SELECT agent_invocations.model FROM agent_invocations WHERE agent_invocations.run_id=runs.id ORDER BY agent_invocations.started_at,agent_invocations.id LIMIT 1),'') AS model,
COALESCE((SELECT hex(step_rounds.global_config_yaml) FROM step_rounds JOIN step_results ON step_results.id=step_rounds.step_result_id WHERE step_results.run_id=runs.id AND step_results.step_name='review' AND step_rounds.global_config_yaml IS NOT NULL ORDER BY step_rounds.round,step_rounds.created_at,step_rounds.id LIMIT 1),'') AS global_config_hex
	,(SELECT COUNT(*) FROM agent_invocations WHERE agent_invocations.run_id=runs.id) AS invocation_count
FROM runs JOIN repos ON repos.id=runs.repo_id WHERE runs.id=` + sqlString(expected.RunID)
	if err := r.query(ctx, sql, &rows); err != nil {
		return NativeLaunchState{}, err
	}
	if len(rows) != 1 {
		return NativeLaunchState{}, errors.New("pipeline: native launch record is missing or ambiguous")
	}
	row := rows[0]
	if row.NoMistakesVersion != NativeVersion || row.NoMistakesBuildSHA != NativeBuildSHA {
		return NativeLaunchState{}, errors.New("pipeline: native version and build do not match the managed gate")
	}
	if row.RunID != expected.RunID || row.RepoID != expected.RepoID || !samePath(row.WorkingPath, expected.Project) || row.Branch != expected.Branch || row.SubmittedHeadSHA != expected.SubmittedHeadSHA || row.LaunchNonce != expected.LaunchNonce || row.ValidationGeneration != expected.ValidationGeneration {
		return NativeLaunchState{}, errors.New("pipeline: native launch record does not match the checked repository, branch, head, and launch identity")
	}
	state := NativeLaunchState{InvocationCount: row.InvocationCount}
	if row.InvocationCount == 0 {
		return state, nil
	}
	if row.TrustedSHA != expected.TrustedSHA || row.Agent != expected.Primary || row.Model != expected.PrimaryModel {
		return NativeLaunchState{}, errors.New("pipeline: native launch record does not match the checked trusted SHA and primary")
	}
	globalConfig, err := hex.DecodeString(row.GlobalConfigHex)
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(globalConfig)) != expected.GlobalConfigSHA256 {
		return NativeLaunchState{}, errors.New("pipeline: native launch record does not match the checked global config")
	}
	return state, nil
}

func samePath(a, b string) bool {
	a, errA := filepath.Abs(a)
	b, errB := filepath.Abs(b)
	return errA == nil && errB == nil && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func sqlString(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// CheckStart refuses a restart over unresolved work and checks repository
// overrides before the native engine could spend an automatic repair cycle.
func (r Reader) CheckStart(ctx context.Context, project, worktree, branch string, policy Policy) error {
	_, err := r.CheckStartEvidence(ctx, project, worktree, branch, policy)
	return err
}

func (r Reader) CheckStartEvidence(ctx context.Context, project, worktree, branch string, policy Policy) (StartEvidence, error) {
	var repos []struct {
		ID            string `json:"id"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.query(ctx, `SELECT id,default_branch FROM repos WHERE lower(replace(working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`)`, &repos); err != nil {
		return StartEvidence{}, err
	}
	if len(repos) != 1 || repos[0].DefaultBranch == "" || branch == repos[0].DefaultBranch {
		return StartEvidence{}, errors.New("pipeline: registered repository and non-default branch required")
	}
	var previous []struct {
		Status string `json:"status"`
	}
	if err := r.query(ctx, `SELECT runs.status FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`) AND runs.branch=`+sqlString(branch)+` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`, &previous); err != nil {
		return StartEvidence{}, err
	}
	// A terminal run has no budget left to reset, so only a still-live one
	// blocks a restart. The set matches Idle's.
	if len(previous) > 0 && !terminalRunStatus[previous[0].Status] {
		return StartEvidence{}, fmt.Errorf("%w; an earlier run must be resolved, not restarted", ErrUnresolved)
	}
	currentBranch, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"symbolic-ref", "--quiet", "--short", "HEAD"}})
	if err != nil || currentBranch.ExitCode != 0 || strings.TrimSpace(string(currentBranch.Stdout)) != branch {
		return StartEvidence{}, errors.New("pipeline: task branch changed before native invocation")
	}
	status, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil || status.ExitCode != 0 || len(strings.TrimSpace(string(status.Stdout))) != 0 {
		return StartEvidence{}, errors.New("pipeline: commit task work before starting the gate")
	}
	head, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"rev-parse", "--verify", "HEAD^{commit}"}})
	headFields := strings.Fields(string(head.Stdout))
	if err != nil || head.ExitCode != 0 || len(headFields) != 1 {
		return StartEvidence{}, errors.New("pipeline: readable task HEAD required")
	}
	task, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"show", "HEAD:.no-mistakes.yaml"}})
	if err != nil || task.ExitCode != 0 {
		return StartEvidence{}, errors.New("pipeline: readable committed task and origin default-branch .no-mistakes.yaml required")
	}
	if err := checkRepoConfig(task.Stdout, policy, true); err != nil {
		return StartEvidence{}, err
	}
	defaultBranch := repos[0].DefaultBranch
	remoteHead, err := r.originDefaultHead(ctx, project, defaultBranch)
	if err != nil {
		return StartEvidence{}, err
	}
	trustedRef := "refs/remotes/origin/" + defaultBranch
	local, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", trustedRef}})
	localFields := strings.Fields(string(local.Stdout))
	if err != nil || local.ExitCode != 0 || len(localFields) != 1 {
		return StartEvidence{}, errors.New("pipeline: readable origin default-branch tracking evidence is required")
	}
	if localFields[0] != remoteHead {
		return StartEvidence{}, errors.New("pipeline: origin default-branch tracking evidence is stale")
	}
	trusted, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"show", remoteHead + ":.no-mistakes.yaml"}})
	if err != nil || trusted.ExitCode != 0 {
		return StartEvidence{}, errors.New("pipeline: readable committed task and origin default-branch .no-mistakes.yaml required")
	}
	if err := checkRepoConfig(trusted.Stdout, policy, false); err != nil {
		return StartEvidence{}, err
	}
	primary, err := effectivePrimaryAgent(task.Stdout, trusted.Stdout, policy)
	if err != nil {
		return StartEvidence{}, err
	}
	return StartEvidence{
		RepoID:              repos[0].ID,
		Branch:              branch,
		HeadSHA:             headFields[0],
		DefaultBranch:       defaultBranch,
		TrustedSHA:          remoteHead,
		TaskConfigSHA256:    fmt.Sprintf("%x", sha256.Sum256(task.Stdout)),
		TrustedConfigSHA256: fmt.Sprintf("%x", sha256.Sum256(trusted.Stdout)),
		EffectivePrimary:    primary,
	}, nil
}

func (r Reader) originDefaultHead(ctx context.Context, project, defaultBranch string) (string, error) {
	remote, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"ls-remote", "--symref", "origin", "HEAD"}})
	if err != nil || remote.ExitCode != 0 {
		return "", errors.New("pipeline: current origin default-branch evidence is required")
	}
	wantRef := "refs/heads/" + defaultBranch
	seenSymref := false
	head := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(remote.Stdout), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD":
			if seenSymref || fields[1] != wantRef {
				return "", errors.New("pipeline: current origin default-branch evidence is required")
			}
			seenSymref = true
		case len(fields) == 2 && fields[1] == "HEAD":
			if head != "" {
				return "", errors.New("pipeline: current origin default-branch evidence is required")
			}
			head = fields[0]
		}
	}
	if !seenSymref || head == "" {
		return "", errors.New("pipeline: current origin default-branch evidence is required")
	}
	return head, nil
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
		AutoFix map[string]int `yaml:"auto_fix"`
	}
	if err := doc.Decode(&config); err != nil {
		return errors.New("pipeline: invalid repository policy override")
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

func effectivePrimaryAgent(taskData, trustedData []byte, p Policy) (string, error) {
	type repoRouting struct {
		Agent             yaml.Node `yaml:"agent"`
		AllowRepoCommands bool      `yaml:"allow_repo_commands"`
	}
	decode := func(data []byte) (repoRouting, error) {
		doc, err := parseYAML(data)
		if err != nil {
			return repoRouting{}, err
		}
		var config repoRouting
		if err := doc.Decode(&config); err != nil {
			return repoRouting{}, errors.New("pipeline: invalid repository policy override")
		}
		return config, nil
	}
	task, err := decode(taskData)
	if err != nil {
		return "", err
	}
	trusted, err := decode(trustedData)
	if err != nil {
		return "", err
	}
	agent := &trusted.Agent
	source := "trusted default-branch"
	if trusted.AllowRepoCommands {
		agent = &task.Agent
		source = "submitted branch"
	}
	if agent.Kind == 0 {
		return wantPrimary(p), nil
	}
	if agent.Kind == yaml.SequenceNode && len(agent.Content) == 1 {
		agent = agent.Content[0]
	}
	want := wantPrimary(p)
	if agent.Kind != yaml.ScalarNode || agent.Value != want {
		return "", fmt.Errorf("pipeline: effective %s agent overrides the owned %s primary in no-mistakes v1.75.1; remove the effective repository agent field or set it to %s", source, want, want)
	}
	return want, nil
}

func wantPrimary(p Policy) string {
	if p.Version == 2 {
		return p.Primary.Harness
	}
	return p.Reviewer.Harness
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
	emptyActionFixable := gate.Step != "review" && gate.AutoFixLimit != nil && *gate.AutoFixLimit > 0
	actions := map[string]string{}
	unresolved := false
	for _, f := range *report.Findings {
		if _, exists := actions[f.ID]; f.ID == "" || exists {
			return nil, errors.New("pipeline: invalid or duplicate finding ID")
		}
		switch f.Action {
		case "no-op":
		case "auto-fix", "ask-user":
			unresolved = true
		case "":
			if !emptyActionFixable {
				return nil, ErrUnresolved
			}
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
			action, exists := actions[id]
			if id == "" || seen[id] || !exists || action != "auto-fix" && action != "ask-user" && !(emptyActionFixable && action == "") {
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
