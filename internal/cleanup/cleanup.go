// Package cleanup returns one clean, proven-inactive task worktree through
// treehouse and closes its task tab. It never invokes git worktree remove,
// deletes a directory, stops an agent, or discards changes: the only
// lifecycle calls it makes are the Herdr tab close of the exact recorded tab
// (after the endpoint is proven agent-free) and treehouse.Service.Return,
// and only after every guard has proven the exact recorded task safe to
// release.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/treehouse"
)

// Service owns the guarded cleanup of one local task.
type Service struct {
	StateDir  string
	Commands  execx.Runner
	Herdr     *herdr.Client
	Treehouse treehouse.Service
}

// Result reports the exact returned task identity.
type Result struct {
	Meta   state.TaskMeta
	Output string
}

// Cleanup validates one task's metadata and isolation, proves its worktree
// clean and its Herdr endpoint inactive, then delegates the worktree release
// to treehouse.Service.Return. Task metadata is preserved whenever any check
// or the return itself fails, so the operator can diagnose and retry the
// exact task.
func (s Service) Cleanup(ctx context.Context, id string) (result Result, err error) {
	if err := state.ValidTaskID(id); err != nil {
		return Result{}, err
	}
	if s.Commands == nil {
		return Result{}, errors.New("cleanup: command runner is required")
	}
	if s.Herdr == nil {
		return Result{}, errors.New("cleanup: Herdr client is required")
	}
	meta, err := state.ReadTaskMeta(s.StateDir, id)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: read task metadata: %w", err)
	}
	if err := validateMeta(meta); err != nil {
		return Result{}, err
	}

	if _, err := lock.AcquireExclusiveNamed(s.StateDir, cleanupLockName(id)); err != nil {
		return Result{}, fmt.Errorf("cleanup: acquire task lock: %w", err)
	}
	defer func() {
		if releaseErr := lock.ReleaseExclusiveNamed(s.StateDir, cleanupLockName(id)); releaseErr != nil {
			releaseErr = fmt.Errorf("cleanup: release task lock: %w", releaseErr)
			if err == nil {
				err = releaseErr
			} else {
				err = errors.Join(err, releaseErr)
			}
		}
	}()

	project, err := fsx.Canonical(meta.Project)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: canonicalize project %q: %w", meta.Project, err)
	}
	worktree, err := fsx.Canonical(meta.Worktree)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: canonicalize worktree %q: %w", meta.Worktree, err)
	}
	if fsx.SamePath(worktree, project) {
		return Result{}, fmt.Errorf("cleanup: worktree %q is the primary checkout", worktree)
	}

	git, err := s.treehouseGit()
	if err != nil {
		return Result{}, err
	}
	if err := treehouse.Validate(ctx, git, project, worktree); err != nil {
		return Result{}, fmt.Errorf("cleanup: validate worktree: %w", err)
	}
	if err := s.requireClean(ctx, worktree); err != nil {
		return Result{}, err
	}
	if err := s.requireInactive(ctx, meta); err != nil {
		return Result{}, err
	}

	// The endpoint is proven agent-free: close the recorded tab so a completed
	// task leaves no terminal behind, then return the worktree.
	if err := s.Herdr.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
		return Result{}, fmt.Errorf("cleanup: close task tab: %w", err)
	}

	if err := s.Treehouse.Return(ctx, project, worktree); err != nil {
		return Result{}, fmt.Errorf("cleanup: return worktree through treehouse: %w", err)
	}

	if err := state.AppendStatus(s.StateDir, id, "done: returned worktree "+worktree+" via cfo cleanup"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record returned worktree: %w", err)
	}
	if err := os.Remove(filepath.Join(s.StateDir, id+".meta")); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	archived, archiveErr := s.archive(id)

	result.Meta = meta
	result.Output = fmt.Sprintf("cleaned %s worktree=%s", id, worktree)
	if archived != "" {
		result.Output += " archive=" + archived
	}
	if archiveErr != nil {
		// The task is genuinely cleaned; only the id is still taken. Say so
		// plainly rather than failing a completed cleanup.
		result.Output += fmt.Sprintf("\nwarning: retained state for %s could not be archived, so respawning that id will be refused: %v", id, archiveErr)
	}
	return result, nil
}

// archive moves a finished task's scratch directory out of the live state
// tree. It is what frees the task id: a respawn refuses any id that collides
// case-insensitively with retained state, which is why reusing a cleaned-up
// id used to need a suffix.
//
// The status log is deliberately left where it is. It is the only record of
// what the goblin reported and callers read it at its known path, so it stays
// readable; spawn instead treats a status log with no live metadata beside it
// as history rather than a live claim on the id.
func (s Service) archive(id string) (string, error) {
	taskTmp := filepath.Join(s.StateDir, "tasktmp", id)
	if _, err := os.Stat(taskTmp); err != nil {
		return "", nil
	}
	dir := filepath.Join(s.StateDir, ArchiveDirName, id+"."+archiveStamp())
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	// The credential script is the one thing never archived: it holds the
	// project's secrets, and a finished task has no further use for them.
	var failures []error
	if err := os.Remove(filepath.Join(taskTmp, "auth.ps1")); err != nil && !errors.Is(err, os.ErrNotExist) {
		failures = append(failures, fmt.Errorf("remove injected credentials: %w", err))
	}
	if err := os.Rename(taskTmp, dir); err != nil {
		return "", errors.Join(append(failures, fmt.Errorf("task temporary directory: %w", err))...)
	}
	return dir, errors.Join(failures...)
}

// ArchiveDirName holds the scratch directories of finished tasks, one per
// cleanup. It is a directory under the state tree so the spawn-time id
// collision scan, which considers files and the tasktmp tree, never sees it.
const ArchiveDirName = "archive"

func archiveStamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func cleanupLockName(id string) string {
	return ".cleanup-" + id + ".lock"
}

func validateMeta(meta state.TaskMeta) error {
	if meta.Backend != "herdr" {
		return fmt.Errorf("cleanup: task %s is not a Herdr task (backend %q)", meta.ID, meta.Backend)
	}
	for name, value := range map[string]string{
		"herdr_session":      meta.HerdrSession,
		"herdr_workspace_id": meta.HerdrWorkspaceID,
		"herdr_tab_id":       meta.HerdrTabID,
		"herdr_pane_id":      meta.HerdrPaneID,
		"project":            meta.Project,
		"worktree":           meta.Worktree,
	} {
		if value == "" {
			return fmt.Errorf("cleanup: task %s metadata is missing %s", meta.ID, name)
		}
	}
	return nil
}

// requireClean refuses any tracked, untracked, staged, or unstaged change.
func (s Service) requireClean(ctx context.Context, worktree string) error {
	result, err := s.Commands.Run(ctx, execx.Request{
		Dir:  worktree,
		Name: "git",
		Args: []string{"status", "--porcelain=v1", "--untracked-files=all"},
	})
	if err != nil {
		return fmt.Errorf("cleanup: inspect worktree status: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("cleanup: git status exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	if status := strings.TrimSpace(string(result.Stdout)); status != "" {
		return fmt.Errorf("cleanup: worktree %q has uncommitted or untracked changes; refusing to discard work:\n%s", worktree, status)
	}
	return nil
}

// requireInactive takes one fresh structural snapshot immediately before the
// return and proves the recorded endpoint has no agent in any state. A
// missing recorded pane, or the exact recorded pane with no registered agent,
// is sufficient inactive evidence; mismatched identity, duplicate identity,
// an unreadable snapshot, and a failed Herdr request are all refused.
func (s Service) requireInactive(ctx context.Context, meta state.TaskMeta) error {
	if s.Herdr.EffectiveSession() != meta.HerdrSession {
		return fmt.Errorf("cleanup: recorded session %q does not match the Herdr client session %q", meta.HerdrSession, s.Herdr.EffectiveSession())
	}
	snapshot, err := s.Herdr.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: Herdr endpoint evidence is unreadable: %w", err)
	}
	if snapshot.Protocol != herdr.SupportedProtocol {
		return fmt.Errorf("cleanup: Herdr session snapshot protocol %d, want %d", snapshot.Protocol, herdr.SupportedProtocol)
	}

	panes := 0
	var pane herdr.SnapshotPane
	for _, candidate := range snapshot.Panes {
		if candidate.ID == meta.HerdrPaneID {
			panes++
			pane = candidate
		}
	}
	if panes == 0 {
		return nil
	}
	if panes > 1 {
		return fmt.Errorf("cleanup: session snapshot has %d copies of pane %s; endpoint identity is ambiguous", panes, meta.HerdrPaneID)
	}
	if pane.TabID != meta.HerdrTabID || pane.WorkspaceID != meta.HerdrWorkspaceID {
		return fmt.Errorf("cleanup: pane %s belongs to tab %s workspace %s, not recorded tab %s workspace %s", pane.ID, pane.TabID, pane.WorkspaceID, meta.HerdrTabID, meta.HerdrWorkspaceID)
	}
	for _, agent := range snapshot.Agents {
		if agent.PaneID == meta.HerdrPaneID {
			return fmt.Errorf("cleanup: pane %s still has agent %q in state %q; refusing to return an active endpoint", agent.PaneID, agent.Agent, agent.Status)
		}
	}
	return nil
}

func (s Service) treehouseGit() (treehouse.Git, error) {
	if s.Treehouse.Git != nil {
		return s.Treehouse.Git, nil
	}
	if s.Treehouse.Commands == nil {
		return nil, errors.New("cleanup: treehouse Git dependency is required")
	}
	return treehouse.RunnerGit{Commands: s.Treehouse.Commands, Sleep: s.Treehouse.Sleep}, nil
}
