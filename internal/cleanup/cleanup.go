// Package cleanup returns one clean, proven-inactive task worktree and closes
// its task tab. It never deletes a worktree itself, stops an agent, or
// discards changes: the only lifecycle calls it makes are the Herdr tab close
// of the exact recorded tab (after the endpoint is proven agent-free) and
// worktree.Service.Return, and only after every guard has proven the exact
// recorded task safe to release. The one directory it removes outright holds
// no work - the task's Go temporary directory, retired with the record
// because it lives outside the state tree the archive rename carries away;
// see Service.removeGoTmp.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/taskcontext"
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
	// worktree exactly as found, so it can never discard work, though the
	// task's Go temporary directory is retired with the record. It still
	// refuses a pane that has a live agent.
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
	contextHome := home.Home{Root: filepath.Dir(s.StateDir), State: s.StateDir}
	retirement, retirementErr := taskcontext.ReadRetirementState(contextHome, id)
	if retirementErr == nil {
		if retirement.Meta != meta {
			return Result{}, errors.New("cleanup: staged retirement identity differs from live task metadata")
		}
		if _, statErr := os.Lstat(meta.Worktree); errors.Is(statErr, os.ErrNotExist) {
			if err := s.proveWorktreeReturned(ctx, project, meta.Worktree); err != nil {
				return Result{}, err
			}
			if err := s.requireInactive(ctx, meta); err != nil {
				return Result{}, err
			}
			if err := s.requireRetirementReady(ctx, contextHome, id, meta); err != nil {
				return Result{}, err
			}
			return s.finishRetirement(contextHome, meta, id, meta.Worktree)
		} else if statErr != nil {
			return Result{}, fmt.Errorf("cleanup: inspect staged worktree: %w", statErr)
		}
		if _, err := taskcontext.ReadRetirement(contextHome, id); err == nil {
			return Result{}, errors.New("cleanup: completed retirement conflicts with a live worktree")
		}
	} else if !errors.Is(retirementErr, os.ErrNotExist) {
		return Result{}, fmt.Errorf("cleanup: read staged retirement proof: %w", retirementErr)
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
	var proof worktree.MergeProof
	if meta.Mode == "local-only" {
		proof, err = worktree.ProveLocalMerged(ctx, s.Commands, project, worktreePath)
	} else {
		proof, err = worktree.ProveMerged(ctx, s.Commands, worktreePath)
	}
	if err != nil {
		return Result{}, err
	}
	if err := s.requireInactive(ctx, meta); err != nil {
		return Result{}, err
	}
	if err := s.requireRetirementReady(ctx, contextHome, id, meta); err != nil {
		return Result{}, err
	}

	// The endpoint is proven agent-free: close the recorded tab so a completed
	// task leaves no terminal behind, then return the worktree.
	if err := s.Herdr.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
		return Result{}, fmt.Errorf("cleanup: close task tab: %w", err)
	}

	if err := taskcontext.StageRetirement(contextHome, meta, proof); err != nil {
		return Result{}, fmt.Errorf("cleanup: stage retirement proof: %w", err)
	}
	if err := s.Worktrees.Return(ctx, project, worktreePath); err != nil {
		return Result{}, fmt.Errorf("cleanup: return worktree: %w", err)
	}
	return s.finishRetirement(contextHome, meta, id, worktreePath)
}

func (s Service) requireRetirementReady(ctx context.Context, contextHome home.Home, id string, meta state.TaskMeta) error {
	manifest, err := taskcontext.Refresh(ctx, contextHome, id, s.Commands)
	if err != nil {
		return fmt.Errorf("cleanup: preserve task context: %w", err)
	}
	recap, err := os.Stat(manifest.Recap)
	if err != nil || recap.IsDir() || recap.Size() == 0 {
		return errors.New("cleanup: save the finished branch recap before retirement")
	}
	if len(manifest.Decisions) > 0 {
		return errors.New("cleanup: unresolved decisions must be answered before retirement")
	}
	if meta.Mode != "no-mistakes" {
		return nil
	}
	launch, err := pipeline.LoadLaunch(filepath.Join(filepath.Dir(manifest.Manifest), "pipeline-launch.json"))
	if err != nil {
		return fmt.Errorf("cleanup: native gate association unavailable: %w", err)
	}
	root, err := pipeline.DefaultRoot()
	if err != nil {
		return err
	}
	run, err := (pipeline.Reader{Root: root, Commands: s.Commands}).BoundRun(ctx, launch)
	if err != nil || run.Status != "completed" {
		return errors.New("cleanup: native gate remains unresolved or unreadable")
	}
	return nil
}

func (s Service) proveWorktreeReturned(ctx context.Context, project, worktreePath string) error {
	if _, err := os.Lstat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return fmt.Errorf("cleanup: staged worktree %q still exists", worktreePath)
		}
		return fmt.Errorf("cleanup: inspect staged worktree %q: %w", worktreePath, err)
	}
	result, err := s.Commands.Run(ctx, execx.Request{Dir: project, Name: "git", Args: []string{"worktree", "list", "--porcelain"}})
	if err != nil || result.ExitCode != 0 {
		return errors.New("cleanup: cannot prove returned worktree is absent from Git administration")
	}
	want, err := fsx.AbsClean(worktreePath)
	if err != nil {
		return fmt.Errorf("cleanup: normalize staged worktree: %w", err)
	}
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		listed, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		listed, err = fsx.AbsClean(listed)
		if err != nil {
			return fmt.Errorf("cleanup: normalize registered worktree: %w", err)
		}
		same := listed == want
		if runtime.GOOS == "windows" {
			same = strings.EqualFold(listed, want)
		}
		if same {
			return fmt.Errorf("cleanup: staged worktree %q remains registered", worktreePath)
		}
	}
	return nil
}

func (s Service) finishRetirement(contextHome home.Home, meta state.TaskMeta, id, worktreePath string) (Result, error) {
	archived, err := s.archive(meta)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: scrub and archive task scratch: %w", err)
	}
	if err := taskcontext.CompleteRetirement(contextHome, id); err != nil {
		return Result{}, fmt.Errorf("cleanup: preserve retirement proof: %w", err)
	}

	if err := state.AppendStatus(s.StateDir, id, "done: returned worktree "+worktreePath+" via cfo cleanup"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record returned worktree: %w", err)
	}
	if err := state.RemoveTaskMeta(s.StateDir, id); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	goTmpErr := s.removeGoTmp(id)

	result := Result{Meta: meta, Output: fmt.Sprintf("cleaned %s worktree=%s", id, worktreePath)}
	if archived != "" {
		result.Output += " archive=" + archived
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
// worktree is left for the operator (or a reboot).
func (s Service) forceArchive(ctx context.Context, meta state.TaskMeta, id, worktreePath string) (Result, error) {
	if err := s.requireInactive(ctx, meta); err != nil {
		return Result{}, err
	}
	var notes []string
	if err := s.Herdr.CloseTab(ctx, meta.HerdrSession, meta.HerdrTabID); err != nil {
		notes = append(notes, "tab close skipped: "+err.Error())
	}
	archived, err := s.archive(meta)
	if err != nil {
		return Result{}, fmt.Errorf("cleanup: scrub and archive task scratch: %w", err)
	}
	if err := state.AppendStatus(s.StateDir, id, "done: force-archived via cfo cleanup --force-archive; worktree "+worktreePath+" left in place"); err != nil {
		return Result{}, fmt.Errorf("cleanup: record force archive: %w", err)
	}
	if err := state.RemoveTaskMeta(s.StateDir, id); err != nil {
		return Result{}, fmt.Errorf("cleanup: retire task metadata: %w", err)
	}
	goTmpErr := s.removeGoTmp(id)

	result := Result{Meta: meta, Output: fmt.Sprintf("force-archived %s; worktree %s left in place, remove it by hand when its handle clears", id, worktreePath)}
	if archived != "" {
		result.Output += " archive=" + archived
	}
	for _, note := range notes {
		result.Output += "\nnote: " + note
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
func (s Service) archive(meta state.TaskMeta) (string, error) {
	taskTmp := filepath.Join(s.StateDir, "tasktmp", meta.ID)
	expected, err := fsx.AbsClean(taskTmp)
	if err != nil {
		return "", err
	}
	recorded, err := fsx.AbsClean(meta.TaskTmp)
	if err != nil || !sameSpelledPath(recorded, expected) {
		return "", errors.New("task temporary path does not match task ownership")
	}
	info, err := os.Lstat(expected)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect task temporary directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("task temporary path is not an owned directory")
	}
	dir := filepath.Join(s.StateDir, ArchiveDirName, meta.ID+"."+archiveStamp())
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	// Both generated launch inputs may contain credentials. Preserve frozen
	// policy and handoff evidence, but refuse archival if either cannot be scrubbed.
	for _, name := range []string{state.AuthScriptName, "mcp.json"} {
		if err := os.Remove(filepath.Join(expected, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("remove injected credential file %s: %w", name, err)
		}
	}
	if err := os.Rename(expected, dir); err != nil {
		return "", fmt.Errorf("task temporary directory: %w", err)
	}
	return dir, nil
}

func sameSpelledPath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
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
