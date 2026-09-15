package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

const snapshotSchema = "fleet-snapshot.v1"

// EndpointReader is the one endpoint dependency used to resolve each task's
// current state. BuildSnapshot does not make a separate endpoint probe for
// rendering data.
type EndpointReader interface {
	crewstate.Endpoint
}

// NewHerdrEndpoint adapts the supported read-only Herdr client to the
// endpoint evidence BuildSnapshot needs. It deliberately does not implement
// crewstate.StructuralValidator: idle status-log fallback requires workspace,
// tab, and label proof that an agent status response cannot establish.
func NewHerdrEndpoint(client *herdr.Client) EndpointReader {
	return herdrEndpoint{client: client, probe: monitor.NewHerdrProber(client)}
}

type herdrEndpoint struct {
	client *herdr.Client
	probe  *monitor.HerdrProber
}

func (e herdrEndpoint) Inspect(ctx context.Context, meta state.TaskMeta) (monitor.EndpointSample, error) {
	return e.probe.Inspect(ctx, meta)
}

func (e herdrEndpoint) Exists(ctx context.Context, target herdr.Target) (bool, error) {
	if e.client == nil {
		return false, errors.New("fleet: Herdr client is required")
	}
	status, err := e.client.AgentStatus(ctx, target)
	if err != nil {
		return false, err
	}
	return status == herdr.AgentAlive, nil
}

func (e herdrEndpoint) BusyState(ctx context.Context, target herdr.Target) (herdr.BusyState, error) {
	if e.client == nil {
		return herdr.BusyUnknown, errors.New("fleet: Herdr client is required")
	}
	return e.client.BusyState(ctx, target)
}

// Snapshot is the typed, read-only fleet view shared by JSON and Markdown
// renderers.
type Snapshot struct {
	Supervisor  supervisor.Health `json:"supervisor"`
	Schema      string            `json:"schema"`
	Home        string            `json:"home"`
	Tasks       []TaskRow         `json:"tasks"`
	Backlog     BacklogRows       `json:"backlog"`
	Secondmates []SecondmateRow   `json:"secondmates"`
}

// SecondmateRow is deliberately empty because secondmates are outside Plan 3.
type SecondmateRow struct{}

// TaskRow is the complete typed projection of one local task metadata record.
type TaskRow struct {
	Outcome  *Outcome          `json:"last_outcome,omitempty"`
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
}

// MonitorSummary is the renderer-facing subset of the persisted Task 4
// observation. It retains only values recorded by the monitor.
type MonitorSummary struct {
	Gate                 *monitor.GateSample `json:"gate,omitempty"`
	Reason               monitor.Reason      `json:"reason"`
	LastObserved         time.Time           `json:"last_observed"`
	Fresh                bool                `json:"fresh"`
	Health               monitor.Health      `json:"health"`
	StaleSeconds         int64               `json:"stale_seconds"`
	LastSeen             *time.Time          `json:"last_seen"`
	Escalation           int                 `json:"escalation"`
	DemandDeepInspection bool                `json:"demand_deep_inspection"`
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
	Peek    string `json:"peek"`
	Context string `json:"context"`
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
		Supervisor:  supervisor.Status(h.State),
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
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var current crewstate.Current
		var liveExists *bool
		if probe, ok := endpoint.(monitor.Prober); ok {
			sample, e := probe.Inspect(probeCtx, meta)
			current = crewstate.Current{State: crewstate.Unknown, Source: crewstate.SourceEndpoint, Detail: "backend_unavailable"}
			if e == nil {
				current.Detail = sample.Detail
				if sample.Verdict == monitor.ProbeMissing {
					v := false
					liveExists = &v
					current.Detail = "endpoint_missing"
				}
				if sample.Verdict == monitor.ProbePresent {
					v := sample.Agent == herdr.AgentAlive
					liveExists = &v
					if !v {
						current.Detail = "missing_agent; restored shell is a stopped worker"
					} else if sample.Busy == herdr.BusyWorking {
						current.State = crewstate.Working
						current.Detail = "running"
					} else {
						current.State = crewstate.Parked
						current.Detail = "registered agent awaiting input"
					}
				}
			}
		} else {
			current, err = crewstate.Resolve(probeCtx, h.State, id, endpoint)
		}
		cancel()
		if err != nil {
			return Snapshot{}, fmt.Errorf("fleet: resolve current state for %q: %w", id, err)
		}
		monitorSummary, endpointExists := readMonitorSummary(h.State, id)
		if _, ok := endpoint.(monitor.Prober); ok {
			endpointExists = liveExists
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
			Outcome:  lastOutcome(h.State, id),
			Current:  current,
			Monitor:  monitorSummary,
			Kind:     meta.Kind,
			Project:  meta.Project,
			Backend:  meta.Backend,
			Endpoint: endpointSummary(meta, endpointExists),
			Artifact: artifact,
			Path:     taskPath(meta),
			Actions:  Actions{Peek: "cfo peek gb-" + meta.ID, Context: "cfo context " + meta.ID},
		})
	}
	sort.Slice(snapshot.Tasks, func(i, j int) bool {
		return snapshot.Tasks[i].ID < snapshot.Tasks[j].ID
	})
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
		Gate:                 observation.Gate,
		Reason:               observation.Reason,
		LastObserved:         observation.LastObserved,
		Fresh:                time.Since(observation.LastObserved) >= 0 && time.Since(observation.LastObserved) < supervisor.FreshFor,
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
		target = herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}.String()
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
