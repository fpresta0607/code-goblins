package monitor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

type fakeProber struct {
	samples map[string]EndpointSample
	errs    map[string]error
	calls   []string
}

func (f *fakeProber) Inspect(_ context.Context, meta state.TaskMeta) (EndpointSample, error) {
	f.calls = append(f.calls, meta.ID)
	if err := f.errs[meta.ID]; err != nil {
		return EndpointSample{}, err
	}
	return f.samples[meta.ID], nil
}

func capture(text string) []byte {
	return []byte(strings.Repeat(text+"\n", 200))
}

func metaFor(id string) state.TaskMeta {
	return state.TaskMeta{
		ID:               id,
		Worktree:         `C:\work\` + id,
		Backend:          "herdr",
		HerdrSession:     "fleet",
		HerdrWorkspaceID: "ws",
		HerdrTabID:       "tab-" + id,
		HerdrPaneID:      "pane-" + id,
	}
}

func sampleFor(meta state.TaskMeta, busy herdr.BusyState, text string) EndpointSample {
	status := herdr.AgentIdle
	if busy == herdr.BusyWorking {
		status = herdr.AgentWorking
	}
	sample := sampleBase(meta, text)
	sample.Busy = busy
	sample.Status = status
	return sample
}

// sampleForStatus builds a sample with an explicit native agent_status, for
// the agent_status-primary classification tests.
func sampleForStatus(meta state.TaskMeta, status, text string) EndpointSample {
	sample := sampleBase(meta, text)
	sample.Busy = herdr.BusyIdle
	if status == herdr.AgentWorking {
		sample.Busy = herdr.BusyWorking
	}
	sample.Status = status
	return sample
}

func sampleBase(meta state.TaskMeta, text string) EndpointSample {
	return EndpointSample{
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "gb-" + meta.ID,
		Agent:    herdr.AgentAlive,
		Capture:  capture(text),
	}
}

func writeTask(t *testing.T, stateDir string, meta state.TaskMeta) {
	t.Helper()
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
}

func testService(stateDir string, probe Prober, now *time.Time) Service {
	return Service{
		StateDir:              stateDir,
		Probe:                 probe,
		Now:                   func() time.Time { return *now },
		StaleEscalateAfter:    time.Minute,
		StallAfter:            time.Minute,
		BusyTurnMax:           10 * time.Minute,
		PauseResurfaceAfter:   time.Hour,
		DemandInspectionAfter: 2,
		Heartbeat:             time.Minute,
		HeartbeatMax:          4 * time.Minute,
	}
}

func TestScanFindsCaseInsensitiveMetadataExtension(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("Foo")
	writeTask(t, stateDir, meta)
	renameMetadataExtension(t, stateDir, meta.ID, "META")
	probe := &fakeProber{samples: map[string]EndpointSample{"Foo": sampleFor(meta, herdr.BusyWorking, "first")}}

	result, err := testService(stateDir, probe, &now).Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 1 || result.Observations[0].TaskID != "Foo" {
		t.Fatalf("observations = %+v, want Foo from Foo.META", result.Observations)
	}
	if got := probe.calls; len(got) != 1 || got[0] != "Foo" {
		t.Errorf("probe calls = %v, want Foo", got)
	}
}

func renameMetadataExtension(t *testing.T, stateDir, id, extension string) {
	t.Helper()
	path := filepath.Join(stateDir, id+".meta")
	temporary := filepath.Join(stateDir, id+".metadata-swap")
	if err := os.Rename(path, temporary); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temporary, filepath.Join(stateDir, id+"."+extension)); err != nil {
		t.Fatal(err)
	}
}

func TestScanIdleGraceThenStallsOnce(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "first")}}
	service := testService(stateDir, probe, &now)

	// Idle with no liveness movement is not a stall yet: the goblin is
	// legitimately thinking or running a quiet subprocess. The idle clock
	// starts but nothing wakes.
	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != nil || first.Observations[0].Health != HealthIdle || first.Observations[0].IdleSince == nil {
		t.Fatalf("first scan = %+v, want idle grace without event", first)
	}

	// Past the stall window with no liveness signal and no notify, the goblin
	// is genuinely wedged: wake once with the stall reason.
	now = now.Add(time.Minute)
	stalled, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stalled.Event == nil || stalled.Event.Kind != "stale" || stalled.Observations[0].Reason != UnchangedIdle {
		t.Fatalf("stall scan = %+v, want one unchanged-idle stall event", stalled)
	}
	if _, err := service.Publish(*stalled.Event); err != nil {
		t.Fatal(err)
	}

	// Dedupe: the same stall does not re-wake, however long it lasts.
	now = now.Add(10 * time.Minute)
	quiet, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Event != nil {
		t.Fatalf("dedupe scan = %+v, want no re-wake for the unchanged stall", quiet)
	}
}

func TestScanStatusWritesKeepAQuietGoblinFromStalling(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := testService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The pane stays quiet and herdr keeps reporting idle, but the goblin is
	// still working: it writes a status line every cycle, which must count as
	// liveness and keep pushing the stall window back.
	for i := 0; i < 5; i++ {
		now = now.Add(30 * time.Second)
		if err := state.AppendStatus(stateDir, "g1", "working: step "+strings.Repeat("x", i)); err != nil {
			t.Fatal(err)
		}
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Event != nil {
			t.Fatalf("cycle %d woke the CFO despite a fresh status write: %+v", i, result.Event)
		}
		if result.Observations[0].Health == HealthStale || result.Observations[0].IdleSince != nil {
			t.Fatalf("cycle %d observation = %+v, want a working goblin, not idle or stale", i, result.Observations[0])
		}
	}
}

func TestScanProtectsBusyThenStalesAndResurfacesPause(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now, now); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyWorking, "same")}}
	service := testService(stateDir, probe, &now)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, "g1", "paused: stale status must not override live busy"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	busy, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if busy.Event != nil || busy.Observations[0].Health != HealthBusy {
		t.Fatalf("protected busy scan = %+v, want busy without event despite paused status", busy)
	}

	stateDir = t.TempDir()
	now = time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta = metaFor("g2")
	writeTask(t, stateDir, meta)
	probe = &fakeProber{samples: map[string]EndpointSample{"g2": sampleFor(meta, herdr.BusyIdle, "same")}}
	service = testService(stateDir, probe, &now)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, "g2", "paused: waiting for vendor"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	paused, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if paused.Event == nil || paused.Observations[0].Health != HealthPaused || paused.Observations[0].Reason != DeclaredPause {
		t.Fatalf("first paused scan = %+v, want paused surface", paused)
	}
	if _, err := service.Publish(*paused.Event); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Minute)
	quiet, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Event != nil {
		t.Fatalf("pause before resurface = %+v, want quiet", quiet)
	}
	now = now.Add(time.Minute)
	resurface, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resurface.Event == nil || resurface.Observations[0].Escalation != 0 {
		t.Fatalf("pause resurface = %+v, want separate non-escalation event", resurface)
	}
}

func TestScanUnknownEndpointPreservesCorruptRecordAndContinuesAfterRestart(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "done: do not trust this"); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": {Verdict: ProbeMissing, Detail: "pane missing"}}}
	service := testService(stateDir, probe, &now)
	unknown, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Event == nil || unknown.Observations[0].Health != HealthUnknown || unknown.Observations[0].Reason != EndpointMissing || !strings.Contains(unknown.Event.Detail, "pane missing") {
		t.Fatalf("unknown endpoint scan = %+v, want actionable endpoint missing", unknown)
	}

	badPath := ObservationPath(stateDir, "g1")
	bad := []byte("not json")
	if err := os.MkdirAll(filepath.Dir(badPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	invalid, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if invalid.Event == nil || invalid.Event.Detail != string(InvalidRecord) || invalid.Observations[0].Reason != InvalidRecord {
		t.Fatalf("invalid record scan = %+v, want invalid-record event", invalid)
	}
	gotBad, err := os.ReadFile(badPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBad) != string(bad) {
		t.Errorf("corrupt record changed to %q, want preservation", gotBad)
	}

	if _, err := service.Publish(*invalid.Event); err != nil {
		t.Fatal(err)
	}
	hb, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if hb.PendingEvent != nil {
		t.Errorf("heartbeat pending event = %+v, want cleared after publish", hb.PendingEvent)
	}
}

func TestScanPreservesSemanticallyIncompleteObservationAsInvalidRecord(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	path := ObservationPath(stateDir, "g1")
	incomplete := []byte(`{"schema":"cfo-monitor.v1","task_id":"g1"}`)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, incomplete, 0o644); err != nil {
		t.Fatal(err)
	}

	service := testService(stateDir, &fakeProber{}, &now)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Event.Detail != string(InvalidRecord) || result.Observations[0].Reason != InvalidRecord {
		t.Fatalf("Scan = %+v, want an actionable invalid-record event", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(incomplete) {
		t.Errorf("incomplete observation changed to %q, want preservation", got)
	}
}

func TestScanSurfacesCorruptHeartbeatWithoutOverwritingIt(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	bad := []byte("not heartbeat JSON")
	if err := os.MkdirAll(filepath.Dir(HeartbeatPath(stateDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HeartbeatPath(stateDir), bad, 0o644); err != nil {
		t.Fatal(err)
	}

	service := testService(stateDir, &fakeProber{}, &now)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Health != HealthUnknown || result.Observations[0].Reason != InvalidRecord {
		t.Fatalf("Scan = %+v, want safe invalid-record observation and event", result)
	}
	got, err := os.ReadFile(HeartbeatPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bad) {
		t.Errorf("corrupt heartbeat changed to %q, want preservation", got)
	}
}

func TestScanBothCorruptRecordsFailsWithoutUndurableEvent(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	observationPath := ObservationPath(stateDir, "g1")
	observationBytes := []byte("not an observation")
	heartbeatBytes := []byte("not a heartbeat")
	if err := os.MkdirAll(filepath.Dir(observationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(observationPath, observationBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HeartbeatPath(stateDir), heartbeatBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	service := testService(stateDir, &fakeProber{}, &now)
	result, err := service.Scan(context.Background())
	if err == nil {
		t.Fatalf("Scan = %+v, nil; want an error because no pending event can be persisted", result)
	}
	if result.Event != nil {
		t.Errorf("Scan event = %+v, want nil when it is not durable", result.Event)
	}
	gotObservation, err := os.ReadFile(observationPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotObservation) != string(observationBytes) {
		t.Errorf("corrupt observation changed to %q, want preservation", gotObservation)
	}
	gotHeartbeat, err := os.ReadFile(HeartbeatPath(stateDir))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotHeartbeat) != string(heartbeatBytes) {
		t.Errorf("corrupt heartbeat changed to %q, want preservation", gotHeartbeat)
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("wake records = %+v, want none without a persisted event", records)
	}
}

func TestScanBootstrapsSignalsOnlyHeartbeatSchedule(t *testing.T) {
	stateDir := t.TempDir()
	firstCycle := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	if err := TouchHeartbeat(stateDir, firstCycle); err != nil {
		t.Fatal(err)
	}
	now := firstCycle.Add(10 * time.Minute)
	service := testService(stateDir, &fakeProber{}, &now)
	bootstrapped, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrapped.Heartbeat.LastHeartbeat.Equal(now) || !bootstrapped.Heartbeat.NextDue.Equal(now.Add(time.Minute)) {
		t.Fatalf("bootstrap heartbeat = %+v, want initialized schedule", bootstrapped.Heartbeat)
	}

	now = now.Add(time.Minute)
	restarted := testService(stateDir, &fakeProber{}, &now)
	due, err := restarted.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if due.Heartbeat.NoChangeStreak != 1 || !due.Heartbeat.NextDue.Equal(now.Add(2*time.Minute)) {
		t.Errorf("due heartbeat = %+v, want resumed due cadence", due.Heartbeat)
	}
}

func TestScanPersistsHeartbeatBackoffAcrossServiceRestart(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	service := testService(stateDir, &fakeProber{}, &now)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Heartbeat.NoChangeStreak != 1 || !first.Heartbeat.NextDue.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("first heartbeat = %+v, want one-step persisted backoff", first.Heartbeat)
	}

	now = now.Add(2 * time.Minute)
	restarted := testService(stateDir, &fakeProber{}, &now)
	second, err := restarted.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Heartbeat.NoChangeStreak != 2 || !second.Heartbeat.NextDue.Equal(now.Add(4*time.Minute)) {
		t.Fatalf("restart heartbeat = %+v, want continued backoff", second.Heartbeat)
	}
}

func TestScanRestartsTaskStallCadenceWithoutReset(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := testService(stateDir, probe, &now)

	active, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.Observations[0].LastSeen.IsZero() || active.Observations[0].LastProgress.IsZero() {
		t.Fatalf("active observation = %+v, want persisted progress times", active.Observations[0])
	}
	lastSeen := active.Observations[0].LastSeen
	lastProgress := active.Observations[0].LastProgress

	now = now.Add(time.Second)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	stalled, err := service.Scan(context.Background())
	if err != nil || stalled.Event == nil {
		t.Fatalf("first stall = %+v, %v", stalled, err)
	}
	if _, err := service.Publish(*stalled.Event); err != nil {
		t.Fatal(err)
	}
	staleSince := *stalled.Observations[0].StaleSince

	now = now.Add(time.Minute)
	restarted := testService(stateDir, probe, &now)
	escalated, err := restarted.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := escalated.Observations[0]
	if escalated.Event != nil {
		t.Fatalf("restart scan = %+v, want no re-wake for the unchanged stall", escalated)
	}
	if !got.LastSeen.Equal(lastSeen) || !got.LastProgress.Equal(lastProgress) || got.StaleSince == nil || !got.StaleSince.Equal(staleSince) {
		t.Errorf("restart observation = %+v, want original progress and stall times retained", got)
	}
}

func TestHeartbeatDoesNotResurfaceAStaleObservation(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := testService(stateDir, probe, &now)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	stalled, err := service.Scan(context.Background())
	if err != nil || stalled.Event == nil {
		t.Fatalf("first stall = %+v, %v", stalled, err)
	}
	if _, err := service.Publish(*stalled.Event); err != nil {
		t.Fatal(err)
	}
	// A stale goblin is doing its job; the heartbeat must not re-wake it.
	now = now.Add(2 * time.Minute)
	resurfaced, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resurfaced.Event != nil {
		t.Fatalf("heartbeat resurfaced a stale observation: %+v", resurfaced.Event)
	}
}

func TestPublishAppendsBeforeEpisodeAndRetainsPendingOnEpisodeFailure(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": {Verdict: ProbeMissing, Detail: "pane missing"}}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil {
		t.Fatal("expected persisted missing-endpoint event")
	}
	if err := os.Mkdir(filepath.Join(stateDir, ".watcher-down"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(*result.Event); err == nil {
		t.Fatal("Publish succeeded despite episode marker directory")
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != "stale" {
		t.Fatalf("wake queue = %+v, want append before episode failure", records)
	}
	obs, err := ReadObservation(stateDir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if obs.PendingEvent == nil || *obs.PendingEvent != *result.Event {
		t.Errorf("pending event = %+v, want durable retry evidence", obs.PendingEvent)
	}

	if err := os.Remove(filepath.Join(stateDir, ".watcher-down")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(*result.Event); err != nil {
		t.Fatalf("Publish retry: %v", err)
	}
	obs, err = ReadObservation(stateDir, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if obs.PendingEvent != nil {
		t.Errorf("pending event after successful publish = %+v, want nil", obs.PendingEvent)
	}

	if len(probe.calls) == 0 {
		t.Error("monitor fake did not receive the inspection-only probe call")
	}
}

func TestScanDoesNotPublishWhenObservationPersistenceFails(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := testService(stateDir, probe, &now)

	monitorDir := filepath.Join(stateDir, "monitor")
	if err := os.MkdirAll(monitorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monitorDir, "tasks"), []byte("blocks task records"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Scan(context.Background()); err == nil {
		t.Fatal("Scan succeeded despite an unwritable observation path")
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("wake records = %+v, want none before observation persistence", records)
	}
}

// A goblin whose provider is refusing it is not merely quiet: waiting longer
// will not help, so it gets its own health rather than being folded into
// stale or, worse, reported as busy because the error text keeps scrolling.
func TestScanReportsAHarnessBeingRefusedByItsProvider(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{
		"g1": sampleFor(meta, herdr.BusyWorking, "Error: 429 rate limit reached for model kimi-k2"),
	}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %d, want 1", len(result.Observations))
	}
	observation := result.Observations[0]
	if observation.Health != HealthErroring || observation.Reason != HarnessError {
		t.Fatalf("health = %q reason = %q, want %q/%q", observation.Health, observation.Reason, HealthErroring, HarnessError)
	}
	if !observation.DemandDeepInspection {
		t.Error("an erroring harness does not demand inspection, so nothing forces a decision")
	}
	if result.Event == nil || !strings.Contains(result.Event.Detail, "rate-limit") {
		t.Fatalf("event = %+v, want one naming the fault", result.Event)
	}
	// The observation has to survive a reload, or the next cycle re-raises it.
	persisted, err := ReadObservation(stateDir, "g1")
	if err != nil {
		t.Fatalf("ReadObservation: %v", err)
	}
	if persisted.Health != HealthErroring {
		t.Errorf("persisted health = %q, want %q", persisted.Health, HealthErroring)
	}
}

func TestScanRaisesOneEventPerErroringEpisode(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{
		"g1": sampleFor(meta, herdr.BusyWorking, "quota exceeded for this organization"),
	}}
	service := testService(stateDir, probe, &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	if first.Event == nil {
		t.Fatal("the first sight of the fault raised no event")
	}
	if _, err := service.Publish(*first.Event); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	now = now.Add(time.Minute)
	second, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if second.Observations[0].Health != HealthErroring {
		t.Errorf("health = %q, want it still erroring", second.Observations[0].Health)
	}
	// The fault does not resolve itself, so repeating the wake every cycle
	// would be noise the CFO learns to ignore.
	if second.Event != nil {
		t.Errorf("event = %+v, want the episode raised once", second.Event)
	}
}

func TestScanLetsAPaneRecoverOnceTheProviderStopsRefusing(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{
		"g1": sampleFor(meta, herdr.BusyWorking, "Error: 429 rate limit reached"),
	}}
	service := testService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	probe.samples["g1"] = sampleFor(meta, herdr.BusyWorking, "running tests, 42 passed")
	now = now.Add(time.Minute)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan after recovery: %v", err)
	}
	if result.Observations[0].Health == HealthErroring {
		t.Fatal("the pane stayed erroring after the provider stopped refusing")
	}
	if result.Observations[0].DemandDeepInspection {
		t.Error("a recovered pane still demands inspection")
	}
}

func TestScanWakesWhenAgentTurnEnds(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentDone, "turn ended")}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("turn-ended scan = %+v, want a harness-agnostic awaiting-input wake", result)
	}
	if !result.Observations[0].DemandDeepInspection {
		t.Error("an ended turn does not demand inspection, so nothing forces a decision")
	}
	if _, err := service.Publish(*result.Event); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	quiet, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Event != nil {
		t.Fatalf("turn-ended re-wake = %+v, want one wake per episode", quiet)
	}
}

func TestScanTreatsFreshTaskWithNoAgentAsLaunching(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now, now); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": {
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "gb-g1",
		Agent:    herdr.AgentDead,
		Busy:     herdr.BusyUnknown,
		Detail:   "pane pane-g1 has no registered agent",
	}}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Event != nil {
		t.Fatalf("a still-launching task woke the CFO: %+v", result.Event)
	}
	if len(result.Observations) != 1 {
		t.Fatalf("observations = %+v, want one", result.Observations)
	}
	obs := result.Observations[0]
	if obs.Health != HealthLaunching || obs.EndpointVerdict != ProbePresent || obs.Reason != None {
		t.Fatalf("observation = %+v, want launching with a present endpoint", obs)
	}

	probe.samples["g1"] = sampleFor(meta, herdr.BusyIdle, "first")
	now = now.Add(time.Second)
	alive, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan after the agent registers: %v", err)
	}
	if alive.Event != nil || alive.Observations[0].Health != HealthIdle {
		t.Fatalf("alive scan = %+v, want an idle baseline without an event", alive)
	}
}

func TestScanWakesWhenLaunchGraceExpiresWithoutAgent(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now, now); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": {
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "gb-g1",
		Agent:    herdr.AgentDead,
		Busy:     herdr.BusyUnknown,
		Detail:   "pane pane-g1 has no registered agent",
	}}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if result.Event != nil || result.Observations[0].Health != HealthLaunching {
		t.Fatalf("in-grace scan = %+v, want quiet launching", result)
	}

	now = now.Add(6 * time.Minute)
	result, err = service.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan after grace: %v", err)
	}
	if result.Event == nil || result.Observations[0].Reason != EndpointUnknown {
		t.Fatalf("post-grace scan = %+v, want an endpoint-unknown death wake", result)
	}
}

func TestScanDoesNotStallAParkedDecisionGoblin(t *testing.T) {
	for _, status := range []string{"blocked: Should I merge this?", "needs-decision: pick a name", "checks-passed: green gate awaiting merge"} {
		t.Run(status, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
			service := testService(stateDir, probe, &now)

			if _, err := service.Scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(stateDir, "g1", status); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				now = now.Add(time.Minute)
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event != nil {
					t.Fatalf("cycle %d woke a parked decision goblin: %+v", i, result.Event)
				}
				if result.Observations[0].Health != HealthParked || result.Observations[0].Reason != AwaitingDecision {
					t.Fatalf("cycle %d observation = %+v, want parked/awaiting_decision", i, result.Observations[0])
				}
			}
		})
	}
}

func TestScanDoesNotStallATerminalOutcomeGoblin(t *testing.T) {
	for _, status := range []string{"done: PR https://example.com/repo/pull/7", "failed: build broke"} {
		t.Run(status, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
			service := testService(stateDir, probe, &now)

			if _, err := service.Scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := state.AppendStatus(stateDir, "g1", status); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				now = now.Add(time.Minute)
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event != nil {
					t.Fatalf("cycle %d woke a terminal outcome goblin: %+v", i, result.Event)
				}
				if result.Observations[0].Health != HealthIdle || result.Observations[0].Reason != None {
					t.Fatalf("cycle %d observation = %+v, want idle/none", i, result.Observations[0])
				}
			}
		})
	}
}

func TestScanKeepsLaunchingQuietAcrossCyclesWithinGrace(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now, now); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": {
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "gb-g1",
		Agent:    herdr.AgentDead,
		Busy:     herdr.BusyUnknown,
		Detail:   "pane pane-g1 has no registered agent",
	}}}
	service := testService(stateDir, probe, &now)

	for i := 0; i < 3; i++ {
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Event != nil {
			t.Fatalf("cycle %d woke a still-launching task: %+v", i, result.Event)
		}
		if len(result.Observations) != 1 || result.Observations[0].Health != HealthLaunching {
			t.Fatalf("cycle %d observation = %+v, want launching", i, result.Observations)
		}
		persisted, err := ReadObservation(stateDir, "g1")
		if err != nil {
			t.Fatal(err)
		}
		if !persisted.LastSeen.IsZero() {
			t.Fatalf("cycle %d persisted LastSeen = %v, want zero while launching", i, persisted.LastSeen)
		}
		now = now.Add(30 * time.Second)
	}
}

func TestScanHoldsQuietADoneAgentWithTerminalVerb(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusLine string
		wantHealth Health
		wantReason Reason
	}{
		{"terminal done", "done: PR https://example.com/repo/pull/7", HealthIdle, None},
		{"terminal failed", "failed: build broke", HealthIdle, None},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			if err := state.AppendStatus(stateDir, "g1", test.statusLine); err != nil {
				t.Fatal(err)
			}
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentDone, "turn ended")}}
			service := testService(stateDir, probe, &now)

			for i := 0; i < 3; i++ {
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event != nil {
					t.Fatalf("cycle %d woke a verb-gated done agent: %+v", i, result.Event)
				}
				if result.Observations[0].Health != test.wantHealth || result.Observations[0].Reason != test.wantReason {
					t.Fatalf("cycle %d observation = %+v, want %s/%s", i, result.Observations[0], test.wantHealth, test.wantReason)
				}
				now = now.Add(time.Minute)
			}
		})
	}
}

func TestScanHoldsQuietADoneAgentWithFreshParkedVerb(t *testing.T) {
	for _, statusLine := range []string{"blocked: Should I merge this?", "needs-decision: pick a name", "checks-passed: green gate awaiting merge"} {
		t.Run(statusLine, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			if err := state.AppendStatus(stateDir, "g1", statusLine); err != nil {
				t.Fatal(err)
			}
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentDone, "turn ended")}}
			service := testService(stateDir, probe, &now)

			for i := 0; i < 3; i++ {
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event != nil {
					t.Fatalf("cycle %d woke a done agent with a fresh parked verb: %+v", i, result.Event)
				}
				if result.Observations[0].Health != HealthParked || result.Observations[0].Reason != AwaitingDecision {
					t.Fatalf("cycle %d observation = %+v, want parked/awaiting_decision", i, result.Observations[0])
				}
				now = now.Add(time.Minute)
			}
		})
	}
}

func TestScanWakesDoneAgentWithStaleParkedVerbAfterCountersAdvance(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	sample := sampleForStatus(meta, herdr.AgentDone, "turn ended")
	sample.StateChangeSeq = 10
	sample.Revision = 3
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
	service := testService(stateDir, probe, &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != nil || first.Observations[0].Health != HealthParked {
		t.Fatalf("fresh parked scan = %+v, want parked without event", first)
	}

	sample.StateChangeSeq = 11
	probe.samples["g1"] = sample
	now = now.Add(time.Minute)
	done, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if done.Event == nil || done.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("done-after-counter-advance scan = %+v, want an awaiting-input wake despite the stale parked verb", done)
	}
}

func TestScanHoldsQuietParkedVerbWhenOnlyRevisionAdvances(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	sample := sampleForStatus(meta, herdr.AgentDone, "turn ended")
	sample.StateChangeSeq = 10
	sample.Revision = 3
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
	service := testService(stateDir, probe, &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != nil || first.Observations[0].Health != HealthParked {
		t.Fatalf("fresh parked scan = %+v, want parked without event", first)
	}

	sample.Revision = 4
	probe.samples["g1"] = sample
	now = now.Add(time.Minute)
	second, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Event != nil || second.Observations[0].Health != HealthParked {
		t.Fatalf("revision-only-advance scan = %+v, want still parked without event", second)
	}
}

func TestScanWakesDoneAgentWithStaleTerminalVerbAfterCountersAdvance(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "done: PR https://example.com/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	sample := sampleForStatus(meta, herdr.AgentDone, "turn ended")
	sample.StateChangeSeq = 20
	sample.Revision = 5
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
	service := testService(stateDir, probe, &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != nil || first.Observations[0].Health != HealthIdle {
		t.Fatalf("fresh terminal scan = %+v, want idle without event", first)
	}

	sample.StateChangeSeq = 21
	probe.samples["g1"] = sample
	now = now.Add(time.Minute)
	done, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if done.Event == nil || done.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("done-after-counter-advance scan = %+v, want an awaiting-input wake despite the stale terminal verb", done)
	}
}

func TestScanWakesWhenBlockedAgentStatusHasNoVerb(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentBlocked, "blocked")}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("blocked scan = %+v, want an awaiting-input wake", result)
	}
	if result.Observations[0].Health != HealthStale {
		t.Fatalf("health = %q, want stale", result.Observations[0].Health)
	}
}

func TestScanHoldsQuietABlockedAgentWithParkedVerb(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentBlocked, "blocked")}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event != nil || result.Observations[0].Health != HealthParked || result.Observations[0].Reason != AwaitingDecision {
		t.Fatalf("blocked+parked scan = %+v, want parked/awaiting_decision without event", result)
	}
}

func TestScanWakesDoneAgentAfterResumeSupersedesStaleParkedVerb(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{
		"g1": sampleForStatus(meta, herdr.AgentBlocked, "blocked"),
	}}
	service := testService(stateDir, probe, &now)

	blocked, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Event != nil || blocked.Observations[0].Health != HealthParked {
		t.Fatalf("blocked+parked scan = %+v, want parked without event", blocked)
	}

	probe.samples["g1"] = sampleForStatus(meta, herdr.AgentWorking, "resumed")
	now = now.Add(time.Minute)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	probe.samples["g1"] = sampleForStatus(meta, herdr.AgentDone, "finished without notify")
	now = now.Add(time.Minute)
	done, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if done.Event == nil || done.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("done-after-resume scan = %+v, want an awaiting-input wake despite the stale blocked verb", done)
	}
}

func TestScanDoesNotConsumeFreshVerbSeenWhileWorking(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{samples: map[string]EndpointSample{
		"g1": sampleForStatus(meta, herdr.AgentWorking, "still working"),
	}}
	service := testService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	probe.samples["g1"] = sampleForStatus(meta, herdr.AgentBlocked, "blocked")
	now = now.Add(time.Minute)
	blocked, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Event != nil || blocked.Observations[0].Health != HealthParked {
		t.Fatalf("blocked+parked scan = %+v, want parked without a double wake", blocked)
	}
}

func TestScanWakesDoneAgentAfterWorkingOnlySawStaleParkedVerb(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "blocked: waiting for approval"); err != nil {
		t.Fatal(err)
	}
	working := sampleForStatus(meta, herdr.AgentWorking, "resumed")
	working.StateChangeSeq = 10
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": working}}
	service := testService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := sampleForStatus(meta, herdr.AgentDone, "finished without notify")
	done.StateChangeSeq = 11
	probe.samples["g1"] = done
	now = now.Add(time.Minute)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("done-after-working scan = %+v, want an awaiting-input wake despite the never-gated parked verb", result)
	}
}

func TestScanWakesDoneAgentAfterWorkingOnlySawStaleTerminalVerb(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	if err := state.AppendStatus(stateDir, "g1", "done: PR https://example.com/repo/pull/7"); err != nil {
		t.Fatal(err)
	}
	working := sampleForStatus(meta, herdr.AgentWorking, "resumed")
	working.StateChangeSeq = 20
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": working}}
	service := testService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := sampleForStatus(meta, herdr.AgentDone, "finished without notify")
	done.StateChangeSeq = 21
	probe.samples["g1"] = done
	now = now.Add(time.Minute)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || result.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("done-after-working scan = %+v, want an awaiting-input wake despite the never-gated terminal verb", result)
	}
}

func TestScanHoldsQuietAnIndeterminateUnknownAgent(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	sample := sampleForStatus(meta, herdr.AgentUnknown, "registered but indeterminate")
	sample.StateChangeSeq = 7
	sample.Revision = 3
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event != nil {
		t.Fatalf("indeterminate agent woke the CFO: %+v", result.Event)
	}
	obs := result.Observations[0]
	if obs.Health == HealthUnknown || obs.Reason == EndpointUnknown || obs.EndpointVerdict != ProbePresent {
		t.Fatalf("observation = %+v, want a present, held-quiet endpoint", obs)
	}
}
