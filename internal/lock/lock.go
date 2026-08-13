// Package lock owns the home's session lock: one primary session may mutate
// fleet state. The lock records CUSTODY of a long-lived owner process (the
// harness), not the calling process; identity is the owner's PID plus its
// process creation time, replacing upstream's MSYS symlink lock and
// harness-ancestry walk.
package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrHeld reports that a live process holds the session lock.
var ErrHeld = errors.New("session lock held by a live process")

// ErrOwnerDead reports that the process asked to take custody is verifiably gone.
var ErrOwnerDead = errors.New("lock: owner process is not alive")

// Info identifies a lock holder: the owner process in custody of the lock
// (the harness ancestor, or the calling process for a plain Acquire) plus,
// where known, the Claude session id currently running under that owner.
type Info struct {
	PID      int       `json:"pid"`
	OwnerPID int       `json:"owner_pid"`
	Session  string    `json:"session"`
	Start    time.Time `json:"start"`
	Hostname string    `json:"hostname"`
	Acquired time.Time `json:"acquired"`
}

// Alive reports whether the holder's process still runs. FILETIME rounding
// means creation times can differ by a tick, so match within one second.
// Fail closed: if the holder cannot be verified (foreign hostname, unknown process state),
// return true (assume alive) to avoid stealing a lock that might be legitimately held.
func (i *Info) Alive() bool {
	localHostname, _ := os.Hostname()
	// If the record has a hostname and it differs from the local hostname,
	// we cannot verify it and must fail closed (assume alive).
	if i.Hostname != "" && i.Hostname != localHostname {
		return true
	}

	start, status := processStart(i.PID)
	// If status is unknown (e.g., access denied), fail closed: assume alive.
	if status == statusUnknown {
		return true
	}
	// If status is dead, the process is gone.
	if status == statusDead {
		return false
	}
	// status is alive; check creation time match (within 1 second).
	diff := start.Sub(i.Start)
	return diff > -time.Second && diff < time.Second
}

// ownerInfo builds the Info record for pid, the process taking custody of
// the lock (the harness ancestor for a hook-driven acquire, or the calling
// process itself for a plain Acquire), with session recorded verbatim. The
// returned status is processStart's verdict on pid, for callers that must
// refuse a verifiably dead owner before writing a record for it.
func ownerInfo(pid int, session string) (*Info, processStatus) {
	start, status := processStart(pid)
	hostname, _ := os.Hostname()
	return &Info{PID: pid, OwnerPID: pid, Session: session, Start: start, Hostname: hostname, Acquired: time.Now().UTC()}, status
}

func writeInfo(path string, info *Info) error {
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Close())
}

// AcquireNamedOwner takes dir/name for ownerPID, the long-lived process (the
// harness ancestor, or a subsystem such as the wake queue) taking custody of
// the lock, recording session as the Claude session currently running under
// that owner. It is AcquireOwner generalized to a caller-chosen lock file
// instead of the fixed ".lock", so unrelated locks (session custody, the
// wake queue) can coexist in the same directory. See AcquireOwner for the
// full acquisition semantics.
func AcquireNamedOwner(dir, name string, ownerPID int, session string) (*Info, error) {
	self, status := ownerInfo(ownerPID, session)
	if status == statusDead {
		return nil, fmt.Errorf("%w: pid %d", ErrOwnerDead, ownerPID)
	}
	return acquire(dir, name, self, true)
}

// AcquireExclusiveNamed takes dir/name for the current process without the
// ordinary session-lock re-acquisition exception. A task spawn needs this
// stricter form because two concurrent Spawn calls run under one process but
// must still contend for the task's creation lock.
func AcquireExclusiveNamed(dir, name string) (*Info, error) {
	self, status := ownerInfo(os.Getpid(), "")
	if status == statusDead {
		return nil, fmt.Errorf("%w: pid %d", ErrOwnerDead, self.PID)
	}
	return acquire(dir, name, self, false)
}

// AcquireOwner takes dir/.lock for ownerPID, the long-lived process (the
// harness ancestor) taking custody of the lock, recording session as the
// Claude session currently running under that owner. A holder that is dead
// (PID gone, or PID recycled with a different creation time) is stolen. A
// holder that already matches ownerPID's identity is treated as
// already-acquired (idempotent re-acquire) regardless of session, never as
// contention: a resumed session keeps custody.
// A verifiably dead ownerPID is refused with ErrOwnerDead before any file is
// created or touched; an unverifiable ownerPID (statusUnknown) still
// proceeds, fail closed to alive exactly as Alive() does.
// Strategy: exclusive create with read-back verification, grace period for
// mid-write files, and retry with a constant backoff on transient errors.
func AcquireOwner(dir string, ownerPID int, session string) (*Info, error) {
	return AcquireNamedOwner(dir, ".lock", ownerPID, session)
}

// Acquire takes dir/.lock for the current process, recording no session.
// The calling process cannot be dead, so it never hits AcquireOwner's
// ErrOwnerDead branch. See AcquireOwner for the full acquisition semantics.
func Acquire(dir string) (*Info, error) {
	return AcquireOwner(dir, os.Getpid(), "")
}

// acquire runs the exclusive-create/read-back/steal loop, recording self as
// the new holder of dir/name if the lock is free or its dead holder can be
// stolen. allowReacquire preserves the session-lock custody contract while
// task-spawn locks require a same-process concurrent caller to contend.
func acquire(dir, name string, self *Info, allowReacquire bool) (*Info, error) {
	path := filepath.Join(dir, name)
	unreadableCount := 0
	unreadableStart := time.Time{}

	for attempt := 0; attempt < 10; attempt++ {
		err := writeInfo(path, self)
		if err == nil {
			// Verify we won the race: read back the file and confirm it records us.
			if verified, verr := ReadNamed(dir, name); verr == nil && verified.PID == self.PID && verified.Start.Equal(self.Start) {
				return self, nil
			}
			// Either we lost the race (another acquirer wrote after us) or the
			// read-back failed transiently. Continue the loop either way: the
			// self-match check below resolves the transient case once the file
			// becomes readable again on a later iteration.
			continue
		}

		if !errors.Is(err, os.ErrExist) {
			// Transient error (e.g., sharing violation): sleep and retry.
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// File exists; read the holder.
		holder, herr := ReadNamed(dir, name)
		if herr != nil {
			// File is unreadable, corrupt, or zero-byte (holder mid-write).
			// Treat as transient: sleep and retry, but only after seeing
			// 3+ consecutive unreadable reads spanning 150ms+.
			if unreadableCount == 0 {
				unreadableStart = time.Now()
			}
			unreadableCount++

			if unreadableCount >= 3 && time.Since(unreadableStart) >= 150*time.Millisecond {
				// Grace period elapsed; treat as crash orphan.
				if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
					return nil, rerr
				}
				unreadableCount = 0
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// File is readable; reset unreadable counter.
		unreadableCount = 0

		// A normal session lock is re-entrant for its owner. An exclusive task
		// lock instead recognizes only the exact record this invocation wrote,
		// preserving a transient failed read-back without admitting another
		// concurrent call from the same process.
		if holder.PID == self.PID && holder.Start.Equal(self.Start) && holder.Hostname == self.Hostname {
			if allowReacquire || holder.Acquired.Equal(self.Acquired) {
				return self, nil
			}
		}

		if holder.Alive() {
			return nil, fmt.Errorf("%w: pid %d on %s since %s",
				ErrHeld, holder.PID, holder.Hostname, holder.Acquired.Format(time.RFC3339))
		}

		// Holder is dead. Re-read immediately to check if it changed
		// (another acquirer might have won the race and written a new holder).
		holder2, herr2 := ReadNamed(dir, name)
		if herr2 != nil {
			// Re-read is unreadable (file mid-write). Never remove on an
			// unreadable re-read, even though we already judged the first read
			// dead; the increment here only seeds the first-read grace counter
			// for later iterations.
			if unreadableCount == 0 {
				unreadableStart = time.Now()
			}
			unreadableCount++
			time.Sleep(50 * time.Millisecond)
			continue
		}

		if holder2.PID != holder.PID || holder2.Start != holder.Start {
			// The file changed; continue without removing (let the race winner handle cleanup).
			continue
		}

		// File still has the same dead holder; it is readable and unchanged.
		// Safe to remove and retry the create.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, rerr
		}
	}

	return nil, errors.New("lock: failed to acquire after 10 attempts")
}

// ReadNamed returns the current holder recorded in dir/name.
func ReadNamed(dir, name string) (*Info, error) {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("lock: unreadable holder record: %w", err)
	}
	return &info, nil
}

// Read returns the current holder recorded in dir/.lock.
func Read(dir string) (*Info, error) {
	return ReadNamed(dir, ".lock")
}

// HeldByNamed reports whether dir/name currently records ownerPID as its
// holder: the hostname matches, the PID matches, and ownerPID's live
// creation time matches the recorded Start within the same one-second
// tolerance Alive uses. Any read error, including a missing lock file,
// returns false. Fail closed applies here too: if ownerPID's process state
// cannot be verified (statusUnknown), Alive() returns true without checking
// Start, so an unverifiable owner reads as held and the recycled-PID
// defense is inert in that branch.
func HeldByNamed(dir, name string, ownerPID int) bool {
	holder, err := ReadNamed(dir, name)
	if err != nil {
		return false
	}
	localHostname, _ := os.Hostname()
	if holder.Hostname != localHostname || holder.PID != ownerPID {
		return false
	}
	return holder.Alive()
}

// HeldBy reports whether dir/.lock currently records ownerPID as its
// holder. See HeldByNamed for the full semantics.
func HeldBy(dir string, ownerPID int) bool {
	return HeldByNamed(dir, ".lock", ownerPID)
}

// ReleaseNamed removes dir/name when the current process identity holds it.
func ReleaseNamed(dir, name string) error {
	return releaseNamed(dir, name, os.Remove, time.Sleep)
}

func releaseNamed(dir, name string, remove func(string) error, sleep func(time.Duration)) error {
	path := filepath.Join(dir, name)
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		holder, err := ReadNamed(dir, name)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err == nil {
			self, _ := ownerInfo(os.Getpid(), "")
			localHostname, _ := os.Hostname()

			// Refuse if the holder's hostname differs from the local hostname.
			if holder.Hostname != "" && holder.Hostname != localHostname {
				return fmt.Errorf("lock: held by foreign host %s, not this process", holder.Hostname)
			}

			// Refuse if the holder's PID or process identity does not match this process.
			if holder.PID != self.PID || !holder.Alive() {
				return fmt.Errorf("lock: held by pid %d, not this process", holder.PID)
			}

			err = remove(path)
			if err == nil || errors.Is(err, os.ErrNotExist) {
				return nil
			}
		}
		lastErr = err
		if attempt < 9 {
			sleep(50 * time.Millisecond)
		}
	}
	return fmt.Errorf("lock: release %s after 10 attempts: %w", name, lastErr)
}

// Release removes dir/.lock when the current process identity holds it. See
// ReleaseNamed for the full semantics.
func Release(dir string) error {
	return ReleaseNamed(dir, ".lock")
}
