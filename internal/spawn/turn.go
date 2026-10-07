package spawn

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// launchTurnWait bounds how long a start waits for another start's turn
// before it gives up and names the start it waited on. A turn covers only
// what must be serial, which takes seconds; a start that holds it this long
// is stuck, and the one waiting on it says so rather than waiting on.
var launchTurnWait = 10 * time.Minute

// launchTurnPoll is how often a start waiting for its turn looks again.
const launchTurnPoll = 500 * time.Millisecond

// takeLaunchTurn takes the home's spawn lock for purpose, such as "the start
// of pp-open-work", which the lock's record keeps so a start that finds it
// held can say what it waits on. A start holds its turn only while it does
// what must be serial, and ends it with endTurn once its terminal's host
// runs; endTurn releases the lock once, however often it is called, and
// returns that release's error every time. A start that finds the lock held
// waits for its turn, says once on Progress what it waits on, and past
// launchTurnWait gives up naming the start that holds it.
func (s Service) takeLaunchTurn(ctx context.Context, purpose string) (endTurn func() error, err error) {
	deadline := time.Now().Add(launchTurnWait)
	isAnnounced := false
	for {
		_, err := lock.AcquireExclusiveNamedFor(s.StateDir, spawnLockName, purpose)
		if err == nil {
			return sync.OnceValue(func() error {
				if err := s.releaseTaskLock(s.StateDir, spawnLockName); err != nil {
					return fmt.Errorf("spawn: release spawn lock: %w", err)
				}
				return nil
			}), nil
		}
		if !errors.Is(err, lock.ErrHeld) {
			return nil, fmt.Errorf("spawn: acquire spawn lock: %w", err)
		}
		holder := s.turnHolder()
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("spawn: %s waited %s for its turn and stopped: %s; try again once that one is up: %w", purpose, launchTurnWait, holder, err)
		}
		if !isAnnounced && s.Progress != nil {
			fmt.Fprintf(s.Progress, "spawn: %s waits for its turn: %s\n", purpose, holder)
			isAnnounced = true
		}
		timer := time.NewTimer(launchTurnPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// turnHolder says what holds the home's spawn lock and since when, as its
// record says.
func (s Service) turnHolder() string {
	holder, err := lock.ReadNamed(s.StateDir, spawnLockName)
	if err != nil {
		return "another start holds the home's spawn lock"
	}
	what := holder.Purpose
	if what == "" {
		what = fmt.Sprintf("process %d", holder.PID)
	}
	return fmt.Sprintf("%s has held the home's spawn lock since %s", what, holder.Acquired.Format(time.RFC3339))
}
