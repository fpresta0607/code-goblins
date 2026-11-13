package lock

import (
	"errors"
	"io/fs"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// processStart returns pid's creation time and status. A process /proc no
// longer lists, or one that has exited and only waits for its parent to
// collect it, is statusDead. Any other failure to read it is statusUnknown,
// which Alive must treat as alive (fail closed: cannot steal what cannot be
// verified).
func processStart(pid int) (time.Time, processStatus) {
	entry, err := proc.Lookup(pid)
	switch {
	case err == nil:
		return entry.Start, statusAlive
	case errors.Is(err, fs.ErrNotExist):
		return time.Time{}, statusDead
	default:
		return time.Time{}, statusUnknown
	}
}
