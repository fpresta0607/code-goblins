// Package monitor persists inspection-only Herdr health observations and
// converts them into durable wake events without taking lifecycle actions.
package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const Schema = "cfo-monitor.v1"

type ProbeVerdict string

const (
	ProbePresent ProbeVerdict = "present"
	ProbeMissing ProbeVerdict = "missing"
	ProbeUnknown ProbeVerdict = "unknown"
)

// EndpointSample is the one read-only response consumed by the monitor. Busy
// is separate from Agent because liveness and activity are different facts.
type EndpointSample struct {
	Verdict  ProbeVerdict
	Endpoint herdr.Endpoint
	TabLabel string
	Agent    herdr.AgentStatus
	Busy     herdr.BusyState
	Capture  []byte
	Detail   string
}

type Health string

const (
	HealthActive  Health = "active"
	HealthBusy    Health = "busy"
	HealthIdle    Health = "idle"
	HealthPaused  Health = "paused"
	HealthStale   Health = "stale"
	HealthUnknown Health = "unknown"
)

type Reason string

const (
	None            Reason = "none"
	UnchangedIdle   Reason = "unchanged_idle"
	BusyTurnOverAge Reason = "busy_turn_over_age"
	DeclaredPause   Reason = "declared_pause"
	EndpointMissing Reason = "endpoint_missing"
	EndpointUnknown Reason = "endpoint_unknown"
	InvalidRecord   Reason = "invalid_record"
)

type EventSource string

const (
	TaskEvent      EventSource = "task"
	HeartbeatEvent EventSource = "heartbeat"
)

type Event struct {
	Source EventSource `json:"source"`
	TaskID string      `json:"task_id,omitempty"`
	Kind   string      `json:"kind"`
	Key    string      `json:"key"`
	Detail string      `json:"detail"`
}

type Observation struct {
	Schema               string       `json:"schema"`
	TaskID               string       `json:"task_id"`
	Endpoint             string       `json:"endpoint"`
	EndpointVerdict      ProbeVerdict `json:"endpoint_verdict"`
	Digest               string       `json:"digest"`
	LastObserved         time.Time    `json:"last_observed"`
	LastSeen             time.Time    `json:"last_seen"`
	LastProgress         time.Time    `json:"last_progress"`
	StaleSince           *time.Time   `json:"stale_since,omitempty"`
	NextEscalation       *time.Time   `json:"next_escalation,omitempty"`
	NextPauseResurface   *time.Time   `json:"next_pause_resurface,omitempty"`
	Health               Health       `json:"health"`
	Reason               Reason       `json:"reason"`
	Escalation           int          `json:"escalation"`
	DemandDeepInspection bool         `json:"demand_deep_inspection"`
	PendingEvent         *Event       `json:"pending_event,omitempty"`
}

type Heartbeat struct {
	Schema         string    `json:"schema"`
	LastCycle      time.Time `json:"last_cycle"`
	LastHeartbeat  time.Time `json:"last_heartbeat"`
	NoChangeStreak int       `json:"no_change_streak"`
	NextDue        time.Time `json:"next_due"`
	PendingEvent   *Event    `json:"pending_event,omitempty"`
}

// ObservationPath returns the only supported persisted task-monitor path.
func ObservationPath(stateDir, id string) string {
	return filepath.Join(stateDir, "monitor", "tasks", id+".json")
}

// HeartbeatPath returns the only supported persisted fleet-heartbeat path.
func HeartbeatPath(stateDir string) string {
	return filepath.Join(stateDir, "monitor", "heartbeat.json")
}

func ReadObservation(stateDir, id string) (Observation, error) {
	if err := state.ValidTaskID(id); err != nil {
		return Observation{}, err
	}
	var observation Observation
	if err := readStrictJSON(ObservationPath(stateDir, id), &observation); err != nil {
		return Observation{}, err
	}
	if observation.Schema != Schema {
		return Observation{}, fmt.Errorf("monitor: unsupported observation schema %q", observation.Schema)
	}
	if observation.TaskID != id {
		return Observation{}, fmt.Errorf("monitor: observation task ID %q does not match path ID %q", observation.TaskID, id)
	}
	if err := validateObservation(observation); err != nil {
		return Observation{}, err
	}
	return observation, nil
}

func WriteObservation(stateDir string, observation Observation) error {
	if err := state.ValidTaskID(observation.TaskID); err != nil {
		return err
	}
	observation.Schema = Schema
	if err := validateObservation(observation); err != nil {
		return err
	}
	return writeJSON(ObservationPath(stateDir, observation.TaskID), observation)
}

func ReadHeartbeat(stateDir string) (Heartbeat, error) {
	var heartbeat Heartbeat
	if err := readStrictJSON(HeartbeatPath(stateDir), &heartbeat); err != nil {
		return Heartbeat{}, err
	}
	if heartbeat.Schema != Schema {
		return Heartbeat{}, fmt.Errorf("monitor: unsupported heartbeat schema %q", heartbeat.Schema)
	}
	if err := validateHeartbeat(heartbeat); err != nil {
		return Heartbeat{}, err
	}
	return heartbeat, nil
}

func WriteHeartbeat(stateDir string, heartbeat Heartbeat) error {
	heartbeat.Schema = Schema
	if err := validateHeartbeat(heartbeat); err != nil {
		return err
	}
	return writeJSON(HeartbeatPath(stateDir), heartbeat)
}

// TouchHeartbeat advances watcher liveness without classifying any endpoint.
// It exists for the signals-only watch path before a structural Herdr prober
// is wired, preserving the typed heartbeat as the one watcher-health record.
func TouchHeartbeat(stateDir string, now time.Time) error {
	heartbeat, err := ReadHeartbeat(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) {
		heartbeat = Heartbeat{}
	}
	heartbeat.LastCycle = now.UTC()
	return WriteHeartbeat(stateDir, heartbeat)
}

func readStrictJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("monitor: decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("monitor: decode %s: multiple JSON values", path)
		}
		return fmt.Errorf("monitor: decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

func validateObservation(observation Observation) error {
	if observation.Schema != Schema {
		return fmt.Errorf("monitor: unsupported observation schema %q", observation.Schema)
	}
	if err := state.ValidTaskID(observation.TaskID); err != nil {
		return err
	}
	if observation.Endpoint == "" {
		return errors.New("monitor: observation endpoint is required")
	}
	if !validProbeVerdict(observation.EndpointVerdict) {
		return fmt.Errorf("monitor: invalid endpoint verdict %q", observation.EndpointVerdict)
	}
	if observation.LastObserved.IsZero() {
		return errors.New("monitor: observation last_observed is required")
	}
	if observation.Escalation < 0 {
		return errors.New("monitor: observation escalation must not be negative")
	}
	if err := validateObservationState(observation); err != nil {
		return err
	}
	return validateEvent(observation.PendingEvent, observation.TaskID, TaskEvent)
}

func validateObservationState(observation Observation) error {
	requireProgress := func() error {
		if observation.Digest == "" || observation.LastSeen.IsZero() || observation.LastProgress.IsZero() {
			return errors.New("monitor: observation progress fields are required")
		}
		return nil
	}
	switch observation.Health {
	case HealthActive, HealthIdle, HealthBusy:
		if observation.EndpointVerdict != ProbePresent || observation.Reason != None {
			return errors.New("monitor: active, idle, and busy observations require a present endpoint and reason none")
		}
		if observation.StaleSince != nil || observation.NextEscalation != nil || observation.NextPauseResurface != nil || observation.Escalation != 0 || observation.DemandDeepInspection {
			return errors.New("monitor: active, idle, and busy observations must not retain stale state")
		}
		return requireProgress()
	case HealthStale:
		if observation.EndpointVerdict != ProbePresent || (observation.Reason != UnchangedIdle && observation.Reason != BusyTurnOverAge) {
			return errors.New("monitor: stale observation has incompatible endpoint or reason")
		}
		if observation.StaleSince == nil || observation.NextEscalation == nil || observation.NextPauseResurface != nil {
			return errors.New("monitor: stale observation is missing escalation timestamps")
		}
		return requireProgress()
	case HealthPaused:
		if observation.EndpointVerdict != ProbePresent || observation.Reason != DeclaredPause {
			return errors.New("monitor: paused observation has incompatible endpoint or reason")
		}
		if observation.StaleSince != nil || observation.NextEscalation != nil || observation.NextPauseResurface == nil || observation.Escalation != 0 || observation.DemandDeepInspection {
			return errors.New("monitor: paused observation has incompatible timing state")
		}
		return requireProgress()
	case HealthUnknown:
		if observation.StaleSince != nil || observation.NextEscalation != nil || observation.NextPauseResurface != nil || observation.Escalation != 0 || observation.DemandDeepInspection {
			return errors.New("monitor: unknown observation must not retain stale state")
		}
		switch observation.Reason {
		case EndpointMissing:
			if observation.EndpointVerdict != ProbeMissing {
				return errors.New("monitor: endpoint-missing observation requires missing verdict")
			}
		case EndpointUnknown, InvalidRecord:
			if observation.EndpointVerdict != ProbeUnknown {
				return errors.New("monitor: unknown observation requires unknown verdict")
			}
		default:
			return fmt.Errorf("monitor: invalid unknown observation reason %q", observation.Reason)
		}
		return nil
	default:
		return fmt.Errorf("monitor: invalid observation health %q", observation.Health)
	}
}

func validateHeartbeat(heartbeat Heartbeat) error {
	if heartbeat.Schema != Schema {
		return fmt.Errorf("monitor: unsupported heartbeat schema %q", heartbeat.Schema)
	}
	if heartbeat.LastCycle.IsZero() {
		return errors.New("monitor: heartbeat last_cycle is required")
	}
	if heartbeat.NoChangeStreak < 0 {
		return errors.New("monitor: heartbeat no_change_streak must not be negative")
	}
	// Signals-only watch cycles legitimately create this transitional form.
	// Scan initializes both scheduling timestamps before using the cadence.
	bootstrap := heartbeat.LastHeartbeat.IsZero() && heartbeat.NextDue.IsZero()
	if !bootstrap {
		if heartbeat.LastHeartbeat.IsZero() || heartbeat.NextDue.IsZero() {
			return errors.New("monitor: heartbeat scheduling timestamps must both be present")
		}
		if heartbeat.NextDue.Before(heartbeat.LastHeartbeat) {
			return errors.New("monitor: heartbeat next_due precedes last_heartbeat")
		}
	}
	if heartbeat.PendingEvent == nil {
		return nil
	}
	switch heartbeat.PendingEvent.Source {
	case HeartbeatEvent:
		return validateEvent(heartbeat.PendingEvent, "", HeartbeatEvent)
	case TaskEvent:
		if err := state.ValidTaskID(heartbeat.PendingEvent.TaskID); err != nil {
			return err
		}
		return validateEvent(heartbeat.PendingEvent, heartbeat.PendingEvent.TaskID, TaskEvent)
	default:
		return fmt.Errorf("monitor: pending heartbeat event source %q is invalid", heartbeat.PendingEvent.Source)
	}
}

func validProbeVerdict(verdict ProbeVerdict) bool {
	return verdict == ProbePresent || verdict == ProbeMissing || verdict == ProbeUnknown
}

func validateEvent(event *Event, taskID string, source EventSource) error {
	if event == nil {
		return nil
	}
	if event.Source != source {
		return fmt.Errorf("monitor: pending event source %q is invalid", event.Source)
	}
	if event.Detail == "" {
		return errors.New("monitor: pending event detail is required")
	}
	switch source {
	case TaskEvent:
		if event.TaskID != taskID || event.Kind != "stale" || event.Key != taskID {
			return errors.New("monitor: task pending event does not match its observation")
		}
	case HeartbeatEvent:
		if event.TaskID != "" || event.Kind != "heartbeat" || event.Key != "heartbeat" {
			return errors.New("monitor: heartbeat pending event is invalid")
		}
	}
	return nil
}
