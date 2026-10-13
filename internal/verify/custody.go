package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Custody keeps the admission slot protected until its caller has proved
// all of this execution's workers gone. It is advisory same-user state,
// never authorization to publish a successful check.
type Custody struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
	dir   string
}

func (turn Turn) Protect(runID string) (Custody, error) {
	if runID == "" || strings.ContainsAny(runID, `/\:.`) || strings.Trim(runID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return Custody{}, errors.New("verify: invalid execution run ID")
	}
	if turn.dir == "" {
		return Custody{}, errors.New("verify: cannot protect a turn without live custody")
	}
	unlock, err := custodyMutation(turn.dir)
	if err != nil {
		return Custody{}, err
	}
	defer unlock()
	if !(Admission{Dir: turn.dir}).isHeldAs(turn.slot, turn.token) {
		return Custody{}, errors.New("verify: cannot protect a turn without live custody")
	}
	guard := Custody{RunID: runID, Token: turn.token, dir: filepath.Join(turn.dir, "custody")}
	if err := os.MkdirAll(guard.dir, 0700); err != nil {
		return Custody{}, err
	}
	data, err := json.Marshal(guard)
	if err != nil {
		return Custody{}, err
	}
	file, err := os.OpenFile(filepath.Join(guard.dir, runID+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Custody{}, err
	}
	_, writeError := file.Write(data)
	syncError := file.Sync()
	closeError := file.Close()
	if err := errors.Join(writeError, syncError, closeError); err != nil {
		return Custody{}, err
	}
	return guard, nil
}

// Complete removes only this run's record after its caller proves cleanup.
// A failed removal leaves the slot protected and must fail the execution.
func (guard Custody) Complete() error {
	if guard.dir == "" || guard.RunID == "" || guard.Token == "" || strings.Trim(guard.RunID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return errors.New("verify: no execution custody to complete")
	}
	unlock, err := custodyMutation(filepath.Dir(guard.dir))
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(guard.dir, guard.RunID+".json")
	data, err := fsx.ReadFile(path)
	if err != nil {
		return err
	}
	var current Custody
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.RunID != guard.RunID || current.Token != guard.Token {
		return errors.New("verify: execution custody changed before cleanup")
	}
	return os.Remove(path)
}

// Admission, protection and release share one short critical section, so
// releasing a slot cannot race a nested command recording its workers.
func custodyMutation(dir string) (func(), error) {
	const name = "custody-mutation"
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := lock.AcquireExclusiveNamedStrict(dir, name); err == nil {
			return func() { _ = lock.ReleaseExclusiveNamed(dir, name) }, nil
		} else if !errors.Is(err, lock.ErrHeld) || !time.Now().Before(deadline) {
			return nil, fmt.Errorf("verify: worker custody mutation: %w", err)
		}
	}
}

func (admission Admission) workerCustody() ([]Custody, error) {
	dir := filepath.Join(admission.Dir, "custody")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var guards []Custody
	for _, entry := range entries {
		data, err := fsx.ReadFile(filepath.Join(dir, entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var guard Custody
		if err := json.Unmarshal(data, &guard); err != nil {
			return nil, err
		}
		if guard.RunID == "" || guard.Token == "" || entry.Name() != guard.RunID+".json" {
			return nil, fmt.Errorf("verify: unreadable worker custody %s", entry.Name())
		}
		guards = append(guards, guard)
	}
	return guards, nil
}

func (admission Admission) checkWorkerCustody() error {
	guards, err := admission.workerCustody()
	if err != nil {
		return fmt.Errorf("verify: read worker custody: %w", err)
	}
	for _, guard := range guards {
		slot, _, _ := strings.Cut(guard.Token, ":")
		if !admission.isHeldAs(slot, guard.Token) {
			return fmt.Errorf("verify: execution %s has unconfirmed worker cleanup", guard.RunID)
		}
	}
	return nil
}
