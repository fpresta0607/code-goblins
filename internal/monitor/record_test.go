package monitor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCPUProgressJudgmentRoundtripAndLegacyDefault(t *testing.T) {
	for _, hasProgress := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy default no evidence", true: "persisted positive CPU"}[hasProgress], func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
			since := now.Add(-time.Minute)
			in := Observation{TaskID: "g1", Endpoint: "fleet:pane-g1", EndpointVerdict: ProbePresent, Health: HealthIdle, Reason: None, Digest: "quiet-pane", LastObserved: now, LastSeen: now, LastProgress: now, EvidenceAt: &now, JobCPU: 5 * time.Second, JobSampledAt: &now, JobSampledSince: &since, HasJobProgress: hasProgress}
			if err := WriteObservation(stateDir, in); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(ObservationPath(stateDir, in.TaskID))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(data, []byte(`"has_job_progress"`)) != hasProgress {
				t.Fatalf("unexpected persisted judgment: %s", data)
			}
			got, err := ReadObservation(stateDir, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if got.HasJobProgress != hasProgress || got.JobSampledAt == nil || !got.JobSampledAt.Equal(now) || got.JobSampledSince == nil || !got.JobSampledSince.Equal(since) {
				t.Errorf("persisted CPU judgment lost its sample binding: %+v", got)
			}
		})
	}
}

func TestMonitorRecordPathsAndStrictJSON(t *testing.T) {
	stateDir := t.TempDir()
	if got, want := ObservationPath(stateDir, "g1"), filepath.Join(stateDir, "monitor", "tasks", "g1.json"); got != want {
		t.Errorf("ObservationPath = %q, want %q", got, want)
	}
	if got, want := HeartbeatPath(stateDir), filepath.Join(stateDir, "monitor", "heartbeat.json"); got != want {
		t.Errorf("HeartbeatPath = %q, want %q", got, want)
	}

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	in := Observation{
		TaskID:          "g1",
		Endpoint:        "fleet:pane-g1",
		EndpointVerdict: ProbePresent,
		Digest:          "digest",
		Health:          HealthActive,
		Reason:          None,
		LastObserved:    now,
		LastSeen:        now,
		LastProgress:    now,
	}
	if err := WriteObservation(stateDir, in); err != nil {
		t.Fatalf("WriteObservation: %v", err)
	}
	got, err := ReadObservation(stateDir, "g1")
	if err != nil {
		t.Fatalf("ReadObservation: %v", err)
	}
	if got.Schema != Schema || got.TaskID != "g1" || got.Health != HealthActive {
		t.Errorf("observation = %+v, want schema-backed round trip", got)
	}
	entries, err := os.ReadDir(filepath.Dir(ObservationPath(stateDir, "g1")))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "g1.json" {
		t.Errorf("task records = %+v, want only atomically-written g1.json", entries)
	}

	if err := os.WriteFile(ObservationPath(stateDir, "bad"), []byte(`{"schema":"cfo-monitor.v1","task_id":"bad","unknown":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadObservation(stateDir, "bad"); err == nil {
		t.Error("ReadObservation accepted an unknown JSON field")
	}
	if err := os.WriteFile(HeartbeatPath(stateDir), []byte(`{"schema":"cfo-monitor.v0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHeartbeat(stateDir); err == nil {
		t.Error("ReadHeartbeat accepted an unsupported schema")
	}
	if _, err := ReadObservation(stateDir, "missing"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing observation error = %v, want ErrNotExist", err)
	}
}

func TestMonitorRecordsRejectIncompleteAndInvalidSemantics(t *testing.T) {
	stateDir := t.TempDir()
	observationPath := ObservationPath(stateDir, "g1")
	if err := os.MkdirAll(filepath.Dir(observationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(observationPath, []byte(`{"schema":"cfo-monitor.v1","task_id":"g1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadObservation(stateDir, "g1"); err == nil {
		t.Error("ReadObservation accepted an incomplete record")
	}

	if err := os.MkdirAll(filepath.Dir(HeartbeatPath(stateDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(HeartbeatPath(stateDir), []byte(`{"schema":"cfo-monitor.v1","last_cycle":"not-a-time"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHeartbeat(stateDir); err == nil {
		t.Error("ReadHeartbeat accepted an invalid required timestamp")
	}
}

func TestWriteHeartbeatRoundTripsPendingEvent(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	in := Heartbeat{
		LastCycle:     now,
		LastHeartbeat: now,
		NextDue:       now.Add(time.Minute),
		PendingEvent:  &Event{Source: HeartbeatEvent, Kind: "heartbeat", Key: "heartbeat", Detail: "review fleet"},
	}
	if err := WriteHeartbeat(stateDir, in); err != nil {
		t.Fatalf("WriteHeartbeat: %v", err)
	}
	got, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != Schema || got.PendingEvent == nil || *got.PendingEvent != *in.PendingEvent {
		t.Errorf("heartbeat = %+v, want pending-event round trip", got)
	}
}

func TestTouchHeartbeatUpdatesLastCycleWithoutChangingPendingEvent(t *testing.T) {
	stateDir := t.TempDir()
	first := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	pending := Event{Source: HeartbeatEvent, Kind: "heartbeat", Key: "heartbeat", Detail: "review fleet"}
	if err := WriteHeartbeat(stateDir, Heartbeat{LastCycle: first, PendingEvent: &pending}); err != nil {
		t.Fatal(err)
	}
	second := first.Add(time.Minute)
	if err := TouchHeartbeat(stateDir, second); err != nil {
		t.Fatalf("TouchHeartbeat: %v", err)
	}
	got, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastCycle.Equal(second) || got.PendingEvent == nil || *got.PendingEvent != pending {
		t.Errorf("heartbeat = %+v, want new LastCycle and preserved pending event", got)
	}
}
