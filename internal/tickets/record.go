package tickets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Record is what the supervisor keeps of a task's ticket: where its issue is,
// whether the task opened it or claimed it, and what it last wrote there.
// The supervisor is its only writer.
type Record struct {
	TaskID     string   `json:"task_id"`
	Repository string   `json:"repository"`
	Number     int      `json:"number"`
	URL        string   `json:"url"`
	IsClaimed  bool     `json:"claimed,omitempty"`
	CommentID  int64    `json:"comment_id,omitempty"`
	State      State    `json:"state"`
	Status     string   `json:"status"`
	Labels     []string `json:"labels"`
	IsDone     bool     `json:"done,omitempty"`
	// Title is the title last written to an issue the task opened.
	Title string `json:"title,omitempty"`
	// PullRequest is the task's pull request, kept once a ticket names it,
	// so its merge can close the ticket after the task has left the board.
	PullRequest string `json:"pull_request,omitempty"`
	// Note is what the board says about the ticket while it is open, such
	// as why the issue the task named was not claimed.
	Note string `json:"note,omitempty"`
	// Overlap is the reason the CFO gave for starting the task beside a
	// teammate's work, as last written to the ticket.
	Overlap string `json:"overlap,omitempty"`
}

// ListRecords reads every task's ticket record, in task order. A file that
// is not a task's record is passed over.
func ListRecords(directory string) ([]Record, error) {
	entries, err := os.ReadDir(filepath.Join(directory, "tickets"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, entry := range entries {
		taskID, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if !isRecord || entry.IsDir() {
			continue
		}
		if record, err := ReadRecord(directory, taskID); err == nil {
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b Record) int { return strings.Compare(a.TaskID, b.TaskID) })
	return records, nil
}

// WriteRecord keeps a task's ticket record under directory/tickets.
func WriteRecord(directory string, record Record) error {
	path, err := recordPath(directory, record.TaskID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// ReadRecord reads a task's ticket record; a task with none returns an error
// that errors.Is matches to os.ErrNotExist.
func ReadRecord(directory, taskID string) (Record, error) {
	path, err := recordPath(directory, taskID)
	if err != nil {
		return Record{}, err
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("read the ticket record of %s: %w", taskID, err)
	}
	if record.TaskID != taskID {
		return Record{}, errors.New("ticket record task identity does not match")
	}
	return record, nil
}

// Harness is the harness the ticket's goblin runs on, as its label names it,
// or empty for a ticket no goblin has worked.
func (r Record) Harness() string {
	for _, label := range r.Labels {
		if name, ok := strings.CutPrefix(label, harnessLabel("")); ok {
			return name
		}
	}
	return ""
}

func recordPath(directory, taskID string) (string, error) {
	if err := state.ValidTaskID(taskID); err != nil {
		return "", err
	}
	return filepath.Join(directory, "tickets", taskID+".json"), nil
}
