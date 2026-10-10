// Package custody takes the home's session lock for the CFO's own session,
// and tells the CFO when that took it from another live session.
package custody

import (
	"fmt"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// Take takes stateDir's session lock for ownerPID, the harness of the CFO's
// own session, which the caller has proven it is. The lock is that session's
// alone, so a live holder that is another process holds it wrongly and loses
// it: the takeover is recorded in state\custody.audit with why, and one check
// wake tells the CFO, since whatever that holder did as the CFO is in no
// transcript of the CFO's. It returns the holder it replaced, nil when it
// replaced none.
func Take(stateDir string, ownerPID int, session, why string) (*lock.Info, *lock.Info, error) {
	holder, replaced, err := lock.SeizeOwner(stateDir, ownerPID, session, why)
	if err != nil || replaced == nil {
		return holder, nil, err
	}
	// The audit holds the takeover already, and every caller says it took the
	// lock over where it has a reader, so a wake queue that cannot be written
	// leaves the takeover recorded and said.
	identity := fmt.Sprintf("custody/%d/%d/%d", holder.PID, replaced.PID, replaced.Acquired.UnixNano())
	if _, err := wake.AppendOnce(stateDir, identity, "check", "custody", Notice(*replaced)); err == nil {
		_, _ = wake.PublishEpisode(stateDir)
	}
	return holder, replaced, nil
}

// Notice is what the CFO is told when its session took the session lock over
// from replaced.
func Notice(replaced lock.Info) string {
	session := "no conversation recorded"
	if replaced.Session != "" {
		session = "conversation " + replaced.Session
	}
	return fmt.Sprintf("custody: this session, the CFO's own, took the home's session lock over from pid %d, a session that is not the CFO's own (%s, holding the lock since %s). What that session did as the CFO is in its transcript, not in this one. state\\%s records the takeover. Tell the Overlord which session it was.",
		replaced.PID, session, replaced.Acquired.UTC().Format(time.RFC3339), lock.AuditFile)
}
