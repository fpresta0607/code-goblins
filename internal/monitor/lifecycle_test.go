package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestPausedTaskStaysQuietAcrossMonitorRestarts(t *testing.T) {
	directory := t.TempDir()
	meta := metaFor("task")
	meta.SpawnGen = "current"
	if err := state.WriteTaskMeta(directory, meta); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(directory, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-1", Action: "pause", Phase: "paused"}); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProber{}
	stamp := time.Now()
	if err := WriteHeartbeat(directory, Heartbeat{LastCycle: stamp, LastHeartbeat: stamp, NextDue: stamp.Add(time.Hour), PendingEvent: &Event{Source: TaskEvent, TaskID: meta.ID, Kind: "stale", Key: meta.ID, Detail: "old task alarm"}}); err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{time.Now(), time.Now().Add(48 * time.Hour)} {
		service := Service{StateDir: directory, Probe: probe, Now: func() time.Time { return now }}
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Observations) != 1 || result.Observations[0].Health != HealthPaused || result.Observations[0].PendingEvent != nil {
			t.Fatalf("paused task raised an alarm: %+v", result)
		}
		heartbeat, err := ReadHeartbeat(directory)
		if err != nil || heartbeat.PendingEvent != nil || result.Event != nil {
			t.Fatalf("pause retained a stale alarm: %+v %+v %v", heartbeat, result.Event, err)
		}
	}
	if len(probe.calls) != 0 {
		t.Fatalf("paused terminal was still probed: %v", probe.calls)
	}
}

func TestFailedOrAbandonedLifecycleStillMonitorsALiveHarness(t *testing.T) {
	for _, phase := range []string{"failed", "resuming", "pausing", "stopping"} {
		t.Run(phase, func(t *testing.T) {
			directory := t.TempDir()
			meta := metaFor("task")
			meta.SpawnGen = "current"
			if err := state.WriteTaskMeta(directory, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(directory, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume", Phase: phase}); err != nil {
				t.Fatal(err)
			}
			probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: {Verdict: ProbePresent, Agent: herdr.AgentAlive, Busy: herdr.BusyWorking, Status: "working", Capture: capture("Working on the retained task")}}}
			_, err := (Service{StateDir: directory, Probe: probe}).Scan(context.Background())
			if err != nil || len(probe.calls) != 1 {
				t.Fatalf("live harness was not inspected: %v %v", probe.calls, err)
			}
		})
	}
}
