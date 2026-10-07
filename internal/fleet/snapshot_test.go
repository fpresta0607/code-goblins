package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPausedFleetTaskHasNoStaleAlarmAndStoppedHistoryIsExplicit(t *testing.T) {
	h := snapshotHome(t)
	meta := writeSnapshotMeta(t, h, "paused-task", t.TempDir(), t.TempDir())
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Data, "backlog.md"), []byte("## Queued\n- **paused-task** - Already dispatched\n- **next-task** - Ready to start\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteOutcome(h.State, state.Outcome{ID: "stopped-task", Phase: "stopped", Title: "Cancelled work", Project: "example", Reason: "No longer needed"}); err != nil {
		t.Fatal(err)
	}
	endpoint := &snapshotEndpoint{}
	snapshot, err := BuildSnapshot(t.Context(), h, endpoint)
	if err != nil || len(endpoint.calls) != 0 || len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Current.State != crewstate.Paused || snapshot.Tasks[0].Monitor.Health != monitor.HealthPaused || snapshot.Tasks[0].Monitor.Escalation != 0 {
		t.Fatalf("snapshot=%+v err=%v calls=%v", snapshot, err, endpoint.calls)
	}
	if len(snapshot.Backlog.Queued) != 1 || snapshot.Backlog.Queued[0].ID != "next-task" {
		t.Fatalf("a paused task is still dispatchable: %+v", snapshot.Backlog.Queued)
	}
	var output bytes.Buffer
	if err := RenderMarkdown(&output, snapshot); err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(output.String(), "## Paused")
	if !ok || !strings.Contains(after, "paused-task") || !strings.Contains(after, "Stopped | No longer needed") || strings.Contains(output.String(), "Finished") || strings.Contains(after, "(unknown)") {
		t.Fatalf("fleet view: %s", output.String())
	}
}

type snapshotEndpoint struct {
	exists     map[string]bool
	busy       map[string]herdr.BusyState
	structural map[string]bool
	calls      []string
}

func TestCompletedFleetOutcomeHasNoRunnableQueueRow(t *testing.T) {
	for _, row := range []string{"- **delivered** - Delivered", "- [ ] delivered - Delivered"} {
		t.Run(row, func(t *testing.T) {
			// Arrange
			h := snapshotHome(t)
			writeBacklog(t, h.Data, "## Queued\n"+row+"\n- [ ] next - Next task\n")
			if err := state.WriteOutcome(h.State, state.Outcome{ID: "delivered", Phase: "done", Title: "Delivered", Evidence: "reported pull request"}); err != nil {
				t.Fatal(err)
			}

			// Act
			snapshot, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Backlog.Queued) != 1 || snapshot.Backlog.Queued[0].ID != "next" || len(snapshot.Completed) != 1 || snapshot.Completed[0].ID != "delivered" {
				t.Fatalf("completed work remains queued or loses history: %+v", snapshot)
			}
		})
	}
}

func TestHerdrEndpointReadsOnlyLiveAgentEvidence(t *testing.T) {
	runner := &fakeRunner{replies: []runnerReply{
		jsonReply(`{"result":{"pane":{"pane_id":"pane-7"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
		jsonReply(`{"result":{"agent":{"agent_status":"working"}}}`),
	}}
	var sleeps []time.Duration
	endpoint := NewTerminalEndpoint(t.TempDir(), newHerdrClient(runner, &sleeps))
	meta := state.TaskMeta{ID: "g7", Backend: "herdr", HerdrSession: "fleet", HerdrPaneID: "pane-7"}

	exists, busy, err := endpoint.Read(context.Background(), meta)
	if err != nil || !exists || busy != herdr.BusyWorking {
		t.Fatalf("Read = %t, %q, %v; want true, busy, nil", exists, busy, err)
	}
	assertRequests(t, runner.requests, [][]string{
		{"pane", "get", "pane-7", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
		{"agent", "get", "pane-7", "--session", "fleet"},
	})
}

func (e *snapshotEndpoint) Read(_ context.Context, meta state.TaskMeta) (bool, herdr.BusyState, error) {
	target := herdrTarget(meta).String()
	e.calls = append(e.calls, "read:"+target)
	return e.exists[target], e.busy[target], nil
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

func TestBuildSnapshotListsTasksInTheOverlordsAttentionOrder(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	for _, id := range []string{"alpha", "beta", "gamma", "delta"} {
		writeSnapshotMeta(t, h, id, "", filepath.Join(h.Root, "project"))
	}
	if err := WriteAttention(h, []string{"gamma", "alpha"}); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := BuildSnapshot(context.Background(), h, &snapshotEndpoint{})

	// Assert
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	var ids []string
	for _, task := range snapshot.Tasks {
		ids = append(ids, task.ID)
	}
	if want := []string{"gamma", "alpha", "beta", "delta"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("tasks = %v, want the attention order first and the rest by ID", ids)
	}
}

func TestBuildSnapshotListsTasksByIDWhenTheAttentionOrderIsUnreadable(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	for _, id := range []string{"beta", "alpha"} {
		writeSnapshotMeta(t, h, id, "", filepath.Join(h.Root, "project"))
	}
	if err := os.WriteFile(filepath.Join(h.State, "attention.json"), []byte("{not a list"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := BuildSnapshot(context.Background(), h, &snapshotEndpoint{})

	// Assert
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	var ids []string
	for _, task := range snapshot.Tasks {
		ids = append(ids, task.ID)
	}
	if want := []string{"alpha", "beta"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("tasks = %v, want them by ID", ids)
	}
}

func TestBuildSnapshotFindsCaseInsensitiveMetadataExtension(t *testing.T) {
	h := snapshotHome(t)
	meta := writeSnapshotMeta(t, h, "Foo", "", filepath.Join(h.Root, "project"))
	path := filepath.Join(h.State, meta.ID+".meta")
	temporary := filepath.Join(h.State, meta.ID+".metadata-swap")
	if err := os.Rename(path, temporary); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, filepath.Join(h.State, meta.ID+".META")); err != nil {
		t.Fatal(err)
	}

	snapshot, err := BuildSnapshot(context.Background(), h, &snapshotEndpoint{})
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].ID != "Foo" {
		t.Fatalf("tasks = %+v, want Foo from Foo.META", snapshot.Tasks)
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
	if alphaRow.Actions.Peek != "cfo peek gb-alpha" {
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
	if zuluRow.Actions.Peek != "cfo peek gb-zulu" {
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

// A native task's row reads its own terminal instead of Herdr: a turn on its
// screen is working, and a composer waiting in the terminal whose host
// recorded itself under the task's id is the task's own, so its latest report
// says where it stands. With no running host the row stays unknown.
func TestBuildSnapshotReadsANativeTaskFromItsOwnTerminal(t *testing.T) {
	for _, test := range []struct {
		name    string
		hasHost bool
		screen  []string
		want    crewstate.Current
	}{
		{"a turn in progress", true, []string{"✽ Reticulating… (12s · esc to interrupt)", "", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, crewstate.Current{State: crewstate.Working, Source: crewstate.SourceEndpoint}},
		{"a composer waiting", true, []string{"> ", "⏵⏵ bypass permissions on (shift+tab to cycle)"}, crewstate.Current{State: crewstate.Done, Source: crewstate.SourceStatus, Detail: "shipped"}},
		{"no running host", false, []string{"> "}, crewstate.Current{State: crewstate.Unknown, Source: crewstate.SourceNone}},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := snapshotHome(t)
			worktree := filepath.Join(h.Root, "worktree")
			if err := os.Mkdir(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "g1", Worktree: worktree, Harness: "claude", Backend: "native"}); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(h.State, "g1", "done: shipped"); err != nil {
				t.Fatal(err)
			}
			if test.hasHost {
				recordHost(t, h.State, "g1")
			}
			screenReads := 0
			screen := func(host.Record) ([]string, error) {
				screenReads++
				return test.screen, nil
			}
			endpoint := terminalEndpoint{native: monitor.NativeProber{StateDir: h.State, ReadScreen: screen}}

			snapshot, err := BuildSnapshot(context.Background(), h, endpoint)

			if err != nil {
				t.Fatal(err)
			}
			if got := snapshot.Tasks[0].Current; got != test.want {
				t.Errorf("current = %+v, want %+v", got, test.want)
			}
			if test.hasHost && screenReads != 1 {
				t.Errorf("screen reads = %d, want one sample of the task's terminal", screenReads)
			}
		})
	}
}

// recordHost writes the record a running host keeps for terminal id.
func recordHost(t *testing.T, stateDir, id string) {
	t.Helper()
	data, err := json.Marshal(host.Record{ID: id, Pipe: `\\.\pipe\code-goblins-host-test`, Token: "token", Version: host.Version, HostPID: os.Getpid(), Started: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "hosts", id+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fleet-view names the goblin a helper works for, in its JSON and on its
// Markdown row, so the CFO sees each helper beside its parent.
func TestFleetViewNamesAHelpersParent(t *testing.T) {
	// Arrange
	h := snapshotHome(t)
	writeSnapshotMeta(t, h, "g1", t.TempDir(), t.TempDir())
	helper := writeSnapshotMeta(t, h, "g1-h1", t.TempDir(), t.TempDir())
	helper.Parent = "g1"
	if err := state.WriteTaskMeta(h.State, helper); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := BuildSnapshot(t.Context(), h, &snapshotEndpoint{})
	var markdown, data bytes.Buffer
	markdownErr, jsonErr := RenderMarkdown(&markdown, snapshot), RenderJSON(&data, snapshot)

	// Assert
	if err != nil || markdownErr != nil || jsonErr != nil {
		t.Fatalf("snapshot %v, markdown %v, json %v", err, markdownErr, jsonErr)
	}
	if !strings.Contains(markdown.String(), "| g1-h1 (helper of g1) |") || strings.Contains(markdown.String(), "| g1 (helper") {
		t.Errorf("fleet view does not name g1-h1 as g1's helper alone:\n%s", markdown.String())
	}
	var decoded struct {
		Tasks []struct {
			ID     string `json:"id"`
			Parent string `json:"parent"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, task := range decoded.Tasks {
		if want := map[string]string{"g1": "", "g1-h1": "g1"}[task.ID]; task.Parent != want {
			t.Errorf("task %s has parent %q in JSON, want %q", task.ID, task.Parent, want)
		}
	}
}
