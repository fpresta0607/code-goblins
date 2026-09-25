package host

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Record is how a running host is found again: by a supervisor started later,
// or by a view opened after it. It lives in the CFO home's state directory,
// which only this Windows user can read, because it holds the token.
type Record struct {
	ID       string    `json:"id"`
	Pipe     string    `json:"pipe"`
	Token    string    `json:"token"`
	Version  int       `json:"version"`
	HostPID  int       `json:"host_pid"`
	ChildPID int       `json:"child_pid"`
	Started  time.Time `json:"started"`
}

func recordPath(stateDir, id string) string {
	return filepath.Join(stateDir, "hosts", id+".json")
}

// ReadRecord reads the record of the host running terminal id.
func ReadRecord(stateDir, id string) (Record, error) {
	if err := state.ValidTaskID(id); err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(recordPath(stateDir, id))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("host: record %s is unreadable: %w", id, err)
	}
	if record.ID != id || record.Pipe == "" || record.Token == "" || record.HostPID <= 0 {
		return Record{}, fmt.Errorf("host: record %s is incomplete", id)
	}
	return record, nil
}

func writeRecord(stateDir string, record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(stateDir, "hosts"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(recordPath(stateDir, record.ID), data)
}

// removeRecord removes the record of terminal id only while it still names
// hostPID, so a host that ends never removes its successor's record.
func removeRecord(stateDir, id string, hostPID int) {
	record, err := ReadRecord(stateDir, id)
	if err != nil || record.HostPID != hostPID {
		return
	}
	_ = os.Remove(recordPath(stateDir, id))
}
