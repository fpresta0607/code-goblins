// Package supervise answers the question the cfo turnend-guard hook exists
// to ask: can this turn end safely? It reports whether goblin work is in
// flight, whether a live watcher holds the home, and whether the Stop-owned
// auto-arm hook (Task 11) is proving it is restoring one. It also owns the
// block-budget ladder and the epoch ledger those predicates read, and the
// failure markers that gate the ladder's one-time alarm.
package supervise

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

const (
	// watchLockName and watcherBeatFile name the files internal/watch's Run
	// writes under the same names (unexported there): this package only
	// ever reads them.
	watchLockName   = ".watch.lock"
	watcherBeatFile = ".last-watcher-beat"

	// autoarmLockName is Task 11's lock: the Stop-owned auto-arm hook holds
	// it, with Session set to autoarmSession, while it works to restore the
	// watcher.
	autoarmLockName = ".claude-autoarm.lock"
	autoarmSession  = "autoarm"

	epochFile = ".claude-autoarm-epoch"

	notifiedFile = ".claude-autoarm-failure-notified"
	alarmedFile  = ".claude-autoarm-failure-alarmed"

	budgetFile     = ".turnend-claude-blocks"
	budgetLockName = ".turnend-claude-blocks.lock"
)

// Needed reports whether any *.meta file exists directly inside stateDir.
// Dot-prefixed names (locks, queues, temp artifacts) are excluded so a state
// file that happens to end in .meta is never miscounted as a task; this is
// Needed's own defensive filter, not a mirror of internal/watch's
// ScanSignals, which does no dot-prefix filtering of its own. The error is a
// third return rather than a swallowed false: the failure posture the
// turnend-guard hook applies treats an unlistable state directory
// differently than a genuinely empty one, and a two-value signature cannot
// tell a caller which happened. os.ReadDir is used rather than
// filepath.Glob specifically because Glob swallows the directory read error
// this return value exists to expose.
func Needed(stateDir string) (bool, string, error) {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return false, "", err
	}
	n := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasSuffix(name, ".meta") {
			n++
		}
	}
	if n == 0 {
		return false, "", nil
	}
	return true, fmt.Sprintf("%d task(s) in flight", n), nil
}

// WatcherHealthy reports whether stateDir's .watch.lock names a live holder
// AND .last-watcher-beat's mtime is younger than grace. Both conjuncts are
// required and neither substitutes for the other: lock.Info.Alive fails
// closed on an unverifiable process, so a lock record alone can read as
// live indefinitely; the beat is the real liveness evidence.
func WatcherHealthy(stateDir string, grace time.Duration) bool {
	holder, err := lock.ReadNamed(stateDir, watchLockName)
	if err != nil || !holder.Alive() {
		return false
	}
	fi, err := os.Stat(filepath.Join(stateDir, watcherBeatFile))
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < grace
}

// AutoarmOwnsRecovery reports whether the Stop-owned auto-arm (Task 11) is
// proving it is restoring the watcher, through exactly one of three proofs:
// a healthy watcher, a live .claude-autoarm.lock the auto-arm still holds
// with Session "autoarm", or a fresh rewake epoch stamped by a still-live
// owner.
//
// Two narrowings are load-bearing. First, a failed or failed-suppressed
// epoch is failure EVIDENCE, never proof: only an outcome of exactly
// "rewake" counts, so a stalled or failed recovery falls through to the
// caller's block-budget ladder instead of reading as healthy forever.
// Second, a live autoarm lock stops being proof once NotifiedOnce is true:
// otherwise the lock proof would stay true at every later Stop even after
// the auto-arm has already reported its own failure, the budget would never
// charge, and the block-budget alarm would never arm.
func AutoarmOwnsRecovery(stateDir string, grace, epochFresh time.Duration) bool {
	if WatcherHealthy(stateDir, grace) {
		return true
	}
	if holder, err := lock.ReadNamed(stateDir, autoarmLockName); err == nil {
		if holder.Session == autoarmSession && holder.Alive() && !NotifiedOnce(stateDir) {
			return true
		}
	}
	epoch, err := ReadEpoch(stateDir)
	if err != nil || epoch.Outcome != "rewake" {
		return false
	}
	if time.Since(epoch.UpdatedAt) >= epochFresh {
		return false
	}
	return pidAlive(epoch.OwnerPID)
}

// Epoch is one record from state/.claude-autoarm-epoch: a single line
// "epoch=<n> owner_pid=<pid> outcome=<o> updated_at=<unix>".
type Epoch struct {
	N         int
	OwnerPID  int
	Outcome   string
	UpdatedAt time.Time
}

func readEpochFile(stateDir string) (Epoch, error) {
	lines, err := fsx.ReadLines(filepath.Join(stateDir, epochFile))
	if errors.Is(err, os.ErrNotExist) {
		return Epoch{}, nil
	}
	if err != nil {
		return Epoch{}, err
	}
	if len(lines) == 0 {
		return Epoch{}, nil
	}
	var e Epoch
	for _, field := range strings.Fields(lines[0]) {
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "epoch":
			e.N, _ = strconv.Atoi(val)
		case "owner_pid":
			e.OwnerPID, _ = strconv.Atoi(val)
		case "outcome":
			e.Outcome = val
		case "updated_at":
			sec, _ := strconv.ParseInt(val, 10, 64)
			e.UpdatedAt = time.Unix(sec, 0).UTC()
		}
	}
	return e, nil
}

func writeEpochFile(stateDir string, e Epoch) error {
	line := fmt.Sprintf("epoch=%d owner_pid=%d outcome=%s updated_at=%d\n", e.N, e.OwnerPID, e.Outcome, e.UpdatedAt.Unix())
	return fsx.AtomicWriteFile(filepath.Join(stateDir, epochFile), []byte(line))
}

// ReadEpoch returns the current epoch ledger record, zero-valued if none has
// been written yet.
func ReadEpoch(stateDir string) (Epoch, error) {
	return readEpochFile(stateDir)
}

// NextEpoch increments the epoch ledger, recording outcome "arming" with the
// caller (os.Getpid()) as owner, and returns the new epoch number.
func NextEpoch(stateDir string) (int, error) {
	cur, err := readEpochFile(stateDir)
	if err != nil {
		return 0, err
	}
	next := cur.N + 1
	e := Epoch{N: next, OwnerPID: os.Getpid(), Outcome: "arming", UpdatedAt: time.Now().UTC()}
	if err := writeEpochFile(stateDir, e); err != nil {
		return 0, err
	}
	return next, nil
}

// ErrStaleEpoch reports that SetOutcome's epoch argument does not equal the
// ledger's current epoch number, so the write was refused. The check is a
// plain inequality, not "epoch is behind": a missing or never-written ledger
// reads as Epoch{} (N == 0), which also fails the equality check against any
// epoch a caller legitimately passes (NextEpoch always returns >= 1), so a
// SetOutcome call with no matching NextEpoch on record refuses the same way
// a genuinely superseded one does. Callers use errors.Is to distinguish this
// benign, expected outcome (the ledger moved on to a newer episode, or never
// had this one) from a real I/O failure that should escalate instead.
var ErrStaleEpoch = errors.New("supervise: epoch is not the ledger's current epoch")

// SetOutcome records outcome for epoch, refusing with ErrStaleEpoch when
// epoch does not equal the ledger's current epoch number, so the write can
// never clobber a newer episode's outcome. See ErrStaleEpoch for exactly
// which conditions trigger the refusal.
func SetOutcome(stateDir string, epoch int, outcome string) error {
	cur, err := readEpochFile(stateDir)
	if err != nil {
		return err
	}
	if cur.N != epoch {
		return fmt.Errorf("%w: got %d, ledger is at %d", ErrStaleEpoch, epoch, cur.N)
	}
	cur.Outcome = outcome
	cur.UpdatedAt = time.Now().UTC()
	return writeEpochFile(stateDir, cur)
}

// markFile create-exclusive touches an empty marker file. Idempotent: an
// already-existing marker is not an error.
func markFile(stateDir, name string) error {
	f, err := os.OpenFile(filepath.Join(stateDir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return f.Close()
}

func markerExists(stateDir, name string) bool {
	_, err := os.Stat(filepath.Join(stateDir, name))
	return err == nil
}

// MarkNotified records that a failure episode has already been reported.
func MarkNotified(stateDir string) error {
	return markFile(stateDir, notifiedFile)
}

// NotifiedOnce reports whether MarkNotified has been called for the current
// episode. Any read error, including a missing file, is false: an
// unprovable claim is not proof.
func NotifiedOnce(stateDir string) bool {
	return markerExists(stateDir, notifiedFile)
}

// MarkAlarm records that the block-budget alarm has fired for the current
// episode.
func MarkAlarm(stateDir string) error {
	return markFile(stateDir, alarmedFile)
}

// AlarmFired reports whether MarkAlarm has been called for the current
// episode. Any read error, including a missing file, is false.
func AlarmFired(stateDir string) bool {
	return markerExists(stateDir, alarmedFile)
}

// withBudgetLock holds state/.turnend-claude-blocks.lock as a lock.Info
// record through the Task 5 named-lock family for fn's duration, retried on
// lock.ErrHeld up to 10 times at 50ms. A dead holder is stolen by the lock
// package itself, so a process killed mid-charge cannot wedge the home.
func withBudgetLock(stateDir string, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := lock.AcquireNamedOwner(stateDir, budgetLockName, os.Getpid(), "budget"); err != nil {
			if errors.Is(err, lock.ErrHeld) {
				lastErr = err
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		defer lock.ReleaseNamed(stateDir, budgetLockName)
		return fn()
	}
	return lastErr
}

func readBudget(stateDir string) (session string, count int, err error) {
	lines, err := fsx.ReadLines(filepath.Join(stateDir, budgetFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	for _, line := range lines {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "session":
			session = val
		case "count":
			count, _ = strconv.Atoi(val)
		}
	}
	return session, count, nil
}

func writeBudget(stateDir, session string, count int) error {
	data := fmt.Sprintf("session=%s\ncount=%d\n", session, count)
	return fsx.AtomicWriteFile(filepath.Join(stateDir, budgetFile), []byte(data))
}

// ChargeBudget increments state/.turnend-claude-blocks's counter for
// session, resetting to 1 when the recorded session differs from a prior
// charge, and returns the new count.
func ChargeBudget(stateDir, session string) (int, error) {
	var count int
	err := withBudgetLock(stateDir, func() error {
		prevSession, prevCount, err := readBudget(stateDir)
		if err != nil {
			return err
		}
		if prevSession != session {
			prevCount = 0
		}
		count = prevCount + 1
		return writeBudget(stateDir, session, count)
	})
	return count, err
}

// removeWithRetry deletes path, retrying up to 10 times at 50ms on a
// transient Windows sharing violation (antivirus/indexer scans) - the same
// bounded-retry shape fsx.AtomicWriteFile uses for its rename. A missing
// file is success: there is nothing left to remove.
func removeWithRetry(path string) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		err := os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		lastErr = err
		time.Sleep(50 * time.Millisecond)
	}
	return lastErr
}

// ResetBudget removes both failure markers, then the budget record, as one
// group under the budget lock. Markers first, budget last: a crash (or a
// removal that exhausts its retries) partway through then always leaves
// NotifiedOnce false, so the turnend-guard hook's own "reset unless already
// notified" check (hook.go step 2) retries the whole group on the very next
// quiet Stop instead of getting stuck skipping it forever on a half-applied
// reset.
func ResetBudget(stateDir string) error {
	return withBudgetLock(stateDir, func() error {
		for _, name := range []string{notifiedFile, alarmedFile, budgetFile} {
			if err := removeWithRetry(filepath.Join(stateDir, name)); err != nil {
				return err
			}
		}
		return nil
	})
}
