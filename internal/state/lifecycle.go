package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

type Lifecycle struct {
	ID                string            `json:"id"`
	Generation        string            `json:"generation"`
	RequestGeneration string            `json:"request_generation"`
	Title             string            `json:"title,omitempty"`
	Project           string            `json:"project,omitempty"`
	Operation         string            `json:"operation"`
	Action            string            `json:"action"`
	Phase             string            `json:"phase"`
	Started           time.Time         `json:"started"`
	Updated           time.Time         `json:"updated"`
	Reason            string            `json:"reason"`
	Session           string            `json:"session,omitempty"`
	GateRun           string            `json:"gate_run,omitempty"`
	GateIntent        string            `json:"gate_intent,omitempty"`
	GateHead          string            `json:"gate_head,omitempty"`
	Handoff           string            `json:"handoff,omitempty"`
	HandoffSaved      bool              `json:"handoff_saved"`
	Stopped           []string          `json:"stopped,omitempty"`
	Teardown          []TeardownProcess `json:"teardown,omitempty"`
	Kept              []string          `json:"kept,omitempty"`
	Problems          []string          `json:"problems,omitempty"`
	NoticeSent        bool              `json:"notice_sent"`
}

type TeardownProcess struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Name    string    `json:"name"`
}

func (record Lifecycle) TeardownLabels() []string {
	var labels []string
	for _, process := range record.Teardown {
		labels = append(labels, fmt.Sprintf("%s pid %d", process.Name, process.PID))
	}
	return labels
}

func (record Lifecycle) TeardownStatus() string {
	if len(record.Teardown) == 0 {
		return ""
	}
	return "finishing Windows teardown: " + strings.Join(record.TeardownLabels(), ", ")
}

// SuppressesMonitoring requires a completed stop or a live controller.
// Failed and interrupted operations can leave live processes behind.
func (record Lifecycle) SuppressesMonitoring(directory string) bool {
	if record.Phase == "paused" || record.Phase == "stopped" {
		return true
	}
	if record.Phase == "pausing" || record.Phase == "resuming" || record.Phase == "stopping" {
		controller, err := lock.ReadNamed(directory, ".lifecycle-"+record.ID+".lock")
		return err == nil && controller.Alive()
	}
	return false
}

// LifecyclePath is where task id's lifecycle record is.
func LifecyclePath(directory, id string) string {
	return filepath.Join(directory, "lifecycle", id+".json")
}

func ReadLifecycle(directory, id string) (Lifecycle, error) {
	if err := ValidTaskID(id); err != nil {
		return Lifecycle{}, err
	}
	data, err := fsx.ReadFile(LifecyclePath(directory, id))
	if err != nil {
		return Lifecycle{}, err
	}
	var record Lifecycle
	if err := json.Unmarshal(data, &record); err != nil {
		return Lifecycle{}, fmt.Errorf("read lifecycle %s: %w", id, err)
	}
	if record.ID != id {
		return Lifecycle{}, errors.New("lifecycle task identity does not match")
	}
	if err := record.validate(); err != nil {
		return record, err
	}
	record.Teardown = pendingTeardown(record.Teardown)
	return record, nil
}

func WriteLifecycle(directory string, record Lifecycle) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "lifecycle"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, "lifecycle", record.ID+".json"), data)
}

func (record Lifecycle) validate() error {
	for _, process := range record.Teardown {
		if process.PID <= 0 || process.Started.IsZero() || process.Name == "" {
			return errors.New("teardown requires a named process and birth-checked identity")
		}
	}
	if err := ValidTaskID(record.ID); err != nil {
		return err
	}
	if record.Operation == "" || !slices.Contains([]string{"pause", "resume", "stop"}, record.Action) {
		return errors.New("lifecycle requires a known action and operation identity")
	}
	if !slices.Contains([]string{"pausing", "paused", "resuming", "running", "stopping", "stopped", "failed"}, record.Phase) {
		return fmt.Errorf("unknown lifecycle phase %q", record.Phase)
	}
	return nil
}
