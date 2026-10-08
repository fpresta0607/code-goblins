package monitor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

func TestNativeProgressCorrelatesOwnedRolloutWithoutSessionOrJobs(t *testing.T) {
	for _, test := range []struct {
		name          string
		status        string
		hasOwnRollout bool
		hasFreshEntry bool
		shouldWake    bool
	}{
		{"moving owned rollout", herdr.AgentWorking, true, true, false},
		{"fresh foreign rollout", herdr.AgentWorking, false, true, true},
		{"silent owned rollout", herdr.AgentWorking, true, false, true},
		{"ready after work", herdr.AgentIdle, true, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the 4647 clocks and absent native session are fixtures;
			// no live host, process, transcript or provider is consulted.
			home, stateDirectory := t.TempDir(), t.TempDir()
			metadata := nativeMeta("g1", "codex")
			metadata.Worktree = t.TempDir()
			writeTask(t, stateDirectory, metadata)
			now := time.Date(2026, 10, 3, 9, 32, 4, 0, time.UTC)
			oldEvidence := time.Date(2026, 10, 3, 5, 37, 11, 0, time.UTC)
			busySince := time.Date(2026, 10, 3, 7, 12, 11, 0, time.UTC)
			write := now.Add(-time.Second)
			if !test.hasFreshEntry {
				write = oldEvidence
			}
			directory := metadata.Worktree
			if !test.hasOwnRollout {
				directory = t.TempDir()
			}
			rollout := filepath.Join(home, ".codex", "sessions", "2026", "10", "03", "rollout-2026-10-03T05-37-11-saved-thread.jsonl")
			if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
				t.Fatal(err)
			}
			metadataEntry, err := json.Marshal(struct {
				Type    string            `json:"type"`
				Payload map[string]string `json:"payload"`
			}{"session_meta", map[string]string{"id": "saved-thread", "cwd": directory}})
			if err != nil {
				t.Fatal(err)
			}
			content := append(metadataEntry, []byte("\n{\"timestamp\":\""+write.Format(time.RFC3339Nano)+"\",\"type\":\"event_msg\"}\n")...)
			if err := os.WriteFile(rollout, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(rollout, oldEvidence, oldEvidence); err != nil {
				t.Fatal(err)
			}
			record := recordNativeHost(t, stateDirectory, metadata.ID)
			record.ChildPID = 0
			recordBytes, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDirectory, "hosts", metadata.ID+".json"), recordBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			prior := Observation{
				Schema: Schema, TaskID: metadata.ID, Endpoint: endpointString(metadata), EndpointVerdict: ProbePresent,
				Health: HealthBusy, Reason: None, Digest: "prior work", LastObserved: now.Add(-time.Minute), LastSeen: now.Add(-time.Minute),
				LastProgress: time.Date(2026, 10, 3, 9, 11, 11, 0, time.UTC), EvidenceAt: &oldEvidence, BusySince: &busySince,
			}
			if err := WriteObservation(stateDirectory, prior); err != nil {
				t.Fatal(err)
			}
			sample := sampleForStatus(metadata, test.status, "\u2022 Working (2h \u2022 esc to interrupt)\n\u203a ")
			if test.status == herdr.AgentIdle {
				sample.Capture = capture("\u203a \n100% context left")
			}
			sample.Harness, sample.Session, sample.CountersUnavailable = "codex", "", true
			service := testService(stateDirectory, &fakeProber{samples: map[string]EndpointSample{metadata.ID: sample}}, &now)
			service.Gate = &fakeGate{sample: GateSample{Active: false}}
			service.Progress = &HostProgress{StateDir: stateDirectory, Home: home}

			// Act
			result, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			// Assert
			if len(result.Observations) != 1 {
				t.Fatalf("observations=%d, want the one native task", len(result.Observations))
			}
			observation := result.Observations[0]
			if test.shouldWake {
				if result.Event == nil || observation.Reason != BusyTurnOverAge {
					t.Fatalf("no genuine silence wake: %+v, %+v", result.Event, observation)
				}
				if observation.EvidenceAt == nil || !observation.EvidenceAt.Equal(oldEvidence) {
					t.Fatal("foreign or silent rollout became fresh progress")
				}
				return
			}
			if result.Event != nil {
				t.Fatalf("current native work woke as stale: %+v", result.Event)
			}
			if test.status == herdr.AgentWorking && (observation.Health != HealthBusy || observation.EvidenceAt == nil || !observation.EvidenceAt.Equal(write)) {
				t.Fatalf("fresh owned rollout was not attributed: %+v", observation)
			}
			if test.status == herdr.AgentIdle && (observation.Health != HealthIdle || observation.BusySince != nil) {
				t.Fatalf("ready composer retained the old busy turn: %+v", observation)
			}
		})
	}
}
