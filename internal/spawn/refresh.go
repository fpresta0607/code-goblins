package spawn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// PaneLiveness reports whether a task's recorded Herdr pane is still live.
// It is a seam because the auth commands must decide liveness without
// depending on a particular way of asking Herdr.
type PaneLiveness interface {
	Live(ctx context.Context, meta state.TaskMeta) bool
}

// HerdrLiveness decides pane liveness through the Herdr endpoint: the exact
// recorded pane must exist and carry a registered agent. A missing pane, a
// missing agent, or an unreadable Herdr is not live - a notice no pane will
// receive must never be reported as delivered.
type HerdrLiveness struct {
	Client *herdr.Client
}

// Live implements PaneLiveness.
func (h HerdrLiveness) Live(ctx context.Context, meta state.TaskMeta) bool {
	if h.Client == nil || meta.HerdrSession == "" || meta.HerdrPaneID == "" {
		return false
	}
	status, err := h.Client.AgentStatus(ctx, herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID})
	return err == nil && status == herdr.AgentAlive
}

// Refreshed is one regenerated credential script. Live marks a pane the
// re-source notice can reach; the caller delivers that notice, because the
// notice travels over the existing fleet sender rather than a new channel.
type Refreshed struct {
	ID   string
	Path string
	Vars int
	Live bool
}

// ProjectRefresh summarizes one fleet-wide refresh pass. Unreachable is how
// a Herdr outage hides from the refreshed count: task records whose worktree
// and tasktmp are still present, but whose pane answered no liveness check,
// so the caller can say the snapshot may still be stale instead of nothing.
type ProjectRefresh struct {
	// Refreshed holds every task whose auth.ps1 was regenerated.
	Refreshed []Refreshed
	// Unreachable counts task records whose worktree and tasktmp are still
	// present but whose pane could not be confirmed live.
	Unreachable int
}

// AuthRefresher rewrites the credential script spawn rendered at dispatch, so
// a credential stored after spawn reaches a goblin that is already working
// without anybody hand-appending lines to the file. The script is always
// regenerated from the project's credential scope with the same generator the
// dispatch used, so it matches the store and is never a stale snapshot.
//
// It deliberately writes only the task's own tasktmp file. The worktree .env
// is never touched: that file is hardlink-shared with the primary checkout
// while the worktree is live, and the adoption rule pauses rather than writes
// through it.
type AuthRefresher struct {
	StateDir string
	// DataDir holds the project manifests, which decide which shared-scope
	// credentials a project's services may read.
	DataDir string
	// Store overrides the credential store. nil opens the machine's store,
	// which is what production and the env-redirected tests both want.
	Store auth.Store
	Panes PaneLiveness
}

// RefreshProject regenerates auth.ps1 for every task whose metadata names
// this project and whose worktree, tasktmp, and pane are all still live.
// A task that is not provably live is skipped: rewriting its file would
// report a refresh nothing can re-source, and hiding that is the same stale
// snapshot this exists to close.
//
// One task's failure never stops the rest. The refreshed scripts are returned
// alongside the joined failures, because a script that changed on disk must
// be reported and its pane told, whatever happened to another task. The
// summary also counts the records that were found with their worktree and
// tasktmp intact but whose pane answered no liveness check, so a caller can
// distinguish "nothing live" from "live records nobody could reach".
func (r AuthRefresher) RefreshProject(ctx context.Context, project string) (ProjectRefresh, error) {
	var result ProjectRefresh
	scope := auth.ProjectName(project)
	if scope == "" {
		return result, errors.New("spawn: a credential refresh needs a project scope")
	}
	env, err := r.scriptEnv(scope)
	if err != nil {
		return result, err
	}
	metas, failures, err := r.projectMetas(scope)
	if err != nil {
		return result, err
	}
	for _, meta := range metas {
		item, refreshed, dirsLive, err := r.refreshLive(ctx, meta, env)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if refreshed {
			result.Refreshed = append(result.Refreshed, item)
		} else if dirsLive {
			result.Unreachable++
		}
	}
	return result, errors.Join(failures...)
}

// RefreshTask regenerates one task's auth.ps1 by id. Unknown tasks and
// archived ones are refused: an unknown id names nothing to refresh, and an
// archived task's credential script was deliberately destroyed at cleanup.
// The live record is consulted first because cleanup frees an id for reuse:
// a respawned id has archived state beside a live record, and it is live.
func (r AuthRefresher) RefreshTask(ctx context.Context, id string) (Refreshed, error) {
	if err := state.ValidTaskID(id); err != nil {
		return Refreshed{}, err
	}
	meta, err := state.ReadTaskMeta(r.StateDir, id)
	if errors.Is(err, fs.ErrNotExist) {
		archived, err := r.archived(id)
		if err != nil {
			return Refreshed{}, err
		}
		if archived {
			return Refreshed{}, fmt.Errorf("spawn: task %s is archived", id)
		}
		return Refreshed{}, fmt.Errorf("spawn: unknown task %s", id)
	}
	if err != nil {
		return Refreshed{}, fmt.Errorf("spawn: read task metadata: %w", err)
	}
	if meta.TaskTmp == "" {
		return Refreshed{}, fmt.Errorf("spawn: task %s has no tasktmp", id)
	}
	scope := auth.ProjectName(meta.Project)
	if scope == "" {
		return Refreshed{}, fmt.Errorf("spawn: task %s names no project, so it has no credential scope", id)
	}
	env, err := r.scriptEnv(scope)
	if err != nil {
		return Refreshed{}, err
	}
	var item Refreshed
	err = r.locked(id, func() error {
		current, readErr := state.ReadTaskMeta(r.StateDir, id)
		if errors.Is(readErr, fs.ErrNotExist) {
			return fmt.Errorf("spawn: task %s is gone; the task is finished", id)
		}
		if readErr != nil {
			return fmt.Errorf("spawn: read task metadata: %w", readErr)
		}
		if auth.ProjectName(current.Project) != scope {
			return fmt.Errorf("spawn: task %s now records project %s; refusing to write %s credentials into it", id, auth.ProjectName(current.Project), scope)
		}
		if info, statErr := os.Stat(current.TaskTmp); statErr != nil || !info.IsDir() {
			return fmt.Errorf("spawn: task %s tasktmp %q is gone; the task is finished", id, current.TaskTmp)
		}
		var writeErr error
		item, writeErr = r.rewrite(current, env, r.Panes != nil && r.Panes.Live(ctx, current))
		return writeErr
	})
	return item, err
}

// scriptEnv is the refresh generator: everything the store says this project's
// pane should hold, declared or not. The manifest is read the way the spawn
// preflight reads it, with no manifest meaning nothing declared; see
// auth.StoredEnv for what a refresh deliberately cannot reproduce.
func (r AuthRefresher) scriptEnv(scope string) (map[string]string, error) {
	store := r.Store
	if store == nil {
		opened, err := auth.OpenStore()
		if err != nil {
			return nil, fmt.Errorf("spawn: open credential store: %w", err)
		}
		store = opened
	}
	manifest, err := auth.LoadManifest(r.DataDir, scope)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("spawn: load project manifest: %w", err)
	}
	env, err := auth.StoredEnv(store, scope, manifest)
	if err != nil {
		return nil, fmt.Errorf("spawn: gather stored credentials: %w", err)
	}
	return env, nil
}

// projectMetas reads every live task record and keeps the ones whose project
// reduces to this scope. A record that names another project is invisible to
// this refresh, exactly as its pane is invisible to this project's dispatch.
// A record that cannot be read is reported and skipped, so one corrupt record
// never blocks the fleet's refresh.
func (r AuthRefresher) projectMetas(scope string) ([]state.TaskMeta, []error, error) {
	entries, err := os.ReadDir(r.StateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("spawn: list task state: %w", err)
	}
	var metas []state.TaskMeta
	var failures []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".meta") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if err := state.ValidTaskID(id); err != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(r.StateDir, id)
		if err != nil {
			failures = append(failures, fmt.Errorf("spawn: task %s: read task metadata: %w", id, err))
			continue
		}
		if auth.ProjectName(meta.Project) == scope {
			metas = append(metas, meta)
		}
	}
	return metas, failures, nil
}

// refreshLive rewrites one task's script under its cleanup lock, so the
// liveness decision and the write see the same task: cleanup cannot archive
// the tasktmp between them, and the write can neither resurrect an archived
// directory nor ride into the archive. refreshed is false for a task that
// was skipped, which is not a failure; dirsLive distinguishes a task whose
// worktree and tasktmp are still there (its pane is what failed) from one
// whose state is already gone.
func (r AuthRefresher) refreshLive(ctx context.Context, meta state.TaskMeta, env map[string]string) (item Refreshed, refreshed, dirsLive bool, err error) {
	err = r.locked(meta.ID, func() error {
		if !r.taskDirsLive(meta) {
			return nil
		}
		dirsLive = true
		if r.Panes == nil || !r.Panes.Live(ctx, meta) {
			return nil
		}
		refreshed = true
		item, err = r.rewrite(meta, env, true)
		return err
	})
	return item, refreshed, dirsLive, err
}

// taskDirsLive requires the worktree and the tasktmp. The worktree is what
// makes the task real; the tasktmp is where the script lives. The pane is
// checked separately, because a task whose directories remain but whose pane
// answers nothing is the case a caller must still be told about.
func (r AuthRefresher) taskDirsLive(meta state.TaskMeta) bool {
	if meta.Worktree == "" || meta.TaskTmp == "" {
		return false
	}
	for _, path := range []string{meta.Worktree, meta.TaskTmp} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func (r AuthRefresher) rewrite(meta state.TaskMeta, env map[string]string, live bool) (Refreshed, error) {
	path, vars, err := writeAuthScript(meta.TaskTmp, env)
	if err != nil {
		return Refreshed{}, fmt.Errorf("spawn: regenerate %s auth.ps1: %w", meta.ID, err)
	}
	return Refreshed{ID: meta.ID, Path: path, Vars: vars, Live: live}, nil
}

// locked runs fn while holding the task's cleanup lock. A held lock means
// cleanup is archiving this task right now, and the refresh declines rather
// than waits: the task is finishing, and its script is about to be destroyed.
func (r AuthRefresher) locked(id string, fn func() error) (err error) {
	name := state.CleanupLockName(id)
	if _, err := lock.AcquireExclusiveNamed(r.StateDir, name); err != nil {
		return fmt.Errorf("spawn: task %s is being cleaned up: %w", id, err)
	}
	defer func() {
		if releaseErr := lock.ReleaseExclusiveNamed(r.StateDir, name); releaseErr != nil && err == nil {
			err = fmt.Errorf("spawn: release task lock %s: %w", name, releaseErr)
		}
	}()
	return fn()
}

// archived reports whether retained state for a cleaned-up task exists under
// the state archive. Cleanup archives the scratch directory under a stamped
// name, so the id prefix is the identity; the match is case-insensitive the
// way task id aliases are.
func (r AuthRefresher) archived(id string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(r.StateDir, state.ArchiveDirName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("spawn: list archived task state: %w", err)
	}
	for _, entry := range entries {
		if strings.EqualFold(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())), id) {
			return true, nil
		}
	}
	return false, nil
}
