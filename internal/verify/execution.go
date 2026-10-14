package verify

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
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
	// The logs are the command's output, or none when the command wrote to
	// its caller's own output (Executor.Stdout), which its caller keeps.
	StdoutLog string `json:"stdout_log,omitempty"`
	StderrLog string `json:"stderr_log,omitempty"`
	// Source is what the command ran on, read before it started and once it
	// had ended, when its executor reads it (Executor.Source).
	Source SourceReads `json:"source,omitzero"`
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
	record.StdoutLog = execution.path + ".stdout.log"
	record.StderrLog = execution.path + ".stderr.log"
	record.Measurement.State = Queued
	record.Transitions = []ExecutionTransition{{State: Queued, At: record.Created}}
	if err := execution.persist(record); err != nil {
		return nil, err
	}
	return execution, nil
}

func (execution *Execution) Path() string { return execution.path }

// keepNoLogs records that the command writes to its caller's own output, so
// the execution keeps no logs of it.
func (execution *Execution) keepNoLogs() error {
	next := execution.record
	next.StdoutLog, next.StderrLog = "", ""
	return execution.persist(next)
}

// LoadExecution reads the execution record at path.
func LoadExecution(path string) (ExecutionRecord, error) {
	var record ExecutionRecord
	data, err := fsx.ReadFile(path)
	if err != nil {
		return record, err
	}
	return record, json.Unmarshal(data, &record)
}
func (execution *Execution) ID() string { return execution.record.ID }

func (execution *Execution) Admit(waited time.Duration) error {
	if execution.record.Measurement.State != Queued || waited < 0 {
		return errors.New("verify: only a queued execution can be admitted")
	}
	next := execution.record
	next.QueueSeconds = waited.Seconds()
	next.Measurement.State = Admitted
	return execution.persist(next)
}

// readSource records a read of the source the command runs on: before it
// starts, or once it has ended.
func (execution *Execution) readSource(isEnd bool, source Source, failure error) error {
	next := execution.record
	next.Source.Read(isEnd, source, failure)
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
	if exitCode >= 0 {
		next.ExitCode = &exitCode
	}
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
func (execution *Execution) Refuse(failure error, waited time.Duration) error {
	state := execution.record.Measurement.State
	if failure == nil || waited < 0 || (state != Queued && state != Admitted) {
		return errors.New("verify: refusal requires a failure before execution")
	}
	next := execution.record
	next.QueueSeconds = waited.Seconds()
	next.Measurement.State = Interrupted
	next.Measurement.CleanupConfirmed = true
	next.Status = "not_started"
	next.Failure = failure.Error()
	return execution.persist(next)
}

func (execution *Execution) interrupt() error {
	if execution.record.Measurement.State != Completed {
		return nil
	}
	next := execution.record
	next.Measurement.State = Interrupted
	return execution.persist(next)
}

func (execution *Execution) recoveredCleanup(ctx context.Context, usage execx.TreeUsage, spared []Leftover, exitCode int) error {
	if execution.record.Status == "passed" || len(spared) == 0 && usage.ActiveProcesses != 0 {
		return errors.New("verify: recovery requires a failed run and a job holding nothing but the services it spared")
	}
	next := execution.record
	next.Status = "failed"
	if exitCode >= 0 {
		next.ExitCode = &exitCode
	}
	if next.Failure == "" {
		next.Failure = "execution failed before cleanup and evidence were confirmed"
	}
	next.Measurement.State = Interrupted
	next.Measurement.CleanupConfirmed = true
	next.Measurement.Usage = usage
	next.Measurement.Spared = spared
	return execution.persistContext(ctx, next)
}

// markRecovered records in a pending execution's record that its controller
// ended before confirming its workers gone, and how recovery confirmed it
// since. The run fails: one its own controller never finished never passes.
// A record its controller finished keeps its result, and one marked already
// is not marked again. A record that is not there claims nothing.
func markRecovered(ctx context.Context, path, runID string, usage execx.TreeUsage, isFound bool, note string) error {
	if filepath.Base(path) != runID+".json" || filepath.Base(filepath.Dir(path)) != "executions" {
		return errors.New("verify: recovery evidence names another run")
	}
	data, err := fsx.ReadFileContext(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var record ExecutionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.ID != runID {
		return errors.New("verify: recovery evidence names another run")
	}
	if record.Status != "pending" {
		return nil
	}
	recovered := &Execution{path: path, record: record}
	next := record
	next.Status = "failed"
	next.Failure = strings.TrimSpace(next.Failure + "\n" + note)
	next.Measurement.State = Interrupted
	next.Measurement.CleanupConfirmed = true
	if isFound {
		next.Measurement.Usage = usage
	}
	return recovered.persistContext(ctx, next)
}

func (execution *Execution) persist(next ExecutionRecord) error {
	return execution.persistContext(context.Background(), next)
}

func (execution *Execution) persistContext(ctx context.Context, next ExecutionRecord) error {
	if len(next.Transitions) > 0 && next.Transitions[len(next.Transitions)-1].State != next.Measurement.State {
		next.Transitions = append(next.Transitions, ExecutionTransition{State: next.Measurement.State, At: time.Now().UTC()})
	}
	if err := save(ctx, execution.path, next); err != nil {
		return err
	}
	execution.record = next
	return nil
}
