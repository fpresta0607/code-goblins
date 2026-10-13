package verify

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func TestExecutionPersistsLifecycleAndKeepsQueueTimeSeparate(t *testing.T) {
	// Arrange
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	command := []string{"check", "original"}
	execution, err := BeginExecution(t.TempDir(), command)
	if err != nil {
		t.Fatal(err)
	}
	command[1] = "changed"

	// Act
	if err := execution.Admit(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if err := execution.Start(); err != nil {
		t.Fatal(err)
	}
	if err := execution.Observe(Measurement{State: Running, Samples: 1}); err != nil {
		t.Fatal(err)
	}
	if err := execution.Observe(Measurement{State: Completed, CleanupConfirmed: true, Samples: 2, WallSeconds: 3}); err != nil {
		t.Fatal(err)
	}
	if err := execution.Finish(0, nil); err != nil {
		t.Fatal(err)
	}
	data, err := fsx.ReadFile(execution.Path())
	if err != nil {
		t.Fatal(err)
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}

	// Assert
	if record.ID == "" || record.Command[1] != "original" || record.QueueSeconds != 2 || record.Measurement.WallSeconds != 3 || record.Status != "passed" || record.ExitCode == nil || *record.ExitCode != 0 {
		t.Fatalf("record = %+v", record)
	}
	wanted := []ExecutionState{Queued, Admitted, Running, Completed}
	if len(record.Transitions) != len(wanted) {
		t.Fatalf("transitions = %+v", record.Transitions)
	}
	for index, transition := range record.Transitions {
		if transition.State != wanted[index] || transition.At.IsZero() {
			t.Fatalf("transition = %+v", transition)
		}
	}
}

func TestExecutionNeverPassesWithoutCompleteExecutionEvidence(t *testing.T) {
	for _, test := range []struct {
		name        string
		measurement Measurement
		exit        int
		failure     error
	}{
		{name: "unfinished", measurement: Measurement{State: Running}},
		{name: "cleanup unproven", measurement: Measurement{State: Completed}},
		{name: "interrupted", measurement: Measurement{State: Interrupted, CleanupConfirmed: true}},
		{name: "command failed", measurement: Measurement{State: Completed, CleanupConfirmed: true}, exit: 1},
		{name: "infrastructure failed", measurement: Measurement{State: Completed, CleanupConfirmed: true}, failure: errors.New("pipe read failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CFO_VERIFY_DIR", t.TempDir())
			execution, err := BeginExecution(t.TempDir(), []string{"check"})
			if err != nil {
				t.Fatal(err)
			}
			if err := execution.Admit(0); err != nil {
				t.Fatal(err)
			}
			if err := execution.Start(); err != nil {
				t.Fatal(err)
			}
			if err := execution.Observe(test.measurement); err != nil {
				t.Fatal(err)
			}
			if err := execution.Finish(test.exit, test.failure); err != nil {
				t.Fatal(err)
			}
			if execution.record.Status == "passed" {
				t.Fatalf("incomplete execution passed: %+v", execution.record)
			}
		})
	}
}

func TestExecutionRefusesInvalidTransitionsAndFailedWrites(t *testing.T) {
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	execution, err := BeginExecution(t.TempDir(), []string{"check"})
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.Start(); err == nil {
		t.Fatal("started without admission")
	}
	if err := os.Remove(execution.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(execution.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(execution.Path(), "occupied"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := execution.Admit(0); err == nil {
		t.Fatal("failed durable admission write was ignored")
	}
	if execution.record.Measurement.State != Queued {
		t.Fatal("failed write advanced the in-memory record")
	}
}
