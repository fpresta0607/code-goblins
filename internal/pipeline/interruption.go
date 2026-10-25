package pipeline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

type InterruptedRun struct {
	ID         string `json:"id"`
	RepoID     string `json:"repo_id"`
	Branch     string `json:"branch"`
	Status     string `json:"status"`
	Head       string `json:"head"`
	PushedHead string `json:"pushed_head"`
	Intent     string `json:"intent"`
	Worktree   string `json:"worktree"`
	// WorktreeEdits is the commit the run worktree's unfinished changes were
	// preserved at, and empty when it held none. It is read beside the run's
	// database row, never from it.
	WorktreeEdits string `json:"worktree_edits"`
}

// IsTerminal reports whether the run has finished, so nothing is left to
// interrupt.
func (run InterruptedRun) IsTerminal() bool {
	return terminalRunStatus[run.Status]
}

var gateIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
var gateCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

// WorktreeRef names where a run's unfinished worktree changes are kept. It
// sits beside the refs recovery anchors its heads under, and it is bound to the
// run, so a goblin recovering custody finds them without the worktree.
func WorktreeRef(runID string) string {
	return "refs/no-mistakes/recovery/" + runID + "/worktree"
}

func (reader Reader) Interruption(ctx context.Context, project, branch string) (InterruptedRun, error) {
	var runs []InterruptedRun
	query := `SELECT runs.id, runs.repo_id, runs.branch, runs.status, runs.head_sha AS head, COALESCE(runs.last_pushed_sha,'') AS pushed_head, COALESCE(runs.intent,'') AS intent, COALESCE(runs.worktree_dir,'') AS worktree FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`
	if err := reader.query(ctx, query, &runs); err != nil {
		return InterruptedRun{}, err
	}
	if len(runs) == 0 {
		return InterruptedRun{}, ErrNoProgress
	}
	if len(runs) != 1 || !gateIdentity.MatchString(runs[0].ID) || !gateIdentity.MatchString(runs[0].RepoID) || !gateCommit.MatchString(runs[0].Head) || runs[0].Branch != branch {
		return InterruptedRun{}, errors.New("gate identity could not be verified")
	}
	return runs[0], nil
}

// Interrupt is called after the task's writers have stopped. Every preserved
// gate head gets an immutable ref in both repositories before the scoped abort.
func (reader Reader) Interrupt(ctx context.Context, project, worktree, branch, runID string) (InterruptedRun, error) {
	run, err := reader.Interruption(ctx, project, branch)
	if err != nil {
		return run, err
	}
	if run.ID != runID {
		return run, errors.New("the gate run changed; no run was aborted")
	}
	for attempts := 0; attempts < 3; attempts++ {
		if err := reader.importInterruptedHead(ctx, worktree, run); err != nil {
			return run, err
		}
		worktreeHead, edits, err := reader.preserveInterruptedWorktree(ctx, worktree, run)
		if err != nil {
			return run, err
		}
		current, err := reader.Interruption(ctx, project, branch)
		if err != nil {
			return run, err
		}
		if current.ID != run.ID {
			return run, errors.New("the gate run changed; no run was aborted")
		}
		if current.Head != run.Head {
			run = current
			continue
		}
		run = current
		if !terminalRunStatus[run.Status] {
			result, err := reader.Commands.Run(ctx, execx.Request{Dir: worktree, Name: "no-mistakes", Args: []string{"axi", "abort", "--run", run.ID}})
			if err != nil || result.ExitCode != 0 {
				return run, fmt.Errorf("abort gate %s failed: %w", run.ID, errors.Join(err, errors.New(strings.TrimSpace(string(result.Stderr)))))
			}
		}
		current, err = reader.Interruption(ctx, project, branch)
		if err != nil {
			return run, err
		}
		if current.ID != run.ID || !terminalRunStatus[current.Status] {
			return run, errors.New("the requested validation run could not be confirmed stopped")
		}
		if current.Head != run.Head {
			if err := reader.importInterruptedHead(ctx, worktree, current); err != nil {
				return current, err
			}
		} else if worktreeHead != "" {
			current.Head = worktreeHead
		}
		current.WorktreeEdits = edits
		return current, nil
	}
	return run, errors.New("gate kept advancing while stopping; its commits are pinned, but the run was not aborted")
}

// preserveInterruptedWorktree pins every commit and every unfinished change the
// gate run's worktree holds before the run is aborted, and returns its head and
// the commit its changes were preserved at.
func (reader Reader) preserveInterruptedWorktree(ctx context.Context, worktree string, run InterruptedRun) (string, string, error) {
	gateWorktree, err := reader.gateRunWorktree(ctx, run.ID, run.RepoID)
	if err != nil {
		return "", "", fmt.Errorf("%w; no run was aborted", err)
	}
	if gateWorktree == "" {
		return "", "", nil
	}
	result, err := reader.runGit(ctx, gateWorktree, false, "rev-parse", "HEAD")
	head := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || !gateCommit.MatchString(head) {
		return "", "", errors.New("gate worktree head could not be read; no run was aborted")
	}
	if head != run.Head {
		actual := run
		actual.Head = head
		if err := reader.importInterruptedHead(ctx, worktree, actual); err != nil {
			return "", "", err
		}
	}
	edits, err := reader.preserveWorktreeEdits(ctx, run.ID, run.RepoID, gateWorktree, head)
	if err != nil {
		return "", "", fmt.Errorf("%w; no run was aborted", err)
	}
	return head, edits, nil
}

// gateRunWorktree is the verified worktree of a gate run, and "" when the run
// has none left.
func (reader Reader) gateRunWorktree(ctx context.Context, runID, repoID string) (string, error) {
	if !gateIdentity.MatchString(runID) || !gateIdentity.MatchString(repoID) {
		return "", errors.New("gate run identity could not be verified")
	}
	worktree := filepath.Join(reader.Root, "worktrees", repoID, runID)
	if _, err := os.Stat(worktree); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--show-toplevel"}, worktree},
		{[]string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, filepath.Join(reader.Root, "repos", repoID+".git")},
	} {
		result, err := reader.runGit(ctx, worktree, false, check.args...)
		if err != nil || result.ExitCode != 0 || !strings.EqualFold(filepath.Clean(strings.TrimSpace(string(result.Stdout))), filepath.Clean(check.want)) {
			return "", errors.New("gate worktree repository could not be verified")
		}
	}
	return worktree, nil
}

// preserveRunWorktreeEdits keeps whatever unfinished changes a failed or
// cancelled run's worktree still holds. Native recovery removes that worktree
// with force, so the fixer's tracked and untracked repairs are committed and
// pinned before anything named recovery runs.
func (reader Reader) preserveRunWorktreeEdits(ctx context.Context, run recoveryRecord) (string, error) {
	worktree, err := reader.gateRunWorktree(ctx, run.RunID, run.RepoID)
	if err != nil {
		return "", fmt.Errorf("pipeline: gate worktree of run %s could not be verified: %w", run.RunID, err)
	}
	if worktree == "" {
		return "", nil
	}
	result, err := reader.runGit(ctx, worktree, false, "rev-parse", "HEAD")
	head := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || !gateCommit.MatchString(head) {
		return "", fmt.Errorf("pipeline: gate worktree of run %s could not be read, so its unfinished changes are not safe: %w", run.RunID, err)
	}
	return reader.preserveWorktreeEdits(ctx, run.RunID, run.RepoID, worktree, head)
}

// preserveWorktreeEdits commits every tracked and untracked change the gate
// run's worktree holds - the fixer's unfinished repairs - and pins that commit
// under the run's own ref, so a teardown that removes the worktree with force
// loses nothing. It is idempotent: the first capture stands, since it is what a
// goblin may already be restoring. It returns the commit, and "" when the
// worktree holds no change.
func (reader Reader) preserveWorktreeEdits(ctx context.Context, runID, repoID, worktree, head string) (string, error) {
	if !gateIdentity.MatchString(runID) || !gateIdentity.MatchString(repoID) {
		return "", errors.New("gate run identity could not be verified")
	}
	bare := filepath.Join(reader.Root, "repos", repoID+".git")
	ref := WorktreeRef(runID)
	kept, found, err := reader.recoveryHead(ctx, bare, ref, true)
	if err != nil {
		return "", fmt.Errorf("gate worktree changes could not be checked: %w", err)
	}
	if found {
		return kept, nil
	}
	status, err := reader.runGit(ctx, worktree, false, "status", "--porcelain", "--untracked-files=all")
	if err != nil || status.ExitCode != 0 {
		return "", errors.New("gate worktree changes could not be read")
	}
	if len(bytes.TrimSpace(status.Stdout)) == 0 {
		return "", nil
	}
	index, err := os.CreateTemp("", "cfo-gate-worktree-")
	if err != nil {
		return "", err
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		return "", err
	}
	// git makes the index it is given, and refuses one that already exists.
	if err := os.Remove(indexPath); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(indexPath) }()
	// The worktree's own index is never written: the changes are staged into an
	// index of this read's own, so the goblin's next command sees what it left.
	env := append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
	var tree string
	for _, args := range [][]string{{"read-tree", head}, {"add", "--all", "--", "."}, {"write-tree"}} {
		result, err := reader.runGitEnv(ctx, worktree, env, args...)
		if err != nil || result.ExitCode != 0 {
			return "", fmt.Errorf("gate worktree changes could not be preserved: %s failed", args[0])
		}
		if args[0] == "write-tree" {
			tree = strings.TrimSpace(string(result.Stdout))
		}
	}
	headTree, err := reader.runGit(ctx, worktree, false, "rev-parse", head+"^{tree}")
	if err != nil || headTree.ExitCode != 0 {
		return "", errors.New("gate worktree changes could not be preserved: the run head has no tree")
	}
	if tree == strings.TrimSpace(string(headTree.Stdout)) {
		return "", nil
	}
	commit, err := reader.runGitEnv(ctx, worktree, env, "-c", "user.name=Code Goblins", "-c", "user.email=code-goblins@localhost", "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", head, "-m", "cfo: preserve the gate worktree edits of run "+runID)
	kept = strings.TrimSpace(string(commit.Stdout))
	if err != nil || commit.ExitCode != 0 || !gateCommit.MatchString(kept) {
		return "", errors.New("gate worktree changes could not be preserved: the commit could not be written")
	}
	if err := reader.preserveRecoveryHead(ctx, bare, ref, kept); err != nil {
		return "", fmt.Errorf("gate worktree changes could not be pinned: %w", err)
	}
	return kept, nil
}

func (reader Reader) importInterruptedHead(ctx context.Context, worktree string, run InterruptedRun) error {
	bare := filepath.Join(reader.Root, "repos", run.RepoID+".git")
	if err := reader.requireCommit(ctx, bare, run.Head, true); err != nil {
		return fmt.Errorf("gate head is not available: %w", err)
	}
	ref := "refs/heads/archive/pause-" + run.ID + "-" + run.Head
	if err := reader.preserveRecoveryHead(ctx, bare, ref, run.Head); err != nil {
		return err
	}
	commands := [][]string{
		{"fetch", "--no-tags", bare, ref + ":" + ref},
		{"cat-file", "-e", run.Head + "^{commit}"},
		{"symbolic-ref", "--short", "HEAD"},
	}
	for _, args := range commands {
		result, err := reader.runGit(ctx, worktree, false, args...)
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("preserve gate head: %s failed", args[0])
		}
		if args[0] == "symbolic-ref" && strings.TrimSpace(string(result.Stdout)) != run.Branch {
			return errors.New("task branch changed; gate commits remain pinned")
		}
	}
	result, err := reader.runGit(ctx, worktree, false, "merge", "--no-edit", ref)
	if err != nil || result.ExitCode != 0 {
		return errors.New("gate commits are pinned locally but could not merge into the task branch; worktree and run are kept for conflict resolution")
	}
	result, err = reader.runGit(ctx, worktree, false, "merge-base", "--is-ancestor", run.Head, "HEAD")
	if err != nil || result.ExitCode != 0 {
		return errors.New("task branch does not contain the preserved gate commits; no run was aborted")
	}
	return nil
}

// RestartInterrupted starts a run in place of the one a pause stopped. launch,
// when given, returns the arguments the replacement starts under beside its
// intent, which is how a gate that runs on its task's own harness keeps doing
// so past cfo pipeline run. It is asked only once a replacement will start,
// and a refusal from it starts nothing.
func (reader Reader) RestartInterrupted(ctx context.Context, project, worktree string, prior InterruptedRun, launch func(context.Context) ([]string, error)) error {
	if strings.TrimSpace(prior.Intent) == "" {
		return errors.New("paused validation has no saved intent")
	}
	current, err := reader.Interruption(ctx, project, prior.Branch)
	if err != nil {
		return err
	}
	if current.ID != prior.ID {
		if current.Intent == prior.Intent {
			return nil
		}
		return errors.New("another validation run owns the branch; inspect it before resuming")
	}
	// A run the pause could not stop is still live: its goblin picks it
	// back up, and no replacement is started beside it. On 2026-10-09 a
	// pause that could not abort Murray's run left every resume refused
	// here, so nothing but a switch by hand brought him back.
	if !terminalRunStatus[current.Status] {
		return nil
	}
	var selected []string
	if launch != nil {
		selected, err = launch(ctx)
		if err != nil {
			return fmt.Errorf("validation did not restart: %w", err)
		}
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	handover, err := StartOf(bounded, reader.Commands, worktree, nil, prior.Intent)
	if err != nil {
		return fmt.Errorf("validation did not restart: %w", err)
	}
	defer handover.Close()
	start := append(append(handover.Args, "--wait", "45s"), selected...)
	// Custody comes back only from a run holding commits it never pushed. A
	// run that published its head left the branch to its goblin, and
	// no-mistakes refuses to recover it, the more so once the goblin pushed
	// its own follow-up commits over that head, as on 2026-10-17.
	if current.PushedHead != current.Head {
		result, err := reader.Commands.Run(bounded, execx.Request{Dir: worktree, Name: "no-mistakes", Args: []string{"axi", "sync", "--recover"}})
		if err != nil || result.ExitCode != 0 {
			return fmt.Errorf("recover paused validation: %s", strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))
		}
	}
	result, runErr := reader.Commands.Run(bounded, execx.Request{Dir: worktree, Name: "no-mistakes", Args: start})
	current, err = reader.Interruption(bounded, project, prior.Branch)
	// A bounded AXI wait exits nonzero while the accepted run keeps working.
	if err == nil && current.ID != prior.ID && current.Intent == prior.Intent {
		return nil
	}
	return fmt.Errorf("validation did not restart: %w", errors.Join(runErr, err, errors.New(strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))))
}
