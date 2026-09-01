package spawn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/herdr"
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
	// Store overrides the credential store. nil opens the machine's store,
	// which is what production and the env-redirected tests both want.
	Store auth.Store
	Panes PaneLiveness
	// Warn receives one line per task record that could not be read and was
	// skipped, so one corrupt record never silently blocks a fleet refresh.
	// nil discards the warnings.
	Warn io.Writer
}

// RefreshProject regenerates auth.ps1 for every task whose metadata names
// this project and whose worktree, tasktmp, and pane are all still live.
// A task that is not provably live is skipped: rewriting its file would
// report a refresh nothing can re-source, and hiding that is the same stale
// snapshot this exists to close.
func (r AuthRefresher) RefreshProject(ctx context.Context, project string) ([]Refreshed, error) {
	scope := auth.ProjectName(project)
	env, err := r.scriptEnv(scope)
	if err != nil {
		return nil, err
	}
	metas, err := r.projectMetas(scope)
	if err != nil {
		return nil, err
	}
	var refreshed []Refreshed
	var failed error
	for _, meta := range metas {
		if !r.taskLive(ctx, meta) {
			continue
		}
		item, err := r.rewrite(meta, env, true)
		if err != nil {
			failed = err
			if r.Warn != nil {
				fmt.Fprintf(r.Warn, "warning: %v\n", err)
			}
			continue
		}
		refreshed = append(refreshed, item)
	}
	if len(refreshed) == 0 && failed != nil {
		return nil, failed
	}
	return refreshed, nil
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
	if info, err := os.Stat(meta.TaskTmp); err != nil || !info.IsDir() {
		return Refreshed{}, fmt.Errorf("spawn: task %s tasktmp %q is gone; the task is finished", id, meta.TaskTmp)
	}
	env, err := r.scriptEnv(auth.ProjectName(meta.Project))
	if err != nil {
		return Refreshed{}, err
	}
	return r.rewrite(meta, env, r.Panes != nil && r.Panes.Live(ctx, meta))
}

// scriptEnv gathers everything stored under (project, *). A manifest is the
// probe contract, not a filter: a credential the operator stored mid-task is
// not declared by any manifest, and leaving it out is the defect. Harness
// billing keys are excluded here rather than in the rendered script, so a
// stored billing key can never reach a pane.
func (r AuthRefresher) scriptEnv(scope string) (map[string]string, error) {
	store := r.Store
	if store == nil {
		opened, err := auth.OpenStore()
		if err != nil {
			return nil, fmt.Errorf("spawn: open credential store: %w", err)
		}
		store = opened
	}
	keys, err := store.Keys()
	if err != nil {
		return nil, fmt.Errorf("spawn: list stored credentials: %w", err)
	}
	env := make(map[string]string)
	for _, key := range keys {
		if key.IsShared() || key.Project != scope {
			continue
		}
		if auth.IsHarnessBillingKey(key.Name) {
			continue
		}
		value, found, err := store.Get(key)
		if err != nil {
			return nil, fmt.Errorf("spawn: read stored credential %s: %w", key, err)
		}
		if !found || value == "" {
			continue
		}
		env[key.Name] = value
	}
	return env, nil
}

// projectMetas reads every live task record and keeps the ones whose project
// reduces to this scope. A record that names another project is invisible to
// this refresh, exactly as its pane is invisible to this project's dispatch.
func (r AuthRefresher) projectMetas(scope string) ([]state.TaskMeta, error) {
	entries, err := os.ReadDir(r.StateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("spawn: list task state: %w", err)
	}
	var metas []state.TaskMeta
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
			if r.Warn != nil {
				fmt.Fprintf(r.Warn, "warning: skipped task %s: read task metadata: %v\n", id, err)
			}
			continue
		}
		if auth.ProjectName(meta.Project) == scope {
			metas = append(metas, meta)
		}
	}
	return metas, nil
}

// taskLive requires the worktree, the tasktmp, and the pane. The worktree is
// what makes the task real; the tasktmp is where the script lives; the pane
// is what can still re-source it.
func (r AuthRefresher) taskLive(ctx context.Context, meta state.TaskMeta) bool {
	if meta.Worktree == "" || meta.TaskTmp == "" {
		return false
	}
	for _, path := range []string{meta.Worktree, meta.TaskTmp} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return false
		}
	}
	return r.Panes != nil && r.Panes.Live(ctx, meta)
}

func (r AuthRefresher) rewrite(meta state.TaskMeta, env map[string]string, live bool) (Refreshed, error) {
	path, vars, err := writeAuthScript(meta.TaskTmp, env)
	if err != nil {
		return Refreshed{}, fmt.Errorf("spawn: regenerate %s auth.ps1: %w", meta.ID, err)
	}
	return Refreshed{ID: meta.ID, Path: path, Vars: vars, Live: live}, nil
}

// archived reports whether retained state for a cleaned-up task exists under
// the state archive. Cleanup archives the scratch directory under a stamped
// name, so the id prefix is the identity; the match is case-insensitive the
// way task id aliases are.
func (r AuthRefresher) archived(id string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(r.StateDir, archiveDirName))
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

// archiveDirName is cleanup's archive directory. Kept as a literal here
// rather than imported: cleanup is a caller of task lifecycle, and spawn must
// not depend on it to name a directory layout both sides own a half of.
const archiveDirName = "archive"
