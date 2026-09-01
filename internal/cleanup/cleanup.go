// Package cleanup returns one clean, proven-inactive task worktree and closes
// its task tab. It never deletes a directory itself, stops an agent, or
// discards changes: the only lifecycle calls it makes are the Herdr tab close
// of the exact recorded tab (after the endpoint is proven agent-free) and
// worktree.Service.Return, and only after every guard has proven the exact
// recorded task safe to release.
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
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// Service owns the guarded cleanup of one local task.
type Service struct {
	StateDir  string
	Commands  execx.Runner
	Herdr     *herdr.Client
	Worktrees worktree.Service
	// ForceArchive retires a task whose worktree can no longer be validated -
	// a directory pinned by a dead process's handle, or already deleted out
	// from under the record. It archives the task record and leaves the
	// directory exactly as found: nothing on disk is deleted, so it can never
	// discard work. It still refuses a pane that has a live agent.
	ForceArchive bool
}

// Result reports the exact returned task identity.
type Result struct {
	Meta   state.TaskMeta
	Output string
}

// Cleanup validates one task's metadata and isolation, proves its worktree
// clean and its Herdr endpoint inactive, then delegates the worktree release
// to worktree.Service.Return. Task metadata is preserved whenever any check
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

	if _, err := lock.AcquireExclusiveNamed(s.StateDir, state.CleanupLockName(id)); err != nil {
		return Result{}, fmt.Errorf("cleanup: acquire task lock: %w", err)
	}
	defer func() {
		if releaseErr := lock.ReleaseExclusiveNamed(s.StateDir, state.CleanupLockName(id)); releaseErr != nil {
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
	worktreePath, err := fsx.Canonical(meta.Worktree)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: canonicalize worktree %q: %w", meta.Worktree, err)
	}
	if fsx.SamePath(worktreePath, project) {
		return Result{}, fmt.Errorf("cleanup: worktree %q is the primary checkout", worktreePath)
	}
	if s.ForceArchive {
		return s.forceArchive(ctx, meta, id, worktreePath)
	}

	git, err := s.worktreeGit()
	if err != nil {
		return Result{}, err
	}
	if err := worktree.Validate(ctx, git, project, worktreePath); err != nil {
		return Result{}, fmt.Errorf("cleanup: validate worktree: %w", err)
	}
	if err := s.requireClean(ctx, worktreePath); err != nil {
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

	if err := s.Worktrees.Return(ctx, project, worktreePath); err != nil {
		return Result{}, fmt.Errorf("cleanup: return worktree: %w", err)
	}

	if err := state.AppendStatus(s.StateDir, id, "done: returned worktree "+worktreePath+" via cfo cleanup"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record returned worktree: %w", err)
	}
	if err := os.Remove(filepath.Join(s.StateDir, id+".meta")); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	archived, archiveErr := s.archive(id)

	result.Meta = meta
	result.Output = fmt.Sprintf("cleaned %s worktree=%s", id, worktreePath)
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

// forceArchive retires a task record without touching its worktree. It is the
// path for a worktree that will not validate - the Utah stub sat in the fleet
// for days as "Under Way" because its empty directory was pinned by a handle
// no process would give up, and the normal path refuses anything it cannot
// prove. The one check that stays is the live-agent refusal: a task is
// retired, never abandoned mid-run. The tab close is best-effort because the
// pane is usually already gone, and no worktree return is attempted, so the
// directory is left for the operator (or a reboot) and nothing is deleted.
func (s Service) forceArchive(ctx context.Context, meta state.TaskMeta, id, worktreePath string) (Result, error) {
	if err := s.requireInactive(ctx, meta); err != nil {
		return Result{}, err
	}
	var notes []string
	if err := s.Herdr.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
		notes = append(notes, "tab close skipped: "+err.Error())
	}
	if err := state.AppendStatus(s.StateDir, id, "done: force-archived via cfo cleanup --force-archive; worktree "+worktreePath+" left in place"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record force archive: %w", err)
	}
	if err := os.Remove(filepath.Join(s.StateDir, id+".meta")); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	archived, archiveErr := s.archive(id)

	result := Result{Meta: meta, Output: fmt.Sprintf("force-archived %s; worktree %s left in place, remove it by hand when its handle clears", id, worktreePath)}
	if archived != "" {
		result.Output += " archive=" + archived
	}
	for _, note := range notes {
		result.Output += "\nnote: " + note
	}
	if archiveErr != nil {
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
	// project's secrets, and a finished task has no further use for them. If
	// it cannot be dropped, refuse to archive rather than move a directory
	// that still holds credentials.
	if err := os.Remove(filepath.Join(taskTmp, "auth.ps1")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove injected credentials: %w", err)
	}
	if err := os.Rename(taskTmp, dir); err != nil {
		return "", fmt.Errorf("task temporary directory: %w", err)
	}
	return dir, nil
}

// ArchiveDirName is where a finished task's scratch directory goes; see
// state.ArchiveDirName for why the name is shared.
const ArchiveDirName = state.ArchiveDirName

func archiveStamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
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

func (s Service) worktreeGit() (worktree.Git, error) {
	if s.Worktrees.Git != nil {
		return s.Worktrees.Git, nil
	}
	if s.Worktrees.Commands == nil {
		return nil, errors.New("cleanup: worktree Git dependency is required")
	}
	return worktree.RunnerGit{Commands: s.Worktrees.Commands, Sleep: s.Worktrees.Sleep}, nil
}
