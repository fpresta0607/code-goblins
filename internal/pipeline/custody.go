package pipeline

import (
	"errors"
	"sync"
)

// CustodyCache keeps each worktree's last custody verdict for the newest run
// of its branch, so a custody read the board repeats starts the native check
// once per change of that run rather than once per read. A nil CustodyCache
// keeps nothing and checks every read.
//
// On 2026-10-28 an open terminal view of a goblin whose newest run was
// cancelled before it pushed asked for custody every five seconds, and every
// ask started `no-mistakes axi sync --check` and the update check it starts in
// turn: two processes a read, and a send waited on them (#1678).
type CustodyCache struct {
	mu       sync.Mutex
	verdicts map[custodyKey]custodyVerdict
}

type custodyKey struct {
	worktree, branch string
}

// custodyVerdict is what the check answered for one record of the run. The
// native engine reads custody of an unpublished finished run from its record
// alone, so the same record has the same answer.
type custodyVerdict struct {
	run              recoveryRecord
	terminalVerified int64
	err              error
}

func NewCustodyCache() *CustodyCache {
	return &CustodyCache{verdicts: map[custodyKey]custodyVerdict{}}
}

// read is the verdict for run on worktree's branch, from check when this
// record of the run has none kept. Only an answer is kept: the run let go or
// still holds the branch. A check that could not be read or an anchor that
// could not be proved is asked again on the next read.
func (c *CustodyCache) read(worktree string, run recoveryRecord, terminalVerified int64, check func() error) error {
	if c == nil {
		return check()
	}
	key := custodyKey{worktree: worktree, branch: run.Branch}
	c.mu.Lock()
	kept, isKept := c.verdicts[key]
	c.mu.Unlock()
	if isKept && kept.run == run && kept.terminalVerified == terminalVerified {
		return kept.err
	}
	err := check()
	if err == nil || errors.Is(err, ErrCustodyNotReturned) {
		c.mu.Lock()
		c.verdicts[key] = custodyVerdict{run: run, terminalVerified: terminalVerified, err: err}
		c.mu.Unlock()
	}
	return err
}
