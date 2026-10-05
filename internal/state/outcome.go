package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type Outcome struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	Title      string    `json:"title"`
	Project    string    `json:"project"`
	Harness    string    `json:"harness,omitempty"`
	Model      string    `json:"model,omitempty"`
	Effort     string    `json:"effort,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Phase      string    `json:"phase"`
	Reason     string    `json:"reason"`
	PR         string    `json:"pr,omitempty"`
	Evidence   string    `json:"evidence,omitempty"`
	At         time.Time `json:"at"`
}

func WriteOutcome(directory string, outcome Outcome) error {
	if err := ValidTaskID(outcome.ID); err != nil {
		return err
	}
	if outcome.Phase != "stopped" && outcome.Phase != "done" || outcome.Phase == "done" && outcome.Evidence == "" {
		return errors.New("completed work requires delivery evidence")
	}
	data, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "outcomes"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, "outcomes", outcome.ID+".json"), data)
}

func ReadOutcome(directory, id string) (Outcome, error) {
	if err := ValidTaskID(id); err != nil {
		return Outcome{}, err
	}
	data, err := fsx.ReadFile(filepath.Join(directory, "outcomes", id+".json"))
	if err != nil {
		return Outcome{}, err
	}
	var outcome Outcome
	if err := json.Unmarshal(data, &outcome); err != nil {
		return Outcome{}, err
	}
	if outcome.ID != id || outcome.Phase != "stopped" && outcome.Phase != "done" || outcome.Phase == "done" && outcome.Evidence == "" {
		return Outcome{}, errors.New("invalid task outcome")
	}
	return outcome, nil
}
