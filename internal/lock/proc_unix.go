//go:build linux || darwin

package lock

import (
	"errors"
	"io/fs"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

func init() { processInstance = readInstance }

// processStart returns pid's creation time and status, as statusOf reads
// what the system said of it.
func processStart(pid int) (time.Time, processStatus) {
	entry, err := proc.Lookup(pid)
	return entry.Start, statusOf(err)
}

// readInstance returns which run of its program pid is (proc.Instance) and
// status, as statusOf reads what the system said of it. It has no clock in
// it, so it is what a record or a ticket knows its process by.
func readInstance(pid int) (string, processStatus) {
	instance, err := proc.Instance(pid)
	return instance, statusOf(err)
}

// statusOf is the status of a process whose reading ended in err. A process
// the system no longer lists, or one that has exited and only waits for
// its parent to collect it, is statusDead. Any other failure to read it is
// statusUnknown, which Alive must treat as alive (fail closed: cannot steal
// what cannot be verified).
func statusOf(err error) processStatus {
	switch {
	case err == nil:
		return statusAlive
	case errors.Is(err, fs.ErrNotExist):
		return statusDead
	default:
		return statusUnknown
	}
}
