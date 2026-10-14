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

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Custody keeps the admission slot protected until its caller has proved
// all of this execution's workers gone. It is advisory same-user state,
// never authorization to publish a successful check.
type Custody struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
	// Job names the Windows job the execution's workers run in, and
	// Controller the process that starts them, so that once that process has
	// gone a later run can prove its workers gone too.
	Job        string    `json:"job"`
	Controller lock.Info `json:"controller"`
	// Created is when the custody was recorded, before any worker started.
	Created time.Time `json:"created"`
	// Evidence is the execution's record, which recovery marks interrupted.
	Evidence string `json:"evidence,omitempty"`
	dir      string
}

// jobNamespace is where the Windows jobs of executions are named: the
// machine's global namespace, so a run in any logon session, or after the
// controller's session has ended, finds the job by its name. The run ID
// after it is random, so no other run can guess an execution's job.
const jobNamespace = `Global\cfo-verify-`

// JobName is the name of the Windows job an execution's workers run in.
func JobName(runID string) string {
	return jobNamespace + runID
}

// Protect records custody of an execution's workers before any of them
// starts. evidence names the execution's record, or is empty for none.
func (turn Turn) Protect(runID, evidence string) (guard Custody, failure error) {
	if runID == "" || strings.ContainsAny(runID, `/\:.`) || strings.Trim(runID, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return Custody{}, errors.New("verify: invalid execution run ID")
	}
	if turn.dir == "" {
		return Custody{}, errors.New("verify: cannot protect a turn without live custody")
	}
	controller, err := currentController()
	if err != nil {
		return Custody{}, err
	}
	unlock, err := custodyMutation(turn.dir)
	if err != nil {
		return Custody{}, err
	}
	defer func() { failure = errors.Join(failure, unlock()) }()
	if !(Admission{Dir: turn.dir}).isHeldAs(turn.slot, turn.token) {
		return Custody{}, errors.New("verify: cannot protect a turn without live custody")
	}
	guard = Custody{RunID: runID, Token: turn.token, Job: JobName(runID), Controller: controller, Created: time.Now().UTC(), Evidence: evidence, dir: filepath.Join(turn.dir, "custody")}
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

// errCustodyBusy says a custody change found the line's custody busy for all
// the few seconds it is given, as a loaded machine can keep it, while its
// caller still waits: the change did nothing, and its caller may try again.
var errCustodyBusy = errors.New("verify: the line's custody stayed busy")

// busy marks err as errCustodyBusy when it came of a custody change's own
// seconds running out while caller still waits.
func busy(caller context.Context, err error) error {
	if caller.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", errCustodyBusy, err)
	}
	return err
}

var custodyMutations = struct {
	sync.Mutex
	pending map[string]lock.Info
}{pending: make(map[string]lock.Info)}

func custodyMutationContext(caller context.Context, dir string) (func() error, error) {
	unlock, err := takeCustodyMutation(caller, dir)
	return unlock, busy(caller, err)
}

func takeCustodyMutation(caller context.Context, dir string) (func() error, error) {
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
		guard.dir = dir
		guards = append(guards, guard)
	}
	return guards, nil
}

// checkWorkerCustody refuses admission while any execution's workers may
// still run outside a live turn, after recovering the custody of each
// execution whose controller has verifiably gone. The caller holds the
// custody mutation.
func (admission Admission) checkWorkerCustody() error {
	guards, err := admission.workerCustody()
	if err != nil {
		return fmt.Errorf("verify: read worker custody: %w", err)
	}
	var abandoned []Custody
	var failure error
	for _, guard := range guards {
		if guard.isAbandoned() {
			abandoned = append(abandoned, guard)
			continue
		}
		slot, _, _ := strings.Cut(guard.Token, ":")
		if !admission.isHeldAs(slot, guard.Token) {
			failure = errors.Join(failure, fmt.Errorf("verify: execution %s has unconfirmed worker cleanup (custody record %s): its controller, pid %d, cannot be shown to have exited", guard.RunID, guard.path(), guard.Controller.PID))
		}
	}
	if len(abandoned) > 0 {
		failure = errors.Join(failure, recoverAbandoned(context.Background(), abandoned))
	}
	return failure
}

// path is where this custody is recorded.
func (guard Custody) path() string {
	return filepath.Join(guard.dir, guard.RunID+".json")
}

// isAbandoned reports whether the process that recorded this custody is
// verifiably gone. A record that names no controller, or whose controller's
// state cannot be read, is not abandoned: its workers are not proven gone.
func (guard Custody) isAbandoned() bool {
	return guard.Controller.PID > 0 && !guard.Controller.Start.IsZero() && !guard.Controller.Alive()
}

// recoverAbandoned frees the custody of executions whose controller has
// gone, once Windows shows nothing of them runs: each job ended and empty,
// or gone, and no worker its controller started outside it. A dead
// controller alone proves nothing. Every job is ended before any
// controller's strays are counted, since until then the executions of one
// controller are each other's strays. A pending execution's record is
// marked failed and interrupted, so recovery never passes a run. The caller
// holds the custody mutation.
func recoverAbandoned(ctx context.Context, guards []Custody) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type ending struct {
		usage   execx.TreeUsage
		isFound bool
		failure error
	}
	endings := make([]ending, len(guards))
	for index, guard := range guards {
		if guard.Job != JobName(guard.RunID) {
			endings[index].failure = errors.New("its custody names another run's job")
			continue
		}
		endings[index].usage, endings[index].isFound, endings[index].failure = guard.endJob(ctx)
	}
	var failure error
	for index, guard := range guards {
		err := endings[index].failure
		if err == nil {
			err = guard.finishRecovery(ctx, endings[index].usage, endings[index].isFound)
		}
		if err != nil {
			failure = errors.Join(failure, fmt.Errorf("verify: execution %s has unconfirmed worker cleanup (custody record %s): %w", guard.RunID, guard.path(), err))
		}
	}
	return failure
}

func (guard Custody) finishRecovery(ctx context.Context, usage execx.TreeUsage, isFound bool) error {
	strays, err := guard.strayWorkers(ctx)
	if err != nil {
		return err
	}
	if len(strays) > 0 {
		started := make([]string, len(strays))
		for index, stray := range strays {
			started[index] = fmt.Sprintf("%s pid %d", stray.ExeBase, stray.PID)
		}
		return fmt.Errorf("its controller, pid %d, started %s outside its job, which still runs. End it once it is known to be this execution's, and the next run recovers the custody", guard.Controller.PID, strings.Join(started, ", "))
	}
	if guard.Evidence != "" {
		note := fmt.Sprintf("its controller, pid %d, ended before its workers were confirmed gone, and pid %d recovered its custody once Windows showed its job ", guard.Controller.PID, os.Getpid())
		if isFound {
			note += "ended and empty"
		} else {
			note += "gone"
		}
		// The workers are proven gone, and a record that is not marked stays
		// pending, which never passes, so a record that cannot be read or
		// written never keeps the machine's line shut.
		_ = markRecovered(ctx, guard.Evidence, guard.RunID, usage, isFound, note)
	}
	return guard.removeIfUnchanged(ctx)
}

// removeIfUnchanged removes this custody record while it is still the one
// recovery proved, so a record replaced since is never taken for it.
func (guard Custody) removeIfUnchanged(ctx context.Context) error {
	data, err := fsx.ReadFileContext(ctx, guard.path())
	if err != nil {
		return err
	}
	var current Custody
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if !current.isSame(guard) {
		return errors.New("its custody record changed during recovery")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(guard.path())
}

func (guard Custody) isSame(other Custody) bool {
	return guard.RunID == other.RunID && guard.Token == other.Token && guard.Job == other.Job &&
		guard.Evidence == other.Evidence && guard.Created.Equal(other.Created) && guard.Controller.PID == other.Controller.PID &&
		guard.Controller.Start.Equal(other.Controller.Start) && guard.Controller.Hostname == other.Controller.Hostname
}
