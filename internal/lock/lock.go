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
func (i *Info) Alive() bool {
	start, ok := processStart(i.PID)
	if !ok {
		return false
	}
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
func Acquire(dir string) (*Info, error) {
	path := filepath.Join(dir, ".lock")
	self := selfInfo()
	for attempt := 0; attempt < 3; attempt++ {
		err := writeInfo(path, self)
		if err == nil {
			return self, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		holder, herr := Read(dir)
		if herr == nil && holder.Alive() {
			return nil, fmt.Errorf("%w: pid %d on %s since %s",
				ErrHeld, holder.PID, holder.Hostname, holder.Acquired.Format(time.RFC3339))
		}
		// Dead or unreadable holder: clear the stale file and retry the
		// exclusive create. Losing that race to a concurrent acquirer is
		// correct behavior, not an error.
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, rerr
		}
	}
	return nil, errors.New("lock: lost the create race three times")
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
	if holder.PID != self.PID || !holder.Alive() {
		return fmt.Errorf("lock: held by pid %d, not this process", holder.PID)
	}
	return os.Remove(filepath.Join(dir, ".lock"))
}
