package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// PreparedWait is explicit permission to pause a prepared generation. Phase
// records admission before calling lifecycle, so an uncertain call is never
// automatically repeated after a restart.
type PreparedWait struct {
	ID         string         `json:"id"`
	Generation string         `json:"generation"`
	Project    string         `json:"project"`
	Worktree   string         `json:"worktree"`
	Session    string         `json:"session"`
	Branch     string         `json:"branch"`
	Head       string         `json:"head"`
	Handoff    string         `json:"handoff"`
	Digest     string         `json:"digest"`
	Condition  PauseCondition `json:"condition"`
	PreparedAt time.Time      `json:"prepared_at"`
	Operation  string         `json:"operation"`
	Phase      string         `json:"phase"`
	Problem    string         `json:"problem,omitempty"`
}

func (record PreparedWait) OperationID() string {
	identity := strings.Join([]string{record.ID, record.Generation, record.Project, record.Worktree, record.Session, record.Branch, record.Head, record.Handoff, record.Digest, record.Condition.Reason, record.Condition.Until}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return "prepared-" + hex.EncodeToString(digest[:20])
}

func (record PreparedWait) Report() string {
	return "waiting on " + strings.TrimPrefix(record.Condition.Until, "task:") + ": preparation complete; no worker-owned work remains (" + record.Operation + ")"
}

func (record PreparedWait) validate() error {
	if err := ValidTaskID(record.ID); err != nil {
		return err
	}
	kind, target, _ := strings.Cut(record.Condition.Until, ":")
	if record.Condition.Reason != "dependency" || kind != "task" && kind != "pr" || kind == "task" && target == record.ID {
		return errors.New("prepared wait requires another task's delivery or a pull request merge")
	}
	if _, err := NewPauseCondition("dependency", record.Condition.Until, record.Condition.At); err != nil {
		return err
	}
	if record.Generation == "" || record.Project == "" || record.Worktree == "" || record.Session == "" || record.Branch == "" || record.Head == "" || record.Handoff == "" || len(record.Digest) != 64 || strings.Trim(record.Digest, "0123456789abcdef") != "" || record.PreparedAt.IsZero() || !record.Condition.At.Equal(record.PreparedAt) {
		return errors.New("prepared wait requires exact generation, source, session and saved handoff identity")
	}
	if record.Operation != record.OperationID() || !slices.Contains([]string{"prepared", "pause-requested", "paused", "resume-requested", "cleared", "invalid", "failed"}, record.Phase) {
		return errors.New("invalid prepared wait operation or phase")
	}
	return nil
}

func ReadPreparedWait(directory, id string) (PreparedWait, error) {
	if err := ValidTaskID(id); err != nil {
		return PreparedWait{}, err
	}
	data, err := fsx.ReadFile(filepath.Join(directory, "prepared-waits", id+".json"))
	if err != nil {
		return PreparedWait{}, err
	}
	var record PreparedWait
	if err := json.Unmarshal(data, &record); err != nil {
		return record, err
	}
	if record.ID != id {
		return record, fmt.Errorf("prepared wait task identity does not match %s", id)
	}
	return record, record.validate()
}

// CheckPreparedComeback keeps an uncertain prepared admission from being
// retried by reboot recovery, including after it published a new generation.
func CheckPreparedComeback(directory string, meta TaskMeta) error {
	record, err := ReadPreparedWait(directory, meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("prepared dependency evidence cannot be read: %w", err)
	}
	if record.Generation != meta.SpawnGen && meta.ResumeOperation != "resume-"+record.Operation {
		return nil
	}
	if record.Phase == "pause-requested" || record.Phase == "resume-requested" || record.Phase == "failed" {
		return fmt.Errorf("prepared lifecycle admission is %s; inspect before retrying", record.Phase)
	}
	return nil
}

func WritePreparedWait(directory string, record PreparedWait) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "prepared-waits"), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(directory, "prepared-waits", record.ID+".json"), data)
}
