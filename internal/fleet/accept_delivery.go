package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// Acceptance is what the CEO accepts: one generation of a task, the files of
// its saved acceptance report and installation receipt, and the identity that
// accepts them.
type Acceptance struct {
	ID         string
	Generation string
	Report     string
	Receipt    string
	AcceptedBy string
}

// Installation reads what an installation receipt names on this machine.
type Installation struct {
	// Hash is the SHA-256 of the file at path, in lowercase hex.
	Hash func(path string) (string, error)
	// DaemonImage is the program file the process of pid, created at
	// started, runs now.
	DaemonImage func(pid int, started time.Time) (string, error)
}

// AcceptDelivery records that one generation of a task delivered, once its
// receipt names that task and generation, the installed program holds the
// receipt's bytes and the receipt's daemon runs them. It changes neither the
// task's record nor its outcome. The same evidence given again changes
// nothing and reads nothing from the machine, which may have moved on since;
// other evidence for an accepted generation is refused. isNew says this call
// recorded it.
func AcceptDelivery(h home.Home, request Acceptance, installation Installation) (isNew bool, err error) {
	if err := state.ValidTaskID(request.ID); err != nil {
		return false, err
	}
	name := ".accept-delivery-" + request.ID + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, lock.ReleaseExclusiveNamed(h.State, name)) }()
	if _, err := state.ReadOutcome(h.State, request.ID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read the outcome of %s: %w", request.ID, err)
	}
	generation, err := state.TaskGeneration(h.State, request.ID)
	if err != nil {
		return false, err
	}
	if request.Generation != generation || generation == "queued" {
		return false, fmt.Errorf("%s is at generation %s, and only the generation it stands at can be accepted, once it has run", request.ID, generation)
	}
	report, err := savedEvidence(h, request.Report)
	if err != nil {
		return false, fmt.Errorf("acceptance report: %w", err)
	}
	data, err := savedEvidence(h, request.Receipt)
	if err != nil {
		return false, fmt.Errorf("installation receipt: %w", err)
	}
	var receipt state.InstallationReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return false, fmt.Errorf("installation receipt: %w", err)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return false, errors.New("installation receipt holds more than one value")
	}
	if err := receipt.Validate(); err != nil {
		return false, err
	}
	if receipt.Task != request.ID || receipt.Generation != generation {
		return false, fmt.Errorf("the installation receipt names %s at generation %s, not %s at %s", receipt.Task, receipt.Generation, request.ID, generation)
	}
	digest := sha256.Sum256(report)
	record := state.AcceptedDelivery{Receipt: receipt, Report: request.Report, ReportSHA256: hex.EncodeToString(digest[:]), AcceptedBy: request.AcceptedBy, At: time.Now().UTC()}

	accepted, err := state.ReadAcceptedDelivery(h.State, request.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if err == nil && accepted.Receipt.Generation == generation {
		if accepted.ReportSHA256 != record.ReportSHA256 || !isSameInstallation(accepted.Receipt, receipt) {
			return false, fmt.Errorf("this evidence contradicts the delivery of %s accepted at %s from %s, which stays as it is", request.ID, accepted.At.Format(time.RFC3339), accepted.Report)
		}
		return false, nil
	}

	installed, err := installation.Hash(receipt.Image.Path)
	if err != nil {
		return false, fmt.Errorf("read the installed program %s: %w", receipt.Image.Path, err)
	}
	if installed != receipt.Image.SHA256 {
		return false, fmt.Errorf("the installed program %s hashes to %s, not the receipt's %s", receipt.Image.Path, installed, receipt.Image.SHA256)
	}
	image, err := installation.DaemonImage(receipt.Daemon.PID, receipt.Daemon.Started)
	if err != nil {
		return false, fmt.Errorf("read the daemon pid %d: %w", receipt.Daemon.PID, err)
	}
	running, err := installation.Hash(image)
	if err != nil {
		return false, fmt.Errorf("read the program %s the daemon runs: %w", image, err)
	}
	if running != receipt.Image.SHA256 {
		return false, fmt.Errorf("the daemon pid %d runs %s, which hashes to %s, not the installed program's %s", receipt.Daemon.PID, image, running, receipt.Image.SHA256)
	}
	if err := state.WriteAcceptedDelivery(h.State, record); err != nil {
		return false, err
	}
	return true, nil
}

// savedEvidence reads a non-empty file the home keeps in its data folder,
// where it outlives the task's worktree and scratch.
func savedEvidence(h home.Home, path string) ([]byte, error) {
	relative, err := filepath.Rel(h.Data, path)
	if err != nil || !filepath.IsLocal(relative) {
		return nil, fmt.Errorf("%s is not in the home's data folder %s", path, h.Data)
	}
	data, err := fsx.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	return data, nil
}

func isSameInstallation(first, second state.InstallationReceipt) bool {
	return first.Image == second.Image && first.Daemon.PID == second.Daemon.PID && first.Daemon.Started.Equal(second.Daemon.Started)
}
