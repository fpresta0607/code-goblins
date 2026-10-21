package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/lock"
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

// A cleanup closes a goblin's terminal before it retires the task's record,
// so a scan that finds the terminal gone while the task's cleanup holds it
// keeps and raises nothing of it (issue #1225). Another command takes the same
// lock a moment to write into the task's folder, as an auth refresh does, and
// a cleanup that ended without retiring the task leaves its lock record
// behind: neither accounts for the terminal, so the goblin it ran is missing,
// and the CFO is woken.
func TestScanRaisesAMissingTerminalUnlessTheTasksCleanupHoldsIt(t *testing.T) {
	holdCleanupLock := func(purpose string) func(*testing.T, string) {
		return func(t *testing.T, directory string) {
			if _, err := lock.AcquireExclusiveNamedFor(directory, state.CleanupLockName("task"), purpose); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lock.ReleaseExclusiveNamed(directory, state.CleanupLockName("task")) })
		}
	}
	for _, test := range []struct {
		name   string
		hold   func(*testing.T, string)
		isWake bool
	}{
		{name: "nothing holds the task", hold: func(*testing.T, string) {}, isWake: true},
		{name: "another command writes into the task's folder", hold: holdCleanupLock(""), isWake: true},
		{name: "a cleanup that ended left its lock record", hold: func(t *testing.T, directory string) {
			ended := lock.Info{PID: os.Getpid(), OwnerPID: os.Getpid(), Start: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Purpose: state.CleanupPurpose("task")}
			data, err := json.Marshal(ended)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, state.CleanupLockName("task")), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}, isWake: true},
		{name: "its cleanup holds the task", hold: holdCleanupLock(state.CleanupPurpose("task")), isWake: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			directory := t.TempDir()
			meta := metaFor("task")
			if err := state.WriteTaskMeta(directory, meta); err != nil {
				t.Fatal(err)
			}
			test.hold(t, directory)
			probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: {Verdict: ProbeMissing, Detail: "pane missing"}}}

			// Act
			result, err := (Service{StateDir: directory, Probe: probe}).Scan(context.Background())

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(probe.calls) != 1 {
				t.Fatalf("probe calls = %v, want the task's terminal read once", probe.calls)
			}
			if isWake := result.Event != nil && result.Event.TaskID == meta.ID && result.Event.Detail == string(EndpointMissing)+": pane missing"; isWake != test.isWake {
				t.Errorf("event = %+v, want an endpoint_missing wake %v", result.Event, test.isWake)
			}
			if _, err := ReadObservation(directory, meta.ID); !test.isWake && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the reading of a task its cleanup holds was kept: %v", err)
			}
		})
	}
}
