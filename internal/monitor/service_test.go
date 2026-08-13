package monitor

import (
	"context"
	"crypto/sha256"
	"fmt"
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
	return EndpointSample{
		Verdict: ProbePresent,
		Endpoint: herdr.Endpoint{
			Target:      herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID},
			WorkspaceID: meta.HerdrWorkspaceID,
			TabID:       meta.HerdrTabID,
			PaneID:      meta.HerdrPaneID,
		},
		TabLabel: "fm-" + meta.ID,
		Agent:    herdr.AgentAlive,
		Busy:     busy,
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
		BusyTurnMax:           10 * time.Minute,
		PauseResurfaceAfter:   time.Hour,
		DemandInspectionAfter: 2,
		Heartbeat:             time.Minute,
		HeartbeatMax:          4 * time.Minute,
	}
}

func TestScanPersistsDigestAndEscalatesUnchangedIdle(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "first")}}
	service := testService(stateDir, probe, &now)

	first, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != nil || len(first.Observations) != 1 || first.Observations[0].Health != HealthActive {
		t.Fatalf("first scan = %+v, want active baseline without event", first)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(capture("first")))
	if first.Observations[0].Digest != wantDigest {
		t.Errorf("digest = %q, want %q", first.Observations[0].Digest, wantDigest)
	}

	now = now.Add(time.Second)
	stale, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stale.Event == nil || stale.Event.Kind != "stale" || stale.Observations[0].Reason != UnchangedIdle || stale.Observations[0].Escalation != 0 {
		t.Fatalf("idle scan = %+v, want first unchanged-idle event without escalation", stale)
	}
	if _, err := service.Publish(*stale.Event); err != nil {
		t.Fatal(err)
	}

	now = now.Add(30 * time.Second)
	quiet, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if quiet.Event != nil || quiet.Observations[0].Escalation != 0 {
		t.Fatalf("before escalation due = %+v, want no event", quiet)
	}

	now = now.Add(30 * time.Second)
	one, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if one.Event == nil || one.Observations[0].Escalation != 1 || one.Observations[0].DemandDeepInspection {
		t.Fatalf("first escalation = %+v, want escalation one", one)
	}
	if _, err := service.Publish(*one.Event); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	two, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if two.Event == nil || two.Observations[0].Escalation != 2 || !two.Observations[0].DemandDeepInspection || !strings.Contains(two.Event.Detail, "demand-deep-inspection") {
		t.Fatalf("second escalation = %+v, want deep inspection", two)
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
	if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), now.Add(-11*time.Minute), now.Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	wedge, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if wedge.Event == nil || wedge.Observations[0].Reason != BusyTurnOverAge || wedge.Observations[0].Health != HealthStale {
		t.Fatalf("expired busy scan = %+v, want busy-turn-over-age stale", wedge)
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

func TestScanRestartsTaskStaleCadenceWithoutReset(t *testing.T) {
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
	stale, err := service.Scan(context.Background())
	if err != nil || stale.Event == nil {
		t.Fatalf("first stale = %+v, %v", stale, err)
	}
	if _, err := service.Publish(*stale.Event); err != nil {
		t.Fatal(err)
	}
	staleSince := *stale.Observations[0].StaleSince

	now = now.Add(time.Minute)
	restarted := testService(stateDir, probe, &now)
	escalated, err := restarted.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := escalated.Observations[0]
	if escalated.Event == nil || got.Escalation != 1 {
		t.Fatalf("restart scan = %+v, want the persisted first escalation", escalated)
	}
	if !got.LastSeen.Equal(lastSeen) || !got.LastProgress.Equal(lastProgress) || got.StaleSince == nil || !got.StaleSince.Equal(staleSince) {
		t.Errorf("restart observation = %+v, want original progress and stale times retained", got)
	}
	if got.NextEscalation == nil || !got.NextEscalation.Equal(now.Add(time.Minute)) {
		t.Errorf("NextEscalation = %v, want %v", got.NextEscalation, now.Add(time.Minute))
	}
}

func TestDueHeartbeatResurfacesPublishedStaleObservation(t *testing.T) {
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
	stale, err := service.Scan(context.Background())
	if err != nil || stale.Event == nil {
		t.Fatalf("first stale = %+v, %v", stale, err)
	}
	if _, err := service.Publish(*stale.Event); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Second)
	resurfaced, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resurfaced.Event == nil || resurfaced.Event.Source != HeartbeatEvent || resurfaced.Event.Kind != "heartbeat" {
		t.Fatalf("due heartbeat = %+v, want heartbeat re-surface event", resurfaced)
	}
	if resurfaced.Heartbeat.NoChangeStreak != 0 {
		t.Errorf("NoChangeStreak = %d, want reset after actionable heartbeat", resurfaced.Heartbeat.NoChangeStreak)
	}
}

func TestPublishAppendsBeforeEpisodeAndRetainsPendingOnEpisodeFailure(t *testing.T) {
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
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil {
		t.Fatal("expected persisted stale event")
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
