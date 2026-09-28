package pipeline

import (
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
	ID       string `json:"id"`
	RepoID   string `json:"repo_id"`
	Branch   string `json:"branch"`
	Status   string `json:"status"`
	Head     string `json:"head"`
	Intent   string `json:"intent"`
	Worktree string `json:"worktree"`
}

// IsTerminal reports whether the run has finished, so nothing is left to
// interrupt.
func (run InterruptedRun) IsTerminal() bool {
	return terminalRunStatus[run.Status]
}

var gateIdentity = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
var gateCommit = regexp.MustCompile(`^[a-f0-9]{40}$`)

func (reader Reader) Interruption(ctx context.Context, project, branch string) (InterruptedRun, error) {
	var runs []InterruptedRun
	query := `SELECT runs.id, runs.repo_id, runs.branch, runs.status, runs.head_sha AS head, COALESCE(runs.intent,'') AS intent, COALESCE(runs.worktree_dir,'') AS worktree FROM runs JOIN repos ON repos.id=runs.repo_id WHERE lower(replace(repos.working_path,char(92),'/'))=lower(` + sqlString(filepath.ToSlash(project)) + `) AND runs.branch=` + sqlString(branch) + ` ORDER BY runs.created_at DESC,runs.id DESC LIMIT 1`
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
		worktreeHead, err := reader.preserveInterruptedWorktree(ctx, worktree, run)
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
		return current, nil
	}
	return run, errors.New("gate kept advancing while stopping; its commits are pinned, but the run was not aborted")
}

func (reader Reader) preserveInterruptedWorktree(ctx context.Context, worktree string, run InterruptedRun) (string, error) {
	if run.Worktree == "" {
		return "", nil
	}
	expected := filepath.Join(reader.Root, "worktrees", run.RepoID, run.ID)
	if !strings.EqualFold(filepath.Clean(run.Worktree), expected) {
		return "", errors.New("gate worktree differs from its verified run directory; no run was aborted")
	}
	if _, err := os.Stat(run.Worktree); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	for _, check := range []struct {
		args []string
		want string
	}{
		{[]string{"rev-parse", "--show-toplevel"}, run.Worktree},
		{[]string{"rev-parse", "--path-format=absolute", "--git-common-dir"}, filepath.Join(reader.Root, "repos", run.RepoID+".git")},
	} {
		result, err := reader.runGit(ctx, run.Worktree, false, check.args...)
		if err != nil || result.ExitCode != 0 || !strings.EqualFold(filepath.Clean(strings.TrimSpace(string(result.Stdout))), filepath.Clean(check.want)) {
			return "", errors.New("gate worktree repository could not be verified; no run was aborted")
		}
	}
	result, err := reader.runGit(ctx, run.Worktree, false, "rev-parse", "HEAD")
	head := strings.TrimSpace(string(result.Stdout))
	if err != nil || result.ExitCode != 0 || !gateCommit.MatchString(head) {
		return "", errors.New("gate worktree head could not be read; no run was aborted")
	}
	if head != run.Head {
		actual := run
		actual.Head = head
		if err := reader.importInterruptedHead(ctx, worktree, actual); err != nil {
			return "", err
		}
	}
	result, err = reader.runGit(ctx, run.Worktree, false, "status", "--porcelain", "--untracked-files=all")
	if err != nil || result.ExitCode != 0 {
		return head, errors.New("gate worktree changes could not be read; no run was aborted")
	}
	if len(result.Stdout) > 0 {
		return head, fmt.Errorf("gate has unfinished changes retained at %s; committed fixes are pinned, and the run was not aborted", run.Worktree)
	}
	return head, nil
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

func (reader Reader) RestartInterrupted(ctx context.Context, project, worktree string, prior InterruptedRun) error {
	if strings.TrimSpace(prior.Intent) == "" {
		return errors.New("paused validation has no saved intent")
	}
	current, err := reader.Interruption(ctx, project, prior.Branch)
	if err != nil {
		return err
	}
	if current.ID != prior.ID {
		if current.Intent == prior.Intent && (!terminalRunStatus[current.Status] || current.Status == "completed") {
			return nil
		}
		return errors.New("another validation run owns the branch; inspect it before resuming")
	}
	if !terminalRunStatus[current.Status] {
		return errors.New("paused validation has not stopped; no replacement run was started")
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result, err := reader.Commands.Run(bounded, execx.Request{Dir: worktree, Name: "no-mistakes", Args: []string{"axi", "sync", "--recover"}})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("recover paused validation: %s", strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))
	}
	result, runErr := reader.Commands.Run(bounded, execx.Request{Dir: worktree, Name: "no-mistakes", Args: []string{"axi", "run", "--intent", prior.Intent, "--wait", "45s"}})
	current, err = reader.Interruption(bounded, project, prior.Branch)
	// A bounded AXI wait exits nonzero while the accepted run keeps working.
	if err == nil && current.ID != prior.ID && current.Intent == prior.Intent && (!terminalRunStatus[current.Status] || current.Status == "completed") {
		return nil
	}
	return fmt.Errorf("validation did not restart: %w", errors.Join(runErr, err, errors.New(strings.TrimSpace(string(result.Stdout)+string(result.Stderr)))))
}
