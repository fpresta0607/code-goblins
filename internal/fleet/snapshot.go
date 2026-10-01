package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const snapshotSchema = "fleet-snapshot.v1"

// EndpointReader is the one endpoint dependency used to resolve each task's
// current state. BuildSnapshot does not make a separate endpoint probe for
// rendering data.
type EndpointReader interface {
	crewstate.Endpoint
}

// NewTerminalEndpoint reads each task's endpoint evidence from its own native
// terminal, the way the monitor reads it. A task an older build recorded in
// Herdr has no terminal this build can read, so it reads as not there.
func NewTerminalEndpoint(stateDir string) EndpointReader {
	return terminalEndpoint{native: monitor.NativeProber{StateDir: stateDir}}
}

type terminalEndpoint struct {
	native monitor.NativeProber
}

func (e terminalEndpoint) Read(ctx context.Context, meta state.TaskMeta) (bool, crewstate.Busy, error) {
	sample, err := e.native.Inspect(ctx, meta)
	if err != nil || sample.Verdict != monitor.ProbePresent {
		return false, crewstate.BusyUnknown, err
	}
	return true, sample.Busy, nil
}

// Validate proves an idle native terminal is the task's own. It is reached
// only after Read found the task's own host answering: a host that recorded
// itself under the task's id, on a pipe whose name is unique to the one
// launch.
func (e terminalEndpoint) Validate(_ context.Context, meta state.TaskMeta) (bool, error) {
	return meta.Backend == "native", nil
}

// Snapshot is the typed, read-only fleet view shared by JSON and Markdown
// renderers.
type Snapshot struct {
	Schema      string          `json:"schema"`
	Home        string          `json:"home"`
	Tasks       []TaskRow       `json:"tasks"`
	Backlog     BacklogRows     `json:"backlog"`
	Secondmates []SecondmateRow `json:"secondmates"`
	Completed   []state.Outcome `json:"completed"`
}

// SecondmateRow is deliberately empty because secondmates are outside Plan 3.
type SecondmateRow struct{}

// TaskRow is the complete typed projection of one local task metadata record.
type TaskRow struct {
	ID       string            `json:"id"`
	Current  crewstate.Current `json:"current_state"`
	Monitor  MonitorSummary    `json:"monitor"`
	Kind     string            `json:"kind"`
	Project  string            `json:"project"`
	Backend  string            `json:"backend"`
	Endpoint EndpointSummary   `json:"endpoint"`
	Artifact string            `json:"artifact"`
	Path     string            `json:"path"`
	Actions  Actions           `json:"actions"`
	Teardown []string          `json:"teardown,omitempty"`
}

// MonitorSummary is the renderer-facing subset of the persisted Task 4
// observation. It retains only values recorded by the monitor.
type MonitorSummary struct {
	Health               monitor.Health `json:"health"`
	StaleSeconds         int64          `json:"stale_seconds"`
	LastSeen             *time.Time     `json:"last_seen"`
	Escalation           int            `json:"escalation"`
	DemandDeepInspection bool           `json:"demand_deep_inspection"`
}

// EndpointSummary preserves the recorded Herdr identity and the monitor's
// latest endpoint verdict. Exists is nil when no trusted verdict is available.
type EndpointSummary struct {
	Target      string `json:"target"`
	Session     string `json:"session"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	PaneID      string `json:"pane_id"`
	Exists      *bool  `json:"exists"`
}

// Actions lists commands that operate on the task without adding another
// state-reading path to the renderer.
type Actions struct {
	Peek string `json:"peek"`
}

// BuildSnapshot reads local metadata, Task 4 monitor observations, and each
// current task state once. It returns a concrete empty secondmate slice because
// Plan 3 produces no secondmate rows.
func BuildSnapshot(ctx context.Context, h home.Home, endpoint EndpointReader) (Snapshot, error) {
	if endpoint == nil {
		return Snapshot{}, errors.New("fleet: endpoint reader is required")
	}
	backlog, err := ReadBacklog(h)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Schema:      snapshotSchema,
		Home:        h.Root,
		Tasks:       []TaskRow{},
		Backlog:     backlog,
		Secondmates: []SecondmateRow{},
	}

	entries, err := os.ReadDir(h.State)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return snapshot, nil
		}
		return Snapshot{}, fmt.Errorf("fleet: read state directory: %w", err)
	}
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.EqualFold(extension, ".meta") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), extension)
		if err := state.ValidTaskID(id); err != nil {
			continue
		}
		meta, err := state.ReadTaskMeta(h.State, id)
		if err != nil {
			return Snapshot{}, fmt.Errorf("fleet: read task metadata %q: %w", id, err)
		}
		snapshot.Backlog.Queued = slices.DeleteFunc(snapshot.Backlog.Queued, func(row BacklogRow) bool { return row.Structured && row.ID == id })
		current, err := crewstate.Resolve(ctx, h.State, id, endpoint)
		if err != nil {
			return Snapshot{}, fmt.Errorf("fleet: resolve current state for %q: %w", id, err)
		}
		monitorSummary, endpointExists := readMonitorSummary(h.State, id)
		var teardown []string
		record, lifecycleErr := state.ReadLifecycle(h.State, id)
		if lifecycleErr == nil {
			teardown = record.TeardownLabels()
		}
		if lifecycleErr == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(h.State) {
			monitorSummary = MonitorSummary{Health: monitor.HealthPaused}
			if record.Phase == "resuming" {
				monitorSummary.Health = monitor.HealthLaunching
			} else if record.Phase != "paused" && record.Phase != "pausing" {
				monitorSummary.Health = monitor.HealthParked
			}
			endpointExists = nil
		}
		if endpointExists == nil && currentEndpointExists(current) {
			present := true
			endpointExists = &present
		}
		artifact, err := taskArtifact(h, id)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Tasks = append(snapshot.Tasks, TaskRow{
			ID:       meta.ID,
			Current:  current,
			Monitor:  monitorSummary,
			Kind:     meta.Kind,
			Project:  meta.Project,
			Backend:  meta.Backend,
			Endpoint: endpointSummary(meta, endpointExists),
			Artifact: artifact,
			Path:     taskPath(meta),
			Actions:  Actions{Peek: "cfo peek gb-" + meta.ID},
			Teardown: teardown,
		})
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool {
		return snapshot.Tasks[i].ID < snapshot.Tasks[j].ID
	})
	// An unreadable attention order leaves the tasks in ID order.
	if attention, err := ReadAttention(h); err == nil {
		SortByAttention(snapshot.Tasks, attention, func(task TaskRow) string { return task.ID })
	}
	completed, err := os.ReadDir(filepath.Join(h.State, "outcomes"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Snapshot{}, err
	}
	for _, entry := range completed {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() {
			continue
		}
		if _, err := state.ReadTaskMeta(h.State, id); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		outcome, err := state.ReadOutcome(h.State, id)
		if err != nil {
			return Snapshot{}, err
		}
		if record, err := state.ReadLifecycle(h.State, id); err == nil && record.Generation == outcome.Generation {
			if status := record.TeardownStatus(); status != "" {
				outcome.Reason += "; " + status
			}
		}
		snapshot.Completed = append(snapshot.Completed, outcome)
	}
	return snapshot, nil
}

func currentEndpointExists(current crewstate.Current) bool {
	return current.Source == crewstate.SourceEndpoint || current.Source == crewstate.SourceStatus
}

func readMonitorSummary(stateDir, id string) (MonitorSummary, *bool) {
	observation, err := monitor.ReadObservation(stateDir, id)
	if err != nil {
		return MonitorSummary{Health: monitor.HealthUnknown}, nil
	}
	summary := MonitorSummary{
		Health:               observation.Health,
		Escalation:           observation.Escalation,
		DemandDeepInspection: observation.DemandDeepInspection,
	}
	if !observation.LastSeen.IsZero() {
		lastSeen := observation.LastSeen
		summary.LastSeen = &lastSeen
	}
	if observation.Health == monitor.HealthStale && observation.StaleSince != nil && !observation.LastObserved.Before(*observation.StaleSince) {
		summary.StaleSeconds = int64(observation.LastObserved.Sub(*observation.StaleSince) / time.Second)
	}
	return summary, endpointVerdict(observation.EndpointVerdict)
}

func endpointVerdict(verdict monitor.ProbeVerdict) *bool {
	switch verdict {
	case monitor.ProbePresent:
		present := true
		return &present
	case monitor.ProbeMissing:
		missing := false
		return &missing
	default:
		return nil
	}
}

func endpointSummary(meta state.TaskMeta, exists *bool) EndpointSummary {
	target := ""
	if meta.HerdrSession != "" && meta.HerdrPaneID != "" {
		target = meta.HerdrSession + ":" + meta.HerdrPaneID
	}
	return EndpointSummary{
		Target:      target,
		Session:     meta.HerdrSession,
		WorkspaceID: meta.HerdrWorkspaceID,
		TabID:       meta.HerdrTabID,
		PaneID:      meta.HerdrPaneID,
		Exists:      exists,
	}
}

func taskArtifact(h home.Home, id string) (string, error) {
	path := filepath.Join(h.Data, id, "report.md")
	info, err := os.Stat(path)
	if err == nil && info.Mode().IsRegular() {
		return path, nil
	}
	if errors.Is(err, os.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return "", nil
	}
	return "", fmt.Errorf("fleet: inspect report artifact for %q: %w", id, err)
}

func taskPath(meta state.TaskMeta) string {
	if meta.Worktree != "" {
		return meta.Worktree
	}
	return meta.Project
}
