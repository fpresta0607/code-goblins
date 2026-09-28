// Package cleanup returns one clean, proven-inactive task worktree and closes
// its task tab. It never deletes a worktree itself, stops an agent at work, or
// discards changes: the only lifecycle calls it makes are the Herdr tab close
// of the exact recorded tab (after the endpoint is proven agent-free), the
// close of a native task's terminal (after its harness is proven idle at its
// composer, which the close ends) and worktree.Service.Return, and only after
// every guard has proven the exact recorded task safe to release. The one
// directory it removes outright holds no work - the task's Go temporary
// directory, retired with the record because it lives outside the state tree
// the archive rename carries away; see Service.removeGoTmp.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/terminal"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// Service owns the guarded cleanup of one local task.
type Service struct {
	StateDir  string
	Commands  execx.Runner
	Terminal  terminal.Backend
	Worktrees worktree.Service
	// ForceArchive retires a task whose worktree can no longer be validated -
	// a directory pinned by a dead process's handle, or already deleted out
	// from under the record. It archives the task record and leaves the
	// worktree exactly as found, so it can never discard work, though the
	// task's Go temporary directory is retired with the record. It still
	// refuses a pane that has a live agent.
	ForceArchive bool
	// LeaveRunningTerminals refuses a native task whose terminal still runs,
	// even one idle at its composer, so nothing is closed: a caller that must
	// never end a process, as reap must not, leaves that to cfo cleanup.
	LeaveRunningTerminals bool
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
	meta, err := state.ReadTaskMeta(s.StateDir, id)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: read task metadata: %w", err)
	}
	if err := validateMeta(meta); err != nil {
		return Result{}, err
	}
	// Only a Herdr task needs Herdr: a native one is proven idle and closed
	// through its own host.
	if meta.Backend == "herdr" && s.Terminal == nil {
		return Result{}, errors.New("cleanup: a Herdr task needs the Herdr terminal backend")
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
		// A worktree already deleted out from under the record cannot be
		// resolved, and that is precisely the case ForceArchive exists for: a
		// path that is not there is not the primary checkout either. Every
		// other resolution failure still refuses, because a path that exists
		// and will not resolve might be the primary checkout, and the guard
		// below cannot be applied to it.
		if !s.ForceArchive || !errors.Is(err, fs.ErrNotExist) {
			return Result{}, fmt.Errorf("cleanup: canonicalize worktree %q: %w", meta.Worktree, err)
		}
		return s.forceArchive(ctx, meta, id, meta.Worktree)
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
	idle, err := s.requireInactive(ctx, meta)
	if err != nil {
		return Result{}, err
	}

	// The endpoint is proven agent-free: close the recorded tab so a completed
	// task leaves no terminal behind, then return the worktree. A native
	// terminal idle at its composer is closed, which ends its harness.
	switch meta.Backend {
	case "herdr":
		if err := s.Terminal.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
			return Result{}, fmt.Errorf("cleanup: close task tab: %w", err)
		}
	case "native":
		if err := host.Close(s.StateDir, idle, nativeCloseWait); err != nil {
			return Result{}, fmt.Errorf("cleanup: close native terminal: %w", err)
		}
	}

	if err := s.Worktrees.Return(ctx, project, worktreePath); err != nil {
		return Result{}, fmt.Errorf("cleanup: return worktree: %w", err)
	}

	if err := state.AppendStatus(s.StateDir, id, "done: returned worktree "+worktreePath+" via cfo cleanup"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record returned worktree: %w", err)
	}
	if err := state.RemoveTaskMeta(s.StateDir, id); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	archived, archiveErr := s.archive(id)
	goTmpErr := s.removeGoTmp(id)

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
	if goTmpErr != nil {
		result.Output += fmt.Sprintf("\nwarning: %v; remove it by hand once the handle clears", goTmpErr)
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
// worktree is left for the operator (or a reboot). A native terminal's close
// is not best-effort: it is what ends an idle harness, so a failed one refuses.
func (s Service) forceArchive(ctx context.Context, meta state.TaskMeta, id, worktreePath string) (Result, error) {
	idle, err := s.requireInactive(ctx, meta)
	if err != nil {
		return Result{}, err
	}
	var notes []string
	switch meta.Backend {
	case "herdr":
		if err := s.Terminal.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
			notes = append(notes, "tab close skipped: "+err.Error())
		}
	case "native":
		if err := host.Close(s.StateDir, idle, nativeCloseWait); err != nil {
			return Result{}, fmt.Errorf("cleanup: close native terminal: %w", err)
		}
	}
	if err := state.AppendStatus(s.StateDir, id, "done: force-archived via cfo cleanup --force-archive; worktree "+worktreePath+" left in place"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record force archive: %w", err)
	}
	if err := state.RemoveTaskMeta(s.StateDir, id); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	archived, archiveErr := s.archive(id)
	goTmpErr := s.removeGoTmp(id)

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
	if goTmpErr != nil {
		result.Output += fmt.Sprintf("\nwarning: %v; remove it by hand once the handle clears", goTmpErr)
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
	if err := os.Remove(filepath.Join(taskTmp, state.AuthScriptName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove injected credentials: %w", err)
	}
	if err := os.Rename(taskTmp, dir); err != nil {
		return "", fmt.Errorf("task temporary directory: %w", err)
	}
	return dir, nil
}

// removeGoTmp removes the task's Go temporary directory, which holds build and
// test scratch a finished task no longer needs and which, living outside the
// state tree, the archive rename does not carry away.
//
// It is deliberately not part of archive(). On Windows a handle still open
// under GOTMPDIR - a killed go test binary, a background process the goblin
// started, antivirus - makes RemoveAll fail, and forceArchive exists for
// exactly that pinned-by-a-dead-handle case. Inside archive() such a failure
// would skip the credential scrub and the rename, so a locked build directory
// would leave the project's secrets on disk and the id still claimed.
func (s Service) removeGoTmp(id string) error {
	goTmp, err := state.GoTmpDir(s.StateDir, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(goTmp); err != nil {
		return fmt.Errorf("go temporary directory %s could not be removed: %w", goTmp, err)
	}
	return nil
}

// ArchiveDirName is where a finished task's scratch directory goes; see
// state.ArchiveDirName for why the name is shared.
const ArchiveDirName = state.ArchiveDirName

func archiveStamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func validateMeta(meta state.TaskMeta) error {
	required := map[string]string{"project": meta.Project, "worktree": meta.Worktree}
	switch meta.Backend {
	case "herdr":
		required["herdr_session"] = meta.HerdrSession
		required["herdr_workspace_id"] = meta.HerdrWorkspaceID
		required["herdr_tab_id"] = meta.HerdrTabID
		required["herdr_pane_id"] = meta.HerdrPaneID
	case "native":
	default:
		return fmt.Errorf("cleanup: task %s is neither a Herdr nor a native task (backend %q)", meta.ID, meta.Backend)
	}
	for name, value := range required {
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

// requireInactive proves the task's endpoint holds no agent at work, and
// returns the native terminal that is to be closed with the task, the zero
// record when there is none.
func (s Service) requireInactive(ctx context.Context, meta state.TaskMeta) (host.Record, error) {
	if meta.Backend == "native" {
		return s.requireNativeIdle(meta)
	}
	return host.Record{}, s.requireHerdrInactive(ctx, meta)
}

// requireHerdrInactive takes one fresh structural snapshot immediately before
// the return and proves the recorded endpoint has no agent in any state. A
// missing recorded pane, or the exact recorded pane with no registered agent,
// is sufficient inactive evidence; mismatched identity, duplicate identity,
// an unreadable snapshot, and a failed Herdr request are all refused.
func (s Service) requireHerdrInactive(ctx context.Context, meta state.TaskMeta) error {
	if s.Terminal.EffectiveSession() != meta.HerdrSession {
		return fmt.Errorf("cleanup: recorded session %q does not match the Herdr client session %q", meta.HerdrSession, s.Terminal.EffectiveSession())
	}
	snapshot, err := s.Terminal.Snapshot(ctx)
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

// requireNativeIdle proves a native task's terminal holds no turn in progress
// (CFO decision 2339). Its host runs exactly as long as the harness and
// removes its record on the way out, so a missing record, or one whose host
// Windows shows as ended or whose pid a later process reuses, means the
// terminal has ended. A running terminal is idle only while its harness shows
// the ready composer with no working marker, and its record is returned so
// the cleanup closes it. A record or a screen that cannot be read, any other
// screen, and a harness whose screens cfo cannot read are all refused. Under
// LeaveRunningTerminals every running terminal is refused.
func (s Service) requireNativeIdle(meta state.TaskMeta) (host.Record, error) {
	record, err := host.ReadRecord(s.StateDir, meta.ID)
	if errors.Is(err, fs.ErrNotExist) {
		return host.Record{}, nil
	}
	if err != nil {
		return host.Record{}, fmt.Errorf("cleanup: native terminal evidence is unreadable: %w", err)
	}
	if !host.Running(record) {
		return host.Record{}, nil
	}
	if s.LeaveRunningTerminals {
		return host.Record{}, fmt.Errorf("cleanup: native task %s still runs in host pid %d, and this cleanup never ends a process; retire it with cfo cleanup %s, which closes its terminal once its harness is idle", meta.ID, record.HostPID, meta.ID)
	}
	screens, ok := harness.NativeScreens(harness.Kind(meta.Harness))
	if !ok {
		return host.Record{}, fmt.Errorf("cleanup: native terminal %s runs %s, whose screen cfo cannot read; end its harness first", meta.ID, meta.Harness)
	}
	screen, err := host.ReadScreen(record)
	if err != nil {
		return host.Record{}, fmt.Errorf("cleanup: native terminal %s still runs in host pid %d and its screen cannot be read: %w", meta.ID, record.HostPID, err)
	}
	if !screens.IsReady(screen) {
		return host.Record{}, fmt.Errorf("cleanup: native terminal %s does not show its harness waiting at the composer; refusing to return an active endpoint. Its screen ends:\n%s", meta.ID, host.ScreenTail(screen, 8))
	}
	return record, nil
}

// nativeCloseWait bounds how long a busy native host is dialed again to close
// its terminal.
const nativeCloseWait = 15 * time.Second

func (s Service) worktreeGit() (worktree.Git, error) {
	if s.Worktrees.Git != nil {
		return s.Worktrees.Git, nil
	}
	if s.Worktrees.Commands == nil {
		return nil, errors.New("cleanup: worktree Git dependency is required")
	}
	return worktree.RunnerGit{Commands: s.Worktrees.Commands, Sleep: s.Worktrees.Sleep}, nil
}
