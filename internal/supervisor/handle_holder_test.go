package supervisor

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The processes the handle readings below name: Microsoft Defender at what
// it holds on a quiet machine, and the two recursive searches of the whole
// drive that outlived their shells, on 2026-11-05 and on 2026-11-11.
var (
	quietHolder = HandleHolder{PID: 5516, Name: "MsMpEng.exe", Started: time.Date(2026, 11, 11, 0, 5, 0, 0, time.UTC), Handles: 9250}
	grepHolder  = HandleHolder{PID: 4242, Name: "grep.exe", Started: time.Date(2026, 11, 11, 1, 10, 0, 0, time.UTC), Handles: 6680532, Command: "grep -rn -A8 pattern / internal/proc/job_windows.go"}
	findHolder  = HandleHolder{PID: 3131, Name: "find.exe", Started: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC), Handles: 1000000, Command: "find / -type d -path */node_modules"}
)

// handleWakes takes one reading per holder, a minute apart, and returns the
// handle wakes raised meanwhile.
func handleWakes(t *testing.T, readings ...HandleHolder) []wake.Record {
	t.Helper()
	s, h := fleetService(t)
	s.Options.Dispatch = &Dispatch{
		Memory: func() (Memory, error) {
			return Memory{Available: 20 * gigabyte, CommitAvailable: 20 * gigabyte, Total: 32 * gigabyte}, nil
		},
		TopHandleHolder: func() (HandleHolder, error) {
			next := readings[0]
			readings = readings[1:]
			return next, nil
		},
	}
	now := time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)
	for range len(readings) {
		now = now.Add(time.Minute)
		if err := s.checkFleet(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	var raised []wake.Record
	for _, record := range fleetWakeRecords(t, h, "memory") {
		if record.Key == "handles" {
			raised = append(raised, record)
		}
	}
	return raised
}

// A process past the handle line wakes the CEO once, however many readings
// find it there, and another past it wakes the CEO for itself.
func TestAProcessOverTheHandleLineRaisesOneWake(t *testing.T) {
	cases := []struct {
		name     string
		readings []HandleHolder
		want     int
	}{
		{"a process under the line raises none", []HandleHolder{quietHolder, quietHolder}, 0},
		{"a process over the line raises one however long it stays", []HandleHolder{grepHolder, grepHolder, grepHolder}, 1},
		{"another process over the line raises its own", []HandleHolder{grepHolder, findHolder}, 2},
		{"a process over the line again after a reading under it raises another", []HandleHolder{grepHolder, quietHolder, grepHolder}, 2},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange and Act
			raised := handleWakes(t, test.readings...)

			// Assert
			if len(raised) != test.want {
				t.Fatalf("the readings raised %d handle wakes, want %d: %+v", len(raised), test.want, raised)
			}
		})
	}
}

// The wake names the process by its pid and its command, so the CEO can
// tell what it is and end it by its pid.
func TestTheHandleWakeNamesTheProcessByItsPidAndCommand(t *testing.T) {
	// Arrange and Act
	raised := handleWakes(t, grepHolder)

	// Assert
	if len(raised) != 1 {
		t.Fatalf("a process over the handle line raised %d wakes, want one", len(raised))
	}
	for _, want := range []string{"handles:", "grep.exe", "pid 4242", "6680532 handles", grepHolder.Command, "goblins reap --force 4242 --apply"} {
		if !strings.Contains(raised[0].Detail, want) {
			t.Errorf("handle wake %q does not name %q", raised[0].Detail, want)
		}
	}
}

// The memory reading on the board names the process holding the most handles.
func TestSnapshotNamesTheTopHandleHolder(t *testing.T) {
	// Arrange
	handler, _ := startBoardWith(t, Memory{Available: 20 * gigabyte, Total: 32 * gigabyte, CommitAvailable: 40 * gigabyte, CommitLimit: 48 * gigabyte}, &spawnRecorder{})
	handler.Service.Options.Dispatch.TopHandleHolder = func() (HandleHolder, error) { return grepHolder, nil }

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Memory == nil || snapshot.Memory.HandleHolder == nil || !reflect.DeepEqual(*snapshot.Memory.HandleHolder, grepHolder) {
		t.Fatalf("memory = %+v, want it to name %+v", snapshot.Memory, grepHolder)
	}
}
