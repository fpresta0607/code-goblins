package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The steps a handoff takes, in order, each recorded once it ran, so a
// failure part-way says which ran and a second handoff goes on from there.
const (
	StepPause       = "pause"
	StepCommit      = "commit"
	StepPush        = "push"
	StepPullRequest = "pull-request"
	StepPacket      = "packet"
)

// The steps a return takes, recorded the same way.
const (
	ReturnStepMerge  = "merge"
	ReturnStepPush   = "push"
	ReturnStepClose  = "close"
	ReturnStepResume = "resume"
)

// The ways a handoff ends.
const (
	OutcomeReturned  = "returned"
	OutcomeCancelled = "cancelled"
)

// Record is a task's handed_off: the handoff in progress, the task handed
// off, or, once it ended, how it ended and what the cloud brought.
type Record struct {
	Task    string    `json:"task"`
	Started time.Time `json:"started"`
	Steps   []string  `json:"steps"`
	Branch  string    `json:"branch,omitempty"`
	// PullRequest is the goblin's pull request the packet is written in,
	// and IsOpenedByHandoff says the handoff opened it as a draft, so a
	// gated task's return closes it and its gate opens its own ready one.
	PullRequest       string `json:"pull_request,omitempty"`
	IsOpenedByHandoff bool   `json:"is_opened_by_handoff,omitempty"`
	// Head is the goblin's branch head the cloud session builds on.
	Head         string     `json:"head,omitempty"`
	PacketDigest string     `json:"packet_digest,omitempty"`
	HandedOff    *time.Time `json:"handed_off,omitempty"`
	// ReturnSteps, ReturnHead and CloudPullRequest are what a return did
	// so far: the head its merge made, before its push.
	ReturnSteps      []string   `json:"return_steps,omitempty"`
	ReturnHead       string     `json:"return_head,omitempty"`
	CloudPullRequest string     `json:"cloud_pull_request,omitempty"`
	CloudCommits     []string   `json:"cloud_commits,omitempty"`
	Ended            *time.Time `json:"ended,omitempty"`
	Outcome          string     `json:"outcome,omitempty"`
}

// IsActive says the handoff has not ended: it is in progress or the task is
// handed off.
func (record Record) IsActive() bool {
	return record.Ended == nil
}

// IsHandedOff says every step of the handoff ran and it has not ended.
func (record Record) IsHandedOff() bool {
	return record.HandedOff != nil && record.Ended == nil
}

func (record Record) hasStep(step string) bool {
	return slices.Contains(record.Steps, step)
}

func (record Record) hasReturnStep(step string) bool {
	return slices.Contains(record.ReturnSteps, step)
}

func recordPath(stateDir, task string) (string, error) {
	if err := state.ValidTaskID(task); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "handoffs", task+".json"), nil
}

// ReadRecord reads task's handoff record, or an error wrapping
// os.ErrNotExist when the task was never handed off.
func ReadRecord(stateDir, task string) (Record, error) {
	path, err := recordPath(stateDir, task)
	if err != nil {
		return Record{}, err
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("read the handoff record of %s: %w", task, err)
	}
	if record.Task != task {
		return Record{}, fmt.Errorf("the handoff record of %s names task %q", task, record.Task)
	}
	return record, nil
}

func readActiveRecord(stateDir, task string) (Record, bool, error) {
	record, err := ReadRecord(stateDir, task)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	return record, record.IsActive(), nil
}

// WriteRecord writes record whole or not at all.
func WriteRecord(stateDir string, record Record) error {
	path, err := recordPath(stateDir, record.Task)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, append(data, '\n'))
}
