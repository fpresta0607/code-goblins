// Package lock owns the home's session lock: one primary session may mutate
// fleet state. Identity is PID plus process creation time, replacing upstream's
// MSYS symlink lock and harness-ancestry walk.
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

// Info identifies a lock holder.
type Info struct {
	PID      int       `json:"pid"`
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

func selfInfo() *Info {
	start, _ := processStart(os.Getpid())
	hostname, _ := os.Hostname()
	return &Info{PID: os.Getpid(), Start: start, Hostname: hostname, Acquired: time.Now().UTC()}
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

// Acquire takes dir/.lock for the current process. A holder that is dead
// (PID gone, or PID recycled with a different creation time) is stolen.
// Strategy: exclusive create with read-back verification, grace period for
// mid-write files, and retry with exponential backoff on transient errors.
func Acquire(dir string) (*Info, error) {
	path := filepath.Join(dir, ".lock")
	self := selfInfo()
	unreadableCount := 0
	unreadableStart := time.Time{}

	for attempt := 0; attempt < 10; attempt++ {
		err := writeInfo(path, self)
		if err == nil {
			// Verify we won the race: read back the file and confirm it records us.
			if verified, verr := Read(dir); verr == nil && verified.PID == self.PID && verified.Start == self.Start {
				return self, nil
			}
			// We lost the race: another acquirer wrote after us. Continue the loop.
			continue
		}

		if !errors.Is(err, os.ErrExist) {
			// Transient error (e.g., sharing violation): sleep and retry.
			time.Sleep(50 * time.Millisecond)
			continue
		}

		// File exists; read the holder.
		holder, herr := Read(dir)
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

		if holder.Alive() {
			return nil, fmt.Errorf("%w: pid %d on %s since %s",
				ErrHeld, holder.PID, holder.Hostname, holder.Acquired.Format(time.RFC3339))
		}

		// Holder is dead. Re-read immediately to check if it changed
		// (another acquirer might have won the race and written a new holder).
		holder2, herr2 := Read(dir)
		if herr2 != nil {
			// Re-read is unreadable (file mid-write). Treat as transient and retry
			// using the same grace-period logic as first-read: never remove on an
			// unreadable re-read, even if we already judged the first read dead.
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

// Read returns the current holder recorded in dir/.lock.
func Read(dir string) (*Info, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("lock: unreadable holder record: %w", err)
	}
	return &info, nil
}

// Release removes the lock when the current process identity holds it.
func Release(dir string) error {
	holder, err := Read(dir)
	if err != nil {
		return err
	}
	self := selfInfo()
	localHostname, _ := os.Hostname()

	// Refuse if the holder's hostname differs from the local hostname.
	if holder.Hostname != "" && holder.Hostname != localHostname {
		return fmt.Errorf("lock: held by foreign host %s, not this process", holder.Hostname)
	}

	// Refuse if the holder's PID or process identity does not match this process.
	if holder.PID != self.PID || !holder.Alive() {
		return fmt.Errorf("lock: held by pid %d, not this process", holder.PID)
	}

	return os.Remove(filepath.Join(dir, ".lock"))
}
