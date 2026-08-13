package fleet

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type snapshotEndpoint struct {
	exists     map[string]bool
	busy       map[string]herdr.BusyState
	structural map[string]bool
	calls      []string
}

func (e *snapshotEndpoint) Exists(_ context.Context, target herdr.Target) (bool, error) {
	e.calls = append(e.calls, "exists:"+target.String())
	return e.exists[target.String()], nil
}

func (e *snapshotEndpoint) BusyState(_ context.Context, target herdr.Target) (herdr.BusyState, error) {
	e.calls = append(e.calls, "busy:"+target.String())
	return e.busy[target.String()], nil
}

func (e *snapshotEndpoint) Validate(_ context.Context, meta state.TaskMeta) (bool, error) {
	e.calls = append(e.calls, "validate:"+meta.ID)
	return e.structural[meta.ID], nil
}

func snapshotHome(t *testing.T) home.Home {
	t.Helper()
	root := t.TempDir()
	h := home.Home{Root: root, State: filepath.Join(root, "state"), Data: filepath.Join(root, "data")}
	if err := os.MkdirAll(h.State, 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func writeSnapshotMeta(t *testing.T, h home.Home, id string, worktree string, project string) state.TaskMeta {
	t.Helper()
	meta := state.TaskMeta{
		ID:               id,
		Worktree:         worktree,
		Project:          project,
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "workspace-" + id,
		HerdrTabID:       "tab-" + id,
		HerdrPaneID:      "pane-" + id,
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

func writeObservation(t *testing.T, h home.Home, observation monitor.Observation) {
	t.Helper()
	if err := monitor.WriteObservation(h.State, observation); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSnapshotSortsRowsAndProjectsTypedTaskState(t *testing.T) {
	h := snapshotHome(t)
	alphaWorktree := filepath.Join(h.Root, "alpha-worktree")
	if err := os.Mkdir(alphaWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	alpha := writeSnapshotMeta(t, h, "alpha", alphaWorktree, filepath.Join(h.Root, "alpha-project"))
	middle := writeSnapshotMeta(t, h, "middle", "", filepath.Join(h.Root, "middle-project"))
	zuluWorktree := filepath.Join(h.Root, "zulu-worktree")
	if err := os.Mkdir(zuluWorktree, 0o755); err != nil {
		t.Fatal(err)
	}
	zulu := writeSnapshotMeta(t, h, "zulu", zuluWorktree, filepath.Join(h.Root, "zulu-project"))

	lastSeen := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	writeObservation(t, h, monitor.Observation{
		TaskID:          alpha.ID,
		Endpoint:        "fleet:pane-alpha",
		EndpointVerdict: monitor.ProbePresent,
		Digest:          "alpha-digest",
		LastObserved:    lastSeen,
		LastSeen:        lastSeen,
		LastProgress:    lastSeen,
		Health:          monitor.HealthActive,
		Reason:          monitor.None,
	})
	staleSince := lastSeen.Add(time.Minute)
	nextEscalation := staleSince.Add(time.Minute)
	writeObservation(t, h, monitor.Observation{
		TaskID:               zulu.ID,
		Endpoint:             "fleet:pane-zulu",
		EndpointVerdict:      monitor.ProbePresent,
		Digest:               "zulu-digest",
		LastObserved:         staleSince.Add(10 * time.Second),
		LastSeen:             lastSeen,
		LastProgress:         lastSeen,
		StaleSince:           &staleSince,
		NextEscalation:       &nextEscalation,
		Health:               monitor.HealthStale,
		Reason:               monitor.UnchangedIdle,
		Escalation:           2,
		DemandDeepInspection: true,
	})
	if err := os.MkdirAll(filepath.Join(h.Data, alpha.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(h.Data, alpha.ID, "report.md")
	if err := os.WriteFile(report, []byte("report"), 0o644); err != nil {
		t.Fatal(err)
	}

	endpoint := &snapshotEndpoint{
		exists: map[string]bool{
			"fleet:pane-alpha": true,
			"fleet:pane-zulu":  true,
		},
		busy: map[string]herdr.BusyState{
			"fleet:pane-alpha": herdr.BusyWorking,
			"fleet:pane-zulu":  herdr.BusyIdle,
		},
		structural: map[string]bool{"zulu": true},
	}
	snapshot, err := BuildSnapshot(context.Background(), h, endpoint)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}

	if snapshot.Schema != "fleet-snapshot.v1" || snapshot.Home != h.Root {
		t.Errorf("snapshot identity = %+v, want schema and home", snapshot)
	}
	if len(snapshot.Secondmates) != 0 || snapshot.Secondmates == nil {
		t.Errorf("secondmates = %#v, want concrete empty slice", snapshot.Secondmates)
	}
	if got := []string{snapshot.Tasks[0].ID, snapshot.Tasks[1].ID, snapshot.Tasks[2].ID}; !reflect.DeepEqual(got, []string{"alpha", "middle", "zulu"}) {
		t.Errorf("task IDs = %v, want deterministic ID order", got)
	}

	alphaRow := snapshot.Tasks[0]
	if alphaRow.Kind != "ship" || alphaRow.Path != alpha.Worktree || alphaRow.Artifact != report {
		t.Errorf("alpha row = %+v, want metadata defaults, worktree path, and report artifact", alphaRow)
	}
	if alphaRow.Current != (crewstate.Current{State: crewstate.Working, Source: crewstate.SourceEndpoint}) {
		t.Errorf("alpha current = %+v, want working endpoint state", alphaRow.Current)
	}
	if alphaRow.Endpoint.Target != "fleet:pane-alpha" || alphaRow.Endpoint.Session != alpha.HerdrSession || alphaRow.Endpoint.WorkspaceID != alpha.HerdrWorkspaceID || alphaRow.Endpoint.TabID != alpha.HerdrTabID || alphaRow.Endpoint.PaneID != alpha.HerdrPaneID || alphaRow.Endpoint.Exists == nil || !*alphaRow.Endpoint.Exists {
		t.Errorf("alpha endpoint = %+v, want complete present Herdr endpoint", alphaRow.Endpoint)
	}
	if alphaRow.Monitor.Health != monitor.HealthActive || alphaRow.Monitor.StaleSeconds != 0 || alphaRow.Monitor.LastSeen == nil || !alphaRow.Monitor.LastSeen.Equal(lastSeen) {
		t.Errorf("alpha monitor = %+v, want active projection", alphaRow.Monitor)
	}
	if alphaRow.Actions.Peek != "cfo peek fm-alpha" {
		t.Errorf("alpha actions = %+v, want typed peek action", alphaRow.Actions)
	}

	middleRow := snapshot.Tasks[1]
	if middleRow.Path != middle.Project || middleRow.Endpoint.Exists != nil || middleRow.Monitor.Health != monitor.HealthUnknown || middleRow.Monitor.LastSeen != nil {
		t.Errorf("middle row = %+v, want project fallback and unknown typed summaries", middleRow)
	}

	zuluRow := snapshot.Tasks[2]
	if zuluRow.Monitor.Health != monitor.HealthStale || zuluRow.Monitor.StaleSeconds != 10 || zuluRow.Monitor.Escalation != 2 || !zuluRow.Monitor.DemandDeepInspection {
		t.Errorf("zulu monitor = %+v, want persisted stale monitor summary", zuluRow.Monitor)
	}
	if zuluRow.Actions.Peek != "cfo peek fm-zulu" {
		t.Errorf("zulu actions = %+v, want task-local peek action", zuluRow.Actions)
	}
}

func TestBuildSnapshotConvertsInvalidObservationToUnknown(t *testing.T) {
	h := snapshotHome(t)
	worktree := filepath.Join(h.Root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSnapshotMeta(t, h, "g1", worktree, "")
	path := monitor.ObservationPath(h.State, "g1")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not JSON"), 0o644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := BuildSnapshot(context.Background(), h, &snapshotEndpoint{})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Monitor != (MonitorSummary{Health: monitor.HealthUnknown}) {
		t.Errorf("monitor projection = %+v, want typed unknown from invalid record", snapshot.Tasks)
	}
}

func TestBuildSnapshotUsesCurrentEndpointEvidenceWhenMonitorIsAbsent(t *testing.T) {
	h := snapshotHome(t)
	worktree := filepath.Join(h.Root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := writeSnapshotMeta(t, h, "g1", worktree, "")
	endpoint := &snapshotEndpoint{
		exists: map[string]bool{meta.HerdrSession + ":" + meta.HerdrPaneID: true},
		busy:   map[string]herdr.BusyState{meta.HerdrSession + ":" + meta.HerdrPaneID: herdr.BusyWorking},
	}

	snapshot, err := BuildSnapshot(context.Background(), h, endpoint)
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	row := snapshot.Tasks[0]
	if row.Current.Source != crewstate.SourceEndpoint || row.Endpoint.Exists == nil || !*row.Endpoint.Exists {
		t.Errorf("row = %+v, want current endpoint evidence to mark the endpoint present", row)
	}
}
