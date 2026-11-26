package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// InstallationReceipt is the identity of the installation one generation of a
// task delivered: the program it installed and the daemon that runs it.
type InstallationReceipt struct {
	Task       string          `json:"task"`
	Generation string          `json:"generation"`
	Image      InstalledImage  `json:"image"`
	Daemon     InstalledDaemon `json:"daemon"`
}

// InstalledImage is an installed program file and the SHA-256 of its bytes.
type InstalledImage struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// InstalledDaemon is a running process, named by its pid and creation time.
type InstalledDaemon struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// AcceptedDelivery says the CEO accepted one generation of a task as delivered
// from its saved acceptance report and installation receipt. It is a fact of
// its own beside the task's outcome, which says how the task was retired, so a
// task cleanup retired as stopped stays stopped and still delivered.
type AcceptedDelivery struct {
	Receipt      InstallationReceipt `json:"receipt"`
	Report       string              `json:"report"`
	ReportSHA256 string              `json:"report_sha256"`
	AcceptedBy   string              `json:"accepted_by"`
	At           time.Time           `json:"at"`
}

// Validate refuses a receipt that does not name a task's generation, an
// installed program and a daemon.
func (receipt InstallationReceipt) Validate() error {
	if err := ValidTaskID(receipt.Task); err != nil {
		return fmt.Errorf("installation receipt: %w", err)
	}
	switch {
	case receipt.Generation == "":
		return errors.New("installation receipt names no generation")
	case !filepath.IsAbs(receipt.Image.Path) || !isSHA256(receipt.Image.SHA256):
		return errors.New("installation receipt needs the installed image's full path and lowercase SHA-256")
	case receipt.Daemon.PID <= 0 || receipt.Daemon.Started.IsZero():
		return errors.New("installation receipt needs the daemon's pid and creation time")
	}
	return nil
}

func isSHA256(digest string) bool {
	return len(digest) == 64 && strings.Trim(digest, "0123456789abcdef") == ""
}

func (record AcceptedDelivery) validate() error {
	if err := record.Receipt.Validate(); err != nil {
		return err
	}
	if record.Report == "" || !isSHA256(record.ReportSHA256) || record.AcceptedBy == "" || record.At.IsZero() {
		return errors.New("accepted delivery needs its saved report, who accepted it and when")
	}
	return nil
}

// AcceptedDeliveryPath is the file that holds the delivery accepted for task id.
func AcceptedDeliveryPath(directory, id string) string {
	return filepath.Join(directory, "accepted-deliveries", id+".json")
}

// ReadAcceptedDelivery reads the delivery accepted for task id; a task with
// none returns an error that is os.ErrNotExist.
func ReadAcceptedDelivery(directory, id string) (AcceptedDelivery, error) {
	if err := ValidTaskID(id); err != nil {
		return AcceptedDelivery{}, err
	}
	data, err := fsx.ReadFile(AcceptedDeliveryPath(directory, id))
	if err != nil {
		return AcceptedDelivery{}, err
	}
	var record AcceptedDelivery
	if err := json.Unmarshal(data, &record); err != nil {
		return AcceptedDelivery{}, fmt.Errorf("read the accepted delivery of %s: %w", id, err)
	}
	if record.Receipt.Task != id {
		return AcceptedDelivery{}, errors.New("the accepted delivery of " + id + " names another task")
	}
	if err := record.validate(); err != nil {
		return AcceptedDelivery{}, fmt.Errorf("read the accepted delivery of %s: %w", id, err)
	}
	return record, nil
}

// WriteAcceptedDelivery keeps record as its task's accepted delivery.
func WriteAcceptedDelivery(directory string, record AcceptedDelivery) error {
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	path := AcceptedDeliveryPath(directory, record.Receipt.Task)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsx.AtomicWriteFile(path, data)
}

// TaskGeneration is the generation task id stands at: its live record's, or,
// once it is retired, its outcome's.
func TaskGeneration(directory, id string) (string, error) {
	meta, err := ReadTaskMeta(directory, id)
	if err == nil {
		return meta.SpawnGen, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read the record of %s: %w", id, err)
	}
	outcome, err := ReadOutcome(directory, id)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s has neither a live record nor a retirement outcome", id)
	}
	if err != nil {
		return "", fmt.Errorf("read the outcome of %s: %w", id, err)
	}
	return outcome.Generation, nil
}

// DeliveredAt says whether task id delivered, and when: its outcome says it
// did, or the CEO accepted the delivery of the generation it stands at. An
// accepted delivery of any other generation delivers nothing, and evidence
// that cannot be read is an error, never an answer.
func DeliveredAt(directory, id string) (time.Time, bool, error) {
	if err := ValidTaskID(id); err != nil {
		return time.Time{}, false, err
	}
	isRecorded := false
	for _, path := range []string{filepath.Join(directory, "outcomes", id+".json"), AcceptedDeliveryPath(directory, id)} {
		_, err := os.Stat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return time.Time{}, false, fmt.Errorf("read the delivery of %s: %w", id, err)
		}
		isRecorded = isRecorded || err == nil
	}
	if !isRecorded {
		return time.Time{}, false, nil
	}
	outcome, err := ReadOutcome(directory, id)
	if err == nil && outcome.Phase == "done" {
		return outcome.At, true, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, fmt.Errorf("read the outcome of %s: %w", id, err)
	}
	accepted, err := ReadAcceptedDelivery(directory, id)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	generation, err := TaskGeneration(directory, id)
	if err != nil || accepted.Receipt.Generation != generation {
		return time.Time{}, false, err
	}
	return accepted.At, true, nil
}
