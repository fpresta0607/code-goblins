package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

type EngineChoice struct {
	ID         string    `json:"id"`
	Generation string    `json:"generation"`
	Harness    string    `json:"harness"`
	Model      string    `json:"model"`
	Effort     string    `json:"effort"`
	When       string    `json:"when"`
	Requested  time.Time `json:"requested"`
}

func WriteEngineChoice(directory string, choice EngineChoice) error {
	if err := choice.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(choice)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "engine"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, "engine", choice.ID+".json"), data)
}

func ReadEngineChoice(directory, id string) (EngineChoice, error) {
	if err := ValidTaskID(id); err != nil {
		return EngineChoice{}, err
	}
	data, err := fsx.ReadFile(filepath.Join(directory, "engine", id+".json"))
	if err != nil {
		return EngineChoice{}, err
	}
	var choice EngineChoice
	if err := json.Unmarshal(data, &choice); err != nil {
		return EngineChoice{}, err
	}
	if choice.ID != id {
		return EngineChoice{}, errors.New("engine choice task identity does not match")
	}
	return choice, choice.validate()
}

func RemoveEngineChoice(directory, id string) error {
	if err := ValidTaskID(id); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(directory, "engine", id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (choice EngineChoice) validate() error {
	if err := ValidTaskID(choice.ID); err != nil {
		return err
	}
	if choice.Generation == "" || choice.Harness == "" || choice.When != "resume" && choice.When != "turn-end" {
		return errors.New("engine choice requires a session, harness and switch timing")
	}
	return nil
}
