package verify

import (
	"crypto/rand"
	"errors"
	"path/filepath"
	"time"
)

// ExecutionRecord is local execution evidence. Same-user files remain
// advisory and cannot authorize publishing a required GitHub check.
type ExecutionRecord struct {
	Version      int                   `json:"version"`
	ID           string                `json:"id"`
	Root         string                `json:"root"`
	Command      []string              `json:"command"`
	Created      time.Time             `json:"created"`
	QueueSeconds float64               `json:"queue_seconds"`
	Measurement  Measurement           `json:"measurement"`
	Transitions  []ExecutionTransition `json:"transitions"`
	Status       string                `json:"status"`
	ExitCode     *int                  `json:"exit_code"`
	Failure      string                `json:"failure,omitempty"`
}

type ExecutionTransition struct {
	State ExecutionState `json:"state"`
	At    time.Time      `json:"at"`
}

type Execution struct {
	path   string
	record ExecutionRecord
}

func BeginExecution(root string, command []string) (*Execution, error) {
	if len(command) == 0 || command[0] == "" || root == "" {
		return nil, errors.New("verify: execution requires a root and a command")
	}
	store, err := StoreDir()
	if err != nil {
		return nil, err
	}
	id := rand.Text()
	execution := &Execution{path: filepath.Join(store, "executions", id+".json")}
	record := ExecutionRecord{Version: 1, ID: id, Root: root, Command: append([]string(nil), command...), Created: time.Now().UTC(), Status: "pending"}
	record.Measurement.State = Queued
	record.Transitions = []ExecutionTransition{{State: Queued, At: record.Created}}
	if err := execution.persist(record); err != nil {
		return nil, err
	}
	return execution, nil
}

func (execution *Execution) Path() string { return execution.path }
func (execution *Execution) ID() string   { return execution.record.ID }

func (execution *Execution) Admit(waited time.Duration) error {
	if execution.record.Measurement.State != Queued || waited < 0 {
		return errors.New("verify: only a queued execution can be admitted")
	}
	next := execution.record
	next.QueueSeconds = waited.Seconds()
	next.Measurement.State = Admitted
	return execution.persist(next)
}

func (execution *Execution) Start() error {
	if execution.record.Measurement.State != Admitted {
		return errors.New("verify: only an admitted execution can start")
	}
	next := execution.record
	next.Measurement.State = Running
	return execution.persist(next)
}

func (execution *Execution) Observe(measurement Measurement) error {
	if execution.record.Measurement.State != Running {
		return errors.New("verify: only a running execution accepts measurements")
	}
	switch measurement.State {
	case Running, Completed, Interrupted, CleanupUncertain:
	default:
		return errors.New("verify: invalid measured execution state")
	}
	next := execution.record
	next.Measurement = measurement
	return execution.persist(next)
}

func (execution *Execution) Finish(exitCode int, failure error) error {
	if execution.record.Status != "pending" {
		return errors.New("verify: execution already finished")
	}
	next := execution.record
	next.ExitCode = &exitCode
	next.Status = "failed"
	if failure != nil {
		next.Failure = failure.Error()
	}
	measurement := next.Measurement
	if failure == nil && exitCode == 0 && measurement.State == Completed && measurement.CleanupConfirmed && measurement.Samples > 0 {
		next.Status = "passed"
	}
	return execution.persist(next)
}

// Refuse records an execution that never started a process, including an
// admission failure. No command exit code is invented for it.
func (execution *Execution) Refuse(failure error) error {
	state := execution.record.Measurement.State
	if failure == nil || (state != Queued && state != Admitted) {
		return errors.New("verify: refusal requires a failure before execution")
	}
	next := execution.record
	next.Measurement.State = Interrupted
	next.Measurement.CleanupConfirmed = true
	next.Status = "not_started"
	next.Failure = failure.Error()
	return execution.persist(next)
}

func (execution *Execution) persist(next ExecutionRecord) error {
	if len(next.Transitions) > 0 && next.Transitions[len(next.Transitions)-1].State != next.Measurement.State {
		next.Transitions = append(next.Transitions, ExecutionTransition{State: next.Measurement.State, At: time.Now().UTC()})
	}
	if err := Save(execution.path, next); err != nil {
		return err
	}
	execution.record = next
	return nil
}
