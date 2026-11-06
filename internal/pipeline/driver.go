package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"gopkg.in/yaml.v3"
)

var ErrUnresolved = errors.New("pipeline: unresolved; a CFO decision is required")

// terminalRunStatus is the set of native run statuses that are finished. Idle
// asks the database for the same set; CheckStart asks it about one run.
var terminalRunStatus = map[string]bool{"completed": true, "failed": true, "cancelled": true, "ci_monitor_interrupted": true}

type Reader struct {
	Commands execx.Runner
	Root     string
	// Custody keeps CanSteer's verdicts for a reader that lives as long as
	// the board; a nil one checks custody on every read.
	Custody *CustodyCache
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

// stateQueryTimeout bounds one read of the gate's database, so a wedged read
// cannot hold a command forever. The fleet's runners answer a read in process
// (gatedb), in tens of milliseconds; a runner that starts sqlite3 for it
// instead can take tens of seconds on a loaded machine just to start the
// program: a 5 s bound failed `cfo pipeline recover` twice on 2026-10-20 while
// sqlite3 was installed and the database readable (#1286).
const stateQueryTimeout = time.Minute

// query reads the gate's database into target with the read-only query
// sqlite3 -readonly -json makes. A read that fails says what ended it: the
// bound, its caller, a program that could not run, or the database's own
// refusal in its words.
func (r Reader) query(ctx context.Context, sql string, target interface{}) error {
	path := filepath.Join(r.Root, "state.sqlite")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("pipeline: cannot inspect state database: %w", err)
	}
	if r.Commands == nil {
		return errors.New("pipeline: command runner required")
	}
	queryCtx, cancel := context.WithTimeout(ctx, stateQueryTimeout)
	defer cancel()
	result, err := r.Commands.Run(queryCtx, execx.Request{Name: "sqlite3", Args: []string{"-readonly", "-json", path, sql}})
	switch {
	case err != nil && ctx.Err() != nil:
		return fmt.Errorf("pipeline: state query stopped: %w", ctx.Err())
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("pipeline: state query timed out: the gate's database gave no answer within %s", stateQueryTimeout)
	case err != nil:
		return fmt.Errorf("pipeline: state query failed: could not run sqlite3: %w", err)
	case result.ExitCode != 0:
		return fmt.Errorf("pipeline: state query failed: the gate's database refused it: %s", strings.TrimSpace(string(result.Stderr)))
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
// A refusal names the runs in flight it waits for, so nobody stops the daemon under them.
func (r Reader) Idle(ctx context.Context) (func() error, error) {
	release, err := lockDaemon(filepath.Join(r.Root, "daemon.lock"))
	if err != nil {
		runs, queryErr := r.activeRuns(ctx)
		if queryErr != nil {
			return nil, err
		}
		if len(runs) == 0 {
			return nil, fmt.Errorf("%w: the no-mistakes daemon is running and no gate run is in flight", ErrBusy)
		}
		return nil, waitsFor(runs)
	}
	runs, err := r.activeRuns(ctx)
	if err != nil || len(runs) != 0 {
		releaseErr := release()
		if err == nil {
			err = waitsFor(runs)
		}
		return nil, errors.Join(err, releaseErr)
	}
	return release, nil
}

type activeRun struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Status string `json:"status"`
	Repo   string `json:"repo"`
}

func (r Reader) activeRuns(ctx context.Context) ([]activeRun, error) {
	var runs []activeRun
	err := r.query(ctx, `SELECT runs.id AS id, runs.branch AS branch, COALESCE(runs.status,'no status') AS status, COALESCE(repos.working_path,'') AS repo FROM runs LEFT JOIN repos ON repos.id=runs.repo_id WHERE runs.status IS NULL OR runs.status NOT IN ('completed','failed','cancelled','ci_monitor_interrupted') ORDER BY runs.created_at, runs.id`, &runs)
	return runs, err
}

func waitsFor(runs []activeRun) error {
	count := "1 gate run"
	if len(runs) != 1 {
		count = fmt.Sprintf("%d gate runs", len(runs))
	}
	named := make([]string, len(runs))
	for i, run := range runs {
		repo := path.Base(strings.ReplaceAll(run.Repo, `\`, "/"))
		named[i] = fmt.Sprintf("%s (%s %s, %s)", run.ID, repo, run.Branch, run.Status)
	}
	return fmt.Errorf("%w: it waits for %s in flight, %s, and changed nothing", ErrBusy, count, strings.Join(named, ", "))
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

// ErrGateFileRequired is what a gate's start answers for a repository with
// no committed gate file at the task's branch or at origin's default branch.
// `cfo project check` reports a repository with none in these words, so the
// two cannot disagree about whether a gate run starts.
var ErrGateFileRequired = errors.New("pipeline: readable committed task and origin default-branch .no-mistakes.yaml required")

// StartCheck is what a gate start may do: the trusted commit a launch
// selection names, and the live run this start reattaches to when it validates
// the same work.
//
// A run that is still working keeps driving the branch after the native
// bounded wait expires, and reattachment keeps its own intent, head and review
// budget while starting no new run, so it is never the restart an unresolved
// earlier run forbids.
type StartCheck struct {
	// TrustedHead is the head of origin's default branch, which a launch
	// selection names. A reattachment carries no selection.
	TrustedHead string
	// ReattachRun is the live run of this exact work that the start reattaches
	// to, and empty when the start begins a run of its own.
	ReattachRun string
}

// CheckStart refuses a new run over a live one that validates other work, and
// checks repository overrides before the native engine could spend an
// automatic repair cycle. A live run of the same work is reattached to: same
// branch, same head, same intent, no new run and no review budget reset.
func (r Reader) CheckStart(ctx context.Context, project, worktree, branch, intent string, policy Policy) (StartCheck, error) {
	var repos []struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.query(ctx, `SELECT default_branch FROM repos WHERE lower(replace(working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`)`, &repos); err != nil {
		return StartCheck{}, err
	}
	if len(repos) != 1 || repos[0].DefaultBranch == "" || branch == repos[0].DefaultBranch {
		return StartCheck{}, errors.New("pipeline: registered repository and non-default branch required")
	}
	var previous []previousRun
	if err := r.query(ctx, `SELECT runs.id AS id, runs.status AS status, runs.head_sha AS head, COALESCE(runs.submitted_head_sha,'') AS submitted_head, COALESCE(runs.intent,'') AS intent FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`) AND runs.branch=`+sqlString(branch)+` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`, &previous); err != nil {
		return StartCheck{}, err
	}
	// A terminal run has no budget left to reset, so only a still-live one
	// can block a new run. The set matches Idle's.
	if len(previous) > 0 && !terminalRunStatus[previous[0].Status] {
		run := previous[0]
		// Native reattachment keeps the run's own intent, so a start may only
		// reattach to the work it names. Head and intent are compared first,
		// because a start of other work is refused whatever the branch holds.
		if strings.TrimSpace(intent) == "" || strings.TrimSpace(run.Intent) != strings.TrimSpace(intent) {
			return StartCheck{}, fmt.Errorf("%w; run %s is %s on %s with another intent, so an earlier run must be resolved, not restarted: read it with no-mistakes axi status --run %s", ErrUnresolved, run.ID, run.Status, branch, run.ID)
		}
		head, err := r.branchHead(ctx, worktree)
		if err != nil {
			return StartCheck{}, err
		}
		if !run.sameHead(head) {
			if err := r.checkAcceptedHandbackReattachment(ctx, project, worktree, branch, run, head); err != nil {
				return StartCheck{}, fmt.Errorf("%w; run %s is %s on %s and started at %s, not the head this start has; accepted handback reattachment refused: %v; read it with no-mistakes axi status --run %s", ErrUnresolved, run.ID, run.Status, branch, run.submittedOrHead(), err, run.ID)
			}
		}
		// A reattachment starts nothing and carries no launch selection, so the
		// checks below, of what a new run starts from, do not apply to it: the
		// live run keeps what it started with, however far origin's default
		// branch moved during the hold (issue 1586).
		return StartCheck{ReattachRun: run.ID}, nil
	}
	status, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"status", "--porcelain", "--untracked-files=all"}})
	if err != nil || status.ExitCode != 0 || len(strings.TrimSpace(string(status.Stdout))) != 0 {
		return StartCheck{}, errors.New("pipeline: commit task work before starting the gate")
	}
	task, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"show", "HEAD:.no-mistakes.yaml"}})
	if err != nil || task.ExitCode != 0 {
		return StartCheck{}, ErrGateFileRequired
	}
	if err := checkRepoConfig(task.Stdout, policy, true); err != nil {
		return StartCheck{}, err
	}
	defaultBranch := repos[0].DefaultBranch
	remoteHead, err := r.originDefaultHead(ctx, project, defaultBranch)
	if err != nil {
		return StartCheck{}, err
	}
	trustedRef := "refs/remotes/origin/" + defaultBranch
	local, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"rev-parse", "--verify", trustedRef}})
	localFields := strings.Fields(string(local.Stdout))
	if err != nil || local.ExitCode != 0 || len(localFields) != 1 {
		return StartCheck{}, errors.New("pipeline: readable origin default-branch tracking evidence is required")
	}
	if localFields[0] != remoteHead {
		return StartCheck{}, errors.New("pipeline: origin default-branch tracking evidence is stale")
	}
	trusted, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"show", trustedRef + ":.no-mistakes.yaml"}})
	if err != nil || trusted.ExitCode != 0 {
		return StartCheck{}, ErrGateFileRequired
	}
	if err := checkRepoConfig(trusted.Stdout, policy, false); err != nil {
		return StartCheck{}, err
	}
	if err := CheckRepoAgent(task.Stdout, trusted.Stdout, policy); err != nil {
		return StartCheck{}, err
	}
	return StartCheck{TrustedHead: remoteHead}, nil
}

type handbackReattachmentState struct {
	ReceiptID         string `json:"receipt_id"`
	ReceiptRoundID    string `json:"receipt_round_id"`
	RunID             string `json:"run_id"`
	RepoID            string `json:"repo_id"`
	Branch            string `json:"branch"`
	Status            string `json:"status"`
	Head              string `json:"head"`
	SubmittedHead     string `json:"submitted_head"`
	Intent            string `json:"intent"`
	CallerDir         string `json:"caller_dir"`
	ReleasedHead      string `json:"released_head"`
	AcceptedHead      string `json:"accepted_head"`
	AcceptedAt        int64  `json:"accepted_at"`
	Candidate         string `json:"candidate"`
	RunPR             string `json:"run_pr"`
	ReceiptPR         string `json:"receipt_pr"`
	PRState           string `json:"pr_state"`
	PushRef           string `json:"push_ref"`
	ReceiptPushRef    string `json:"receipt_push_ref"`
	Target            string `json:"target"`
	ReceiptTarget     string `json:"receipt_target"`
	UpstreamURL       string `json:"upstream_url"`
	ForkURL           string `json:"fork_url"`
	PublishedHead     string `json:"published_head"`
	PushGeneration    int64  `json:"push_generation"`
	ReceiptGeneration int64  `json:"receipt_generation"`
	CustodyReturned   int    `json:"custody_returned"`
	PushActive        int    `json:"push_active"`
}

func (r Reader) checkAcceptedHandbackReattachment(ctx context.Context, project, worktree, branch string, run previousRun, head string) error {
	query := `SELECT h.id AS receipt_id,h.round_id AS receipt_round_id,runs.id AS run_id,runs.repo_id,runs.branch,runs.status,runs.head_sha AS head,COALESCE(runs.submitted_head_sha,'') AS submitted_head,COALESCE(runs.intent,'') AS intent,h.caller_dir,h.released_head,h.accepted_head,COALESCE(h.accepted_at,0) AS accepted_at,h.candidate_json AS candidate,runs.pr_url AS run_pr,h.pr_url AS receipt_pr,runs.pr_state,runs.push_ref,h.push_ref AS receipt_push_ref,runs.push_target_fingerprint AS target,h.target_fingerprint AS receipt_target,repos.upstream_url,COALESCE(repos.fork_url,'') AS fork_url,runs.last_pushed_sha AS published_head,COALESCE(runs.push_generation,0) AS push_generation,h.push_generation AS receipt_generation,CASE WHEN runs.custody_returned_at IS NULL THEN 0 ELSE 1 END AS custody_returned,COALESCE(runs.push_active,-1) AS push_active FROM runs JOIN repos ON repos.id=runs.repo_id JOIN ci_handbacks h ON h.run_id=runs.id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` AND runs.id=` + sqlString(run.ID) + ` AND runs.id=(SELECT latest.id FROM runs latest WHERE latest.repo_id=runs.repo_id AND latest.branch=runs.branch ORDER BY latest.created_at DESC,latest.id DESC LIMIT 1) AND h.id=(SELECT latest.id FROM ci_handbacks latest WHERE latest.run_id=runs.id ORDER BY latest.id DESC LIMIT 1)`
	read := func() (handbackReattachmentState, error) {
		var rows []handbackReattachmentState
		if err := r.query(ctx, query, &rows); err != nil {
			return handbackReattachmentState{}, err
		}
		if len(rows) != 1 {
			return handbackReattachmentState{}, errors.New("expected exactly one latest run and handback receipt")
		}
		return rows[0], nil
	}
	state, err := read()
	if err != nil {
		return err
	}
	if !gateIdentity.MatchString(state.ReceiptID) || !gateIdentity.MatchString(state.ReceiptRoundID) || !gateIdentity.MatchString(state.RunID) || !gateIdentity.MatchString(state.RepoID) || state.RunID != run.ID || state.Branch != branch || state.Status != "running" || state.Status != run.Status || state.Head != run.Head || state.SubmittedHead != run.SubmittedHead || state.Intent != run.Intent || state.PushActive != 0 || state.CustodyReturned != 0 {
		return errors.New("run identity or custody does not match the accepted continuation")
	}
	if !gateCommit.MatchString(state.ReleasedHead) || !gateCommit.MatchString(state.AcceptedHead) || state.AcceptedAt <= 0 || state.AcceptedHead != head || state.Head != head {
		return errors.New("caller and current run head must equal the accepted handback head")
	}
	var candidate struct {
		Head, BaseHead, ManagedBranch, UpstreamURL, ForkURL, ReviewRoundID string
	}
	if err := json.Unmarshal([]byte(state.Candidate), &candidate); err != nil {
		return fmt.Errorf("invalid accepted handback candidate: %w", err)
	}
	if candidate.Head != head || !gateCommit.MatchString(candidate.BaseHead) || candidate.ManagedBranch != "HEAD" && candidate.ManagedBranch != branch || !gateIdentity.MatchString(candidate.ReviewRoundID) || candidate.UpstreamURL == "" || candidate.UpstreamURL != state.UpstreamURL || candidate.ForkURL != state.ForkURL {
		return errors.New("accepted handback candidate or repository target changed")
	}
	if state.PRState != "open" || state.RunPR == "" || state.RunPR != state.ReceiptPR || state.Target == "" || state.Target != state.ReceiptTarget || state.PushRef != "refs/heads/"+branch || state.PushRef != state.ReceiptPushRef || state.ReceiptGeneration <= 0 {
		return errors.New("accepted handback publication identity changed")
	}
	isOriginalPublication := state.PublishedHead == state.ReleasedHead && state.PushGeneration == state.ReceiptGeneration
	isAcceptedPublication := state.PublishedHead == state.AcceptedHead && state.PushGeneration == state.ReceiptGeneration+1
	if !isOriginalPublication && !isAcceptedPublication {
		return errors.New("accepted handback publication generation changed")
	}
	caller, err := fsx.Canonical(worktree)
	if err != nil {
		return err
	}
	if !fsx.SamePath(caller, state.CallerDir) {
		return errors.New("accepted handback belongs to another caller worktree")
	}
	checkCaller := func() error {
		actualBranch, err := r.runGit(ctx, caller, false, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil || actualBranch.ExitCode != 0 || strings.TrimSpace(string(actualBranch.Stdout)) != branch {
			return errors.New("accepted handback caller branch changed")
		}
		actualHead, err := r.branchHead(ctx, caller)
		if err != nil || actualHead != head {
			return errors.New("accepted handback caller head changed")
		}
		clean, err := r.runGit(ctx, caller, false, "status", "--porcelain", "--untracked-files=all")
		if err != nil || clean.ExitCode != 0 || len(strings.TrimSpace(string(clean.Stdout))) != 0 {
			return errors.New("accepted handback requires a clean caller worktree")
		}
		return nil
	}
	if err := checkCaller(); err != nil {
		return err
	}
	fresh, err := read()
	if err != nil {
		return err
	}
	if fresh != state {
		return errors.New("accepted handback binding changed during reattachment")
	}
	return checkCaller()
}

// previousRun is the branch's latest run as CheckStart reads it.
type previousRun struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Head          string `json:"head"`
	SubmittedHead string `json:"submitted_head"`
	Intent        string `json:"intent"`
}

// sameHead reports whether the run started on the commit the branch still
// holds. A run from a no-mistakes that records no submitted head is read by its
// current head, which is that commit until a gate fix moves it, so a moved one
// is unproven rather than another run's work.
func (run previousRun) sameHead(head string) bool {
	return run.submittedOrHead() == head
}

func (run previousRun) submittedOrHead() string {
	if run.SubmittedHead != "" {
		return run.SubmittedHead
	}
	return run.Head
}

// branchHead is the commit the task branch holds, which a live run started at
// until the gate moves its own worktree.
func (r Reader) branchHead(ctx context.Context, worktree string) (string, error) {
	result, err := r.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"rev-parse", "HEAD"}})
	head := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || !gateCommit.MatchString(head) {
		return "", errors.New("pipeline: readable task branch head is required")
	}
	return head, nil
}

// CheckRepoAgent says whether a policy refuses the agent a repository's gate
// file names, given the file as the task's branch and as the default branch
// have it. It is what a gate's start asks and what `cfo project check`
// reads, so a pin is judged by the code that would refuse the run. From
// version 6 the run's own launch selection replaces whatever agent the
// repository names, so no repository agent is refused.
func CheckRepoAgent(taskData, trustedData []byte, p Policy) error {
	if p.Version >= 6 {
		return nil
	}
	return checkEffectivePrimaryAgent(taskData, trustedData, p)
}

func (r Reader) originDefaultHead(ctx context.Context, project, defaultBranch string) (string, error) {
	remote, err := r.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"ls-remote", "--symref", "origin", "HEAD"}})
	fields := strings.Fields(string(remote.Stdout))
	if err != nil || remote.ExitCode != 0 || len(fields) != 5 || fields[0] != "ref:" || fields[1] != "refs/heads/"+defaultBranch || fields[2] != "HEAD" || fields[4] != "HEAD" {
		return "", errors.New("pipeline: current origin default-branch evidence is required")
	}
	return fields[3], nil
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
	// A branch can edit its own .no-mistakes.yaml, so each count is held to
	// the policy's as a ceiling: lowering one only sends more to a person,
	// while raising one, or setting one the policy does not govern, could let
	// a gate fix its way past a decision. babysit is the legacy name for ci.
	ceilings := map[string]int{"review": p.AutoFix.Review, "test": p.AutoFix.Test, "lint": p.AutoFix.Lint, "rebase": p.AutoFix.Rebase, "ci": p.AutoFix.CI, "babysit": p.AutoFix.CI}
	for key, got := range config.AutoFix {
		ceiling, governed := ceilings[key]
		if !governed {
			return fmt.Errorf("pipeline: repository auto_fix.%s is not a count the task policy governs", key)
		}
		if got < 0 || got > ceiling {
			return fmt.Errorf("pipeline: repository auto_fix.%s is %d, outside 0 to the task policy's %d: a repository may lower an automatic fix, never raise one", key, got, ceiling)
		}
	}
	return nil
}

func checkEffectivePrimaryAgent(taskData, trustedData []byte, p Policy) error {
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
		return err
	}
	trusted, err := decode(trustedData)
	if err != nil {
		return err
	}
	agent := &trusted.Agent
	source := "trusted default-branch"
	if trusted.AllowRepoCommands {
		agent = &task.Agent
		source = "submitted branch"
	}
	if agent.Kind == 0 {
		return nil
	}
	if agent.Kind == yaml.SequenceNode && len(agent.Content) == 1 {
		agent = agent.Content[0]
	}
	want := p.Reviewer.Harness
	if p.Version > 1 {
		want = p.Primary.Harness
	}
	if agent.Kind != yaml.ScalarNode || agent.Value != want {
		return fmt.Errorf("pipeline: effective %s agent overrides the owned %s primary in no-mistakes v1.75.1; remove the effective repository agent field or set it to %s", source, want, want)
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

// RunBranch is the branch of run in this registered project, which must be
// that branch's latest run: a native command answers only the latest run of
// the branch checked out where it runs.
func (r Reader) RunBranch(ctx context.Context, project, run string) (string, error) {
	var rows []struct {
		Branch string `json:"branch"`
	}
	sql := `SELECT runs.branch FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.id=` + sqlString(run) + ` AND runs.id=(SELECT latest.id FROM runs latest WHERE latest.repo_id=runs.repo_id AND latest.branch=runs.branch ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)`
	if err := r.query(ctx, sql, &rows); err != nil {
		return "", err
	}
	if len(rows) != 1 || rows[0].Branch == "" {
		return "", fmt.Errorf("pipeline: run %s is not the latest run of a branch in this task's project", run)
	}
	return rows[0].Branch, nil
}

// Response is one answer to a parked gate. Accept names the open ask-user and
// auto-fix findings the CFO, or on a spent review the task's own goblin, takes
// as they stand, with approve only. A recognized blank configured Test failure
// also needs Accept and the CFO's Reason, which the native exception keeps.
type Response struct{ Action, Findings, Instructions, Accept, Reason string }
type finding struct {
	ID          string `json:"id"`
	Action      string `json:"action"`
	Severity    string `json:"severity"`
	Category    string `json:"category"`
	Description string `json:"description"`
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
		Summary  string     `json:"summary"`
	}
	if err := json.Unmarshal([]byte(gate.Findings), &report); err != nil || report.Findings == nil {
		return nil, errors.New("pipeline: missing or invalid findings evidence")
	}
	emptyActionFixable := gate.Step != "review" && gate.AutoFixLimit != nil && *gate.AutoFixLimit > 0
	actions := map[string]string{}
	var open []string
	unresolved, blank := false, false
	isConfiguredTestException := false
	baseRefusal, _, _ := strings.Cut(report.Summary, "\n")
	isRequiredEnvironmentRefused := strings.HasPrefix(baseRefusal, "Base attribution unavailable (test command was given no turn on the base checkout:") && strings.Contains(baseRefusal, "required test environment")
	for _, f := range *report.Findings {
		if f.ID == "" {
			return nil, errors.New("pipeline: invalid or duplicate finding ID")
		}
		_, wrapperRefusal, _ := strings.Cut(f.Description, "The wrapper reported: ")
		isRequiredEnvironmentRefused = isRequiredEnvironmentRefused || f.ID == "test-command-no-turn" && strings.Contains(wrapperRefusal, "required test environment")
		switch f.Action {
		case "no-op":
			// Native selection includes all matching IDs; informational rows
			// add no actionable meaning to a selected repair.
			continue
		case "auto-fix", "ask-user":
			unresolved = true
			open = append(open, f.ID)
		case "":
			if gate.Step == "test" && response.Action == "approve" && response.Reason != "" && f.Category == "test-command" && f.Severity == "error" {
				exitCode, err := strconv.Atoi(strings.TrimPrefix(f.Description, "configured test command failed with exit code "))
				if err == nil && exitCode != 0 && f.Description == fmt.Sprintf("configured test command failed with exit code %d", exitCode) {
					unresolved, isConfiguredTestException = true, true
					open = append(open, f.ID)
					break
				}
			}
			if !emptyActionFixable {
				return nil, ErrUnresolved
			}
			unresolved, blank = true, true
		default:
			return nil, ErrUnresolved
		}
		if _, exists := actions[f.ID]; exists {
			return nil, errors.New("pipeline: invalid or duplicate finding ID")
		}
		actions[f.ID] = f.Action
	}
	args := []string{"axi", "respond", "--step", gate.Step, "--action", response.Action}
	switch response.Action {
	case "approve":
		if gate.Step == "test" && isRequiredEnvironmentRefused {
			return nil, fmt.Errorf("pipeline: a recorded required test environment refusal cannot be approved; repair the binding, then retry with --action fix --findings %s and require a successful Test", strings.Join(open, ","))
		}
		if response.Reason != "" {
			// The native engine keeps a reason only for an approved Test
			// exception, and a reason it drops would leave the run reading as
			// one nobody explained, so every other approval is refused here.
			if gate.Step != "test" {
				return nil, fmt.Errorf("pipeline: --reason applies only to an approve of the test step, and this gate is %s", gate.Step)
			}
			if problem := approvalReasonProblem(response.Reason); problem != "" {
				return nil, fmt.Errorf("pipeline: --reason %s", problem)
			}
		}
		if response.Accept != "" {
			// The CFO takes the open findings as they stand only by naming
			// every one of them; an unrecognized blank action needs its fix.
			named := strings.Split(response.Accept, ",")
			slices.Sort(named)
			slices.Sort(open)
			switch {
			case blank:
				return nil, ErrUnresolved
			case len(open) == 0:
				return nil, errors.New("pipeline: no open ask-user or auto-fix finding to accept; approve without --accept")
			case !slices.Equal(named, open):
				return nil, fmt.Errorf("pipeline: --accept must name exactly the open ask-user and auto-fix findings: %s", strings.Join(open, ","))
			}
		} else if unresolved {
			return nil, ErrUnresolved
		}
		if response.Findings != "" || response.Instructions != "" {
			return nil, errors.New("pipeline: approve does not accept fix arguments")
		}
		if response.Reason != "" {
			args = append(args, "--reason", response.Reason)
		}
		if isConfiguredTestException {
			args = append(args, "--run", gate.RunID)
		}
	case "fix":
		if response.Accept != "" {
			return nil, errors.New("pipeline: --accept goes with approve, never with fix")
		}
		if response.Reason != "" {
			return nil, errors.New("pipeline: --reason goes with approve of the test step, never with fix")
		}
		if ReviewCyclesSpent(s, gate) {
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

// ReviewCyclesSpent reports whether gate is a review whose run has spent the
// repair cycles its selection permits: no fix of it is left, and its owning
// goblin may accept its open findings itself.
func ReviewCyclesSpent(s Selection, gate Gate) bool {
	return gate.Step == "review" && gate.Round-1 >= s.ReviewCycles
}

// approvalReasonProblem says what keeps a response's reason out of the native
// exception record, and is empty when nothing does. A blank reason is stored
// as none and the run reads as approved by nobody, and a control character
// would break the status line the reason is audited on.
func approvalReasonProblem(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "must say why the exception is approved"
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "must be one line of plain text"
		}
	}
	return ""
}

// TrustedHead is the head of origin's default branch for a registered
// project, which is the trusted commit a launch selection names.
func (r Reader) TrustedHead(ctx context.Context, project string) (string, error) {
	var repos []struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := r.query(ctx, `SELECT default_branch FROM repos WHERE lower(replace(working_path,char(92),'/'))=lower(`+sqlString(filepath.ToSlash(project))+`)`, &repos); err != nil {
		return "", err
	}
	if len(repos) != 1 || repos[0].DefaultBranch == "" {
		return "", errors.New("pipeline: registered repository required")
	}
	return r.originDefaultHead(ctx, project, repos[0].DefaultBranch)
}
