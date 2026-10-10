package spawn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// PaneLiveness reports whether a task's terminal is still live. It is a seam
// so the auth commands' tests decide liveness without a running terminal.
type PaneLiveness interface {
	Live(ctx context.Context, meta state.TaskMeta) bool
}

// NativeLiveness decides liveness by a task's native terminal: it is live
// while the terminal's host runs, since the host ends with its harness. A task
// recorded in any other backend is not live: a notice no terminal will
// receive must never be reported as delivered.
type NativeLiveness struct {
	StateDir string
}

// Live implements PaneLiveness.
func (n NativeLiveness) Live(_ context.Context, meta state.TaskMeta) bool {
	return meta.Backend == "native" && nativeTerminalRuns(n.StateDir, meta.ID)
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

// ProjectRefresh summarizes one fleet-wide refresh pass. Unreachable counts
// task records whose worktree and tasktmp are still present, but whose
// terminal answered no liveness check, so the caller can say the snapshot may
// still be stale instead of nothing.
type ProjectRefresh struct {
	// Refreshed holds every task whose auth.ps1 was regenerated.
	Refreshed []Refreshed
	// Unreachable counts task records whose worktree and tasktmp are still
	// present but whose pane could not be confirmed live.
	Unreachable int
}

// AuthRefresher writes a task's credential script, so a credential stored
// after spawn, or a service granted after it, reaches a goblin that is
// already working without anybody hand-appending lines to the file. The
// script is always regenerated from the store, for the services the task's
// record names and no other, so it matches what the task's next terminal
// would be given and is never a stale snapshot.
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

// GrantMCP adds MCP servers of the project's .mcp.json to the ones task id's
// record names, so a server whose entry holds a value is given to the task. A
// harness reads its MCP servers when it starts, so the grant reaches the task
// at its next relaunch, which writes its configuration again. It is the one
// way a task comes by such a server after its spawn, and it changes no other
// task. It returns the servers the record names afterwards.
//
// A server the project's .mcp.json does not define is refused before anything
// is written, and the record is changed under the lock a switch and a resume
// hold from start to end.
func (r AuthRefresher) GrantMCP(id string, servers []string) ([]string, error) {
	if err := state.ValidTaskID(id); err != nil {
		return nil, err
	}
	meta, err := state.ReadTaskMeta(r.StateDir, id)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("spawn: %s is not a running task, and only a running task is granted an MCP server. A queued task names its servers in its brief", id)
	}
	if err != nil {
		return nil, fmt.Errorf("spawn: read task metadata: %w", err)
	}
	undefined, err := undefinedServersLine(meta.Project, servers)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	if undefined != "" {
		return nil, fmt.Errorf("spawn: no MCP server was granted to %s: %s", id, undefined)
	}
	var named []string
	lockName := state.MetadataLockName(id)
	if _, err := lock.AcquireExclusiveNamed(r.StateDir, lockName); err != nil {
		return nil, fmt.Errorf("spawn: the record of task %s is being changed by another command, as a switch or a resume of it does, so run the grant again once that has ended: %w", id, err)
	}
	err = func() (err error) {
		defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(r.StateDir, lockName)) }()
		current, err := state.ReadTaskMeta(r.StateDir, id)
		if err != nil {
			return fmt.Errorf("spawn: read task metadata: %w", err)
		}
		if current.Project != meta.Project {
			return fmt.Errorf("spawn: task %s now records project %s, so it was not granted a server of %s", id, auth.ProjectName(current.Project), auth.ProjectName(meta.Project))
		}
		named = slices.Clone(current.MCPServers)
		for _, server := range servers {
			if !slices.Contains(named, server) {
				named = append(named, server)
			}
		}
		slices.Sort(named)
		if err := state.WriteTaskMCPServers(r.StateDir, id, named); err != nil {
			return fmt.Errorf("spawn: name the granted MCP servers in the record of %s: %w", id, err)
		}
		return state.AppendStatus(r.StateDir, id, "credentials: the CFO granted the MCP servers "+strings.Join(servers, ", ")+", so the task's next terminal is given "+strings.Join(named, ", "))
	}()
	return named, err
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
	source, err := r.source(scope)
	if err != nil {
		return result, err
	}
	metas, failures, err := r.projectMetas(scope)
	if err != nil {
		return result, err
	}
	for _, meta := range metas {
		item, refreshed, dirsLive, err := r.refreshLive(ctx, meta, source)
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
	if _, err := state.ReadTaskMeta(r.StateDir, id); errors.Is(err, fs.ErrNotExist) {
		archived, err := r.archived(id)
		if err != nil {
			return Refreshed{}, err
		}
		if archived {
			return Refreshed{}, fmt.Errorf("spawn: task %s is archived", id)
		}
		return Refreshed{}, fmt.Errorf("spawn: unknown task %s", id)
	} else if err != nil {
		return Refreshed{}, fmt.Errorf("spawn: read task metadata: %w", err)
	}
	// The record is read under the task's lock, and the script is written for
	// the project and the services that one reading names. A cleanup and a
	// respawn of the id, or a grant, can therefore never leave one reading's
	// project or services in a script written for another's.
	var item Refreshed
	err := r.locked(id, func() error {
		current, err := state.ReadTaskMeta(r.StateDir, id)
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("spawn: task %s is gone; the task is finished", id)
		}
		if err != nil {
			return fmt.Errorf("spawn: read task metadata: %w", err)
		}
		if current.TaskTmp == "" {
			return fmt.Errorf("spawn: task %s has no tasktmp", id)
		}
		scope := auth.ProjectName(current.Project)
		if scope == "" {
			return fmt.Errorf("spawn: task %s names no project, so it has no credential scope", id)
		}
		if info, statErr := os.Stat(current.TaskTmp); statErr != nil || !info.IsDir() {
			return fmt.Errorf("spawn: task %s tasktmp %q is gone; the task is finished", id, current.TaskTmp)
		}
		source, err := r.source(scope)
		if err != nil {
			return err
		}
		item, err = r.rewrite(current, source, r.Panes != nil && r.Panes.Live(ctx, current))
		return err
	})
	return item, err
}

// Grant adds services of the project's manifest to the ones task id carries.
// It names them in the task's record, so every later terminal of the task
// carries them, and rewrites the task's credential script, which its running
// terminal loads. It is the one way a task comes by a service after its
// spawn, and it changes no other task. It returns the script it wrote and
// the services the record names afterwards.
//
// A service the manifest does not declare is refused before anything is
// written. The record is changed under the lock a switch and a resume hold
// from start to end, so neither can publish the record as it read it before
// the grant.
func (r AuthRefresher) Grant(ctx context.Context, id string, services []string) (Refreshed, []string, error) {
	if err := state.ValidTaskID(id); err != nil {
		return Refreshed{}, nil, err
	}
	meta, err := state.ReadTaskMeta(r.StateDir, id)
	if errors.Is(err, fs.ErrNotExist) {
		return Refreshed{}, nil, fmt.Errorf("spawn: %s is not a running task, and only a running task is granted a service. A queued task names its services in its brief", id)
	}
	if err != nil {
		return Refreshed{}, nil, fmt.Errorf("spawn: read task metadata: %w", err)
	}
	scope := auth.ProjectName(meta.Project)
	if scope == "" {
		return Refreshed{}, nil, fmt.Errorf("spawn: task %s names no project, so it has no credential scope", id)
	}
	source, err := r.source(scope)
	if err != nil {
		return Refreshed{}, nil, err
	}
	if asked := source.manifest.Grant(auth.Need{Services: services}); len(asked.Unknown) > 0 || len(services) == 0 {
		return Refreshed{}, nil, fmt.Errorf("spawn: no service was granted to %s: %s", id, auth.UnknownLine(scope, asked))
	}
	var recorded []string
	lockName := state.MetadataLockName(id)
	if _, err := lock.AcquireExclusiveNamed(r.StateDir, lockName); err != nil {
		return Refreshed{}, nil, fmt.Errorf("spawn: the record of task %s is being changed by another command, as a switch or a resume of it does, so run the grant again once that has ended: %w", id, err)
	}
	err = func() (err error) {
		defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(r.StateDir, lockName)) }()
		current, err := state.ReadTaskMeta(r.StateDir, id)
		if err != nil {
			return fmt.Errorf("spawn: read task metadata: %w", err)
		}
		if auth.ProjectName(current.Project) != scope {
			return fmt.Errorf("spawn: task %s now records project %s, so it was not granted %s's services", id, auth.ProjectName(current.Project), scope)
		}
		// What the task carries now: what its record names, or, for a task an
		// older build spawned, what its next terminal would be given.
		recorded = recordedServices(source.manifest.Grant(taskNeed(current)))
		for _, service := range services {
			if !slices.Contains(recorded, service) {
				recorded = append(recorded, service)
			}
		}
		slices.Sort(recorded)
		if err := state.WriteTaskCredentials(r.StateDir, id, recorded); err != nil {
			return fmt.Errorf("spawn: name the granted services in the record of %s: %w", id, err)
		}
		return state.AppendStatus(r.StateDir, id, "credentials: the CFO granted "+strings.Join(services, ", ")+", so the task carries "+strings.Join(recorded, ", "))
	}()
	if err != nil {
		return Refreshed{}, nil, err
	}
	item, err := r.RefreshTask(ctx, id)
	return item, recorded, err
}

// credentialSource is where a project's credential scripts are generated
// from: the store, and the manifest that says which stored names each service
// declares.
type credentialSource struct {
	scope    string
	store    auth.Store
	manifest auth.Manifest
}

// source opens the store and reads the project's manifest the way the spawn
// preflight reads it, with no manifest meaning no service declared, and so no
// credential for any task.
func (r AuthRefresher) source(scope string) (credentialSource, error) {
	store := r.Store
	if store == nil {
		opened, err := auth.OpenStore()
		if err != nil {
			return credentialSource{}, fmt.Errorf("spawn: open credential store: %w", err)
		}
		store = opened
	}
	manifest, err := auth.LoadManifest(r.DataDir, scope)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return credentialSource{}, fmt.Errorf("spawn: load project manifest: %w", err)
	}
	return credentialSource{scope: scope, store: store, manifest: manifest}, nil
}

// scriptEnv is the refresh generator: what the store holds for the services
// the task carries, and nothing else stored in its project's scope. See
// auth.StoredEnv for what a refresh deliberately cannot reproduce.
func (s credentialSource) scriptEnv(meta state.TaskMeta) (map[string]string, error) {
	carried := s.manifest.Grant(taskNeed(meta)).Services
	env, err := auth.StoredEnv(s.store, s.scope, s.manifest.Only(carried))
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
func (r AuthRefresher) refreshLive(ctx context.Context, meta state.TaskMeta, source credentialSource) (item Refreshed, refreshed, dirsLive bool, err error) {
	err = r.locked(meta.ID, func() error {
		// The record is read again under the lock: a grant may have named a
		// service since the project's records were listed, and the id may be
		// another project's task by now, which is not this refresh's.
		current, err := state.ReadTaskMeta(r.StateDir, meta.ID)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && auth.ProjectName(current.Project) != source.scope) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("spawn: task %s: read task metadata: %w", meta.ID, err)
		}
		meta = current
		if !r.taskDirsLive(meta) {
			return nil
		}
		dirsLive = true
		if r.Panes == nil || !r.Panes.Live(ctx, meta) {
			return nil
		}
		refreshed = true
		item, err = r.rewrite(meta, source, true)
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

func (r AuthRefresher) rewrite(meta state.TaskMeta, source credentialSource, live bool) (Refreshed, error) {
	env, err := source.scriptEnv(meta)
	if err != nil {
		return Refreshed{}, err
	}
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
