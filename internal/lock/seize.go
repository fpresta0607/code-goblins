package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// AuditFile is the file in a lock's directory that records every time one of
// its locks was taken from a live holder, one JSON record a line.
const AuditFile = "custody.audit"

// takeoverTurn is the lock that lets one takeover at a time read a holder,
// record it and replace it, and takeoverWait how long a takeover waits for
// its turn: each holds it for a few file operations.
const (
	takeoverTurn = ".custody.lock"
	takeoverWait = 10 * time.Second
)

// Takeover is one record of AuditFile: lock Lock was taken from From, a live
// holder, for To, at At, by the command Why names.
type Takeover struct {
	At   time.Time `json:"at"`
	Lock string    `json:"lock"`
	From Info      `json:"from"`
	To   Info      `json:"to"`
	Why  string    `json:"why"`
}

// SeizeOwner takes dir/.lock for ownerPID as AcquireOwner does, except that a
// live holder that is another process loses it. Only a caller that has proven
// ownerPID is the CFO's own session calls it: the session lock is that
// session's, so whoever else holds it holds it wrongly. It returns the holder
// it replaced, nil when it replaced none. See SeizeNamedOwner.
func SeizeOwner(dir string, ownerPID int, session, why string) (*Info, *Info, error) {
	return SeizeNamedOwner(dir, ".lock", ownerPID, session, why, func(*Info) bool { return true })
}

// SeizeNamedOwner takes dir/name for ownerPID as AcquireNamedOwner does, and
// where another live process holds it and isReplaceable says that holder may
// be replaced, takes it from that holder and returns the holder. The takeover
// is appended to dir/AuditFile, with why, before the holder's record is
// removed, and a takeover that cannot be recorded is not made. A holder that
// may not be replaced keeps the lock and is ErrHeld, as it is to
// AcquireNamedOwner.
func SeizeNamedOwner(dir, name string, ownerPID int, session, why string, isReplaceable func(holder *Info) bool) (*Info, *Info, error) {
	info, err := AcquireNamedOwner(dir, name, ownerPID, session)
	if !errors.Is(err, ErrHeld) {
		return info, nil, err
	}
	release, err := awaitTakeoverTurn(dir, name)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	path := filepath.Join(dir, name)
	var replaced *Info
	for attempt := 0; attempt < acquireAttempts; attempt++ {
		// Another takeover for this owner may have had the turn before this one.
		info, err := AcquireNamedOwner(dir, name, ownerPID, session)
		if !errors.Is(err, ErrHeld) {
			return info, replaced, err
		}
		holder, err := ReadNamed(dir, name)
		if err != nil || !holder.Alive() {
			// The lock is changing hands, or its holder ended since the
			// refusal: the next attempt takes it as any acquisition does.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if !isReplaceable(holder) {
			return nil, replaced, heldError(holder)
		}
		// A record a reader held open is still there on the next attempt, and
		// its takeover is already recorded.
		if replaced == nil || replaced.PID != holder.PID || !replaced.Start.Equal(holder.Start) || !replaced.Acquired.Equal(holder.Acquired) {
			self, _ := ownerInfo(ownerPID, session)
			if err := appendTakeover(dir, Takeover{At: self.Acquired, Lock: name, From: *holder, To: *self, Why: why}); err != nil {
				return nil, replaced, fmt.Errorf("lock: %s is held by pid %d, and the takeover could not be recorded in %s, so it was not made: %w", name, holder.PID, AuditFile, err)
			}
		}
		replaced = holder
		if err := removeStale(path, attempt); err != nil {
			return nil, replaced, err
		}
	}
	return nil, replaced, fmt.Errorf("lock: failed to take %s over after %d attempts", name, acquireAttempts)
}

// awaitTakeoverTurn waits for dir's turn to take a lock over and returns how
// to give the turn back.
func awaitTakeoverTurn(dir, name string) (func(), error) {
	for deadline := time.Now().Add(takeoverWait); ; time.Sleep(25 * time.Millisecond) {
		_, err := AcquireExclusiveNamedFor(dir, takeoverTurn, "the takeover of "+name)
		if err == nil {
			return func() { _ = ReleaseExclusiveNamed(dir, takeoverTurn) }, nil
		}
		if !errors.Is(err, ErrHeld) || time.Now().After(deadline) {
			return nil, fmt.Errorf("lock: no turn to take %s over: %w", name, err)
		}
	}
}

func appendTakeover(dir string, record Takeover) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := fsx.OpenAppend(filepath.Join(dir, AuditFile), 0o600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	return errors.Join(err, file.Close())
}
