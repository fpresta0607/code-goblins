package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func (turn Turn) Protect(runID string) (guard Custody, failure error) {
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
	defer func() { failure = errors.Join(failure, unlock()) }()
	if !(Admission{Dir: turn.dir}).isHeldAs(turn.slot, turn.token) {
		return Custody{}, errors.New("verify: cannot protect a turn without live custody")
	}
	guard = Custody{RunID: runID, Token: turn.token, dir: filepath.Join(turn.dir, "custody")}
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
	return guard.CompleteContext(context.Background())
}

func (guard Custody) CompleteContext(ctx context.Context) (failure error) {
	if guard.dir == "" || guard.RunID == "" || guard.Token == "" || strings.Trim(guard.RunID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return errors.New("verify: no execution custody to complete")
	}
	unlock, err := custodyMutationContext(ctx, filepath.Dir(guard.dir))
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, unlock()) }()
	path := filepath.Join(guard.dir, guard.RunID+".json")
	data, err := fsx.ReadFileContext(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(path)
}

// Admission, protection and release share one short critical section, so
// releasing a slot cannot race a nested command recording its workers.
func custodyMutation(dir string) (func() error, error) {
	return custodyMutationContext(context.Background(), dir)
}

var custodyMutations = struct {
	sync.Mutex
	pending map[string]lock.Info
}{pending: make(map[string]lock.Info)}

func custodyMutationContext(caller context.Context, dir string) (func() error, error) {
	key, err := fsx.AbsClean(dir)
	if err != nil {
		return nil, err
	}
	key = strings.ToLower(key)
	ctx, cancel := context.WithTimeout(caller, 5*time.Second)
	defer cancel()
	const name = "custody-mutation"
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if err := lockBefore(ctx, &custodyMutations.Mutex); err != nil {
			return nil, err
		}
		if owner, pending := custodyMutations.pending[key]; pending {
			if err := lock.ReleaseExclusiveOwned(ctx, dir, name, owner); err != nil {
				custodyMutations.Unlock()
				return nil, fmt.Errorf("verify: retry worker custody release: %w", err)
			}
			delete(custodyMutations.pending, key)
		}
		owner, err := lock.AcquireExclusiveNamedStrictContext(ctx, dir, name)
		if err != nil && owner != nil {
			custodyMutations.pending[key] = *owner
		}
		if err == nil {
			return func() error {
				defer custodyMutations.Unlock()
				releaseContext, cancel := context.WithTimeout(caller, 5*time.Second)
				defer cancel()
				if err := lock.ReleaseExclusiveOwned(releaseContext, dir, name, *owner); err != nil {
					custodyMutations.pending[key] = *owner
					return fmt.Errorf("verify: release worker custody mutation: %w", err)
				}
				return nil
			}, nil
		}
		custodyMutations.Unlock()
		if !errors.Is(err, lock.ErrHeld) {
			return nil, fmt.Errorf("verify: worker custody mutation: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func lockBefore(ctx context.Context, mutex *sync.Mutex) error {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !mutex.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		mutex.Unlock()
		return err
	}
	return nil
}

func (admission Admission) workerCustody() ([]Custody, error) {
	return admission.workerCustodyContext(context.Background())
}

func (admission Admission) workerCustodyContext(ctx context.Context) ([]Custody, error) {
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
		data, err := fsx.ReadFileContext(ctx, filepath.Join(dir, entry.Name()))
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
