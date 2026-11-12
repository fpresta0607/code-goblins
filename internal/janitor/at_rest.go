package janitor

import (
	"time"

	"github.com/fpresta0607/code-goblins/internal/crewstate"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// statusTail is how many of a task's last status lines are read for its
// latest report, past the records the CEO writes into the log after it.
const statusTail = 50

// AtRestSince is when task id's goblin was last seen at work, once it has
// delivered and rests by what the home records, and zero while it works or
// waits. A goblin rests when both hold: the last outcome it reported is done
// or failed, and the monitor reads it at its prompt with no turn in progress
// and nothing asked. The time is the monitor's own, when it last saw the
// goblin make progress. Anything else is a goblin at work, and so is
// whatever cannot be read: a goblin that reported working, blocked or
// waiting-on, one in a turn however long, one whose harness is refused or
// still starting, and one that asks the CEO something in prose. A done
// report is one delivery's, and the home records none of the waits of a
// goblin that went on to the next, which its tree shows (IsWaiting).
func AtRestSince(stateDir, id string) time.Time {
	lines, err := state.TailStatus(stateDir, id, statusTail)
	if err != nil {
		return time.Time{}
	}
	if verb, isReported := crewstate.LatestVerb(lines); !isReported || verb != "done" && verb != "failed" {
		return time.Time{}
	}
	observation, err := monitor.ReadObservation(stateDir, id)
	if err != nil {
		return time.Time{}
	}
	switch {
	case observation.Health == monitor.HealthIdle:
	case observation.Health == monitor.HealthStale && (observation.Reason == monitor.UnchangedIdle || observation.Reason == monitor.AwaitingAnswer || observation.Reason == monitor.GoblinIdle):
	default:
		return time.Time{}
	}
	return observation.LastProgress
}

// IsWaiting says tree shows its goblin waiting by design on work of its own
// at now: a sub-agent, background shell, monitor or run in the machine's
// heavy line it started (fleettree.Tree.Waits), with something new under it
// within the busy budget the monitor gives such a wait. A task can ship
// several pull requests under one id, so a goblin that reported one done and
// waits so on the next is at work.
func IsWaiting(tree fleettree.Tree, now time.Time) bool {
	return len(tree.Waits()) > 0 && now.Sub(workedAt(tree)) < monitor.DefaultBusyTurnMax
}

// workedAt is the latest sign of work tree shows: its own records' activity
// or the processor use of the jobs that are its goblin's work.
func workedAt(tree fleettree.Tree) time.Time {
	return later(tree.ActivityAt(), tree.Busy())
}
