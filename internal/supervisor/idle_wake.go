package supervisor

import (
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// idleWakeAfter is how long work that could run may wait with memory free
// and nothing starting before the CFO is woken about it, and woken again for
// as long as that lasts. On 2026-10-07 memory_ready woke the CFO once, the
// work it named could not start, and the fleet idled four hours.
const idleWakeAfter = 30 * time.Minute

// wakeWhenStalled wakes the CFO as idle once work the scheduler could not
// start has waited idleWakeAfter with memory free and nothing started since,
// and again each idleWakeAfter that lasts, naming each waiting task and why it
// did not start. A reading with nothing waiting stops the clock.
func (s *Service) wakeWhenStalled(w *fleetWakes, record *Scheduling, memory Memory, now time.Time) error {
	if record == nil || len(record.Waiting) == 0 {
		w.IdleSince = time.Time{}
		return nil
	}
	if w.IdleSince.IsZero() {
		w.IdleSince = now
	}
	since := w.IdleSince
	if started := lastStart(s.Store.Home.State); started.After(since) {
		since = started
	}
	if now.Sub(since) < idleWakeAfter || !w.due("idle", idleWakeAfter, now) {
		return nil
	}
	waiting := make([]string, 0, len(record.Waiting))
	for _, work := range record.Waiting {
		waiting = append(waiting, work.ID+": "+work.Why)
	}
	detail := fmt.Sprintf("idle: nothing has started for %d minutes with %.1f GB of memory and %.1f GB of commit free while this work waits; %s; next: fix what stops each, then start it with Start on the board or cfo spawn, or park its row if it must not start",
		int(now.Sub(since)/time.Minute), gigabytes(memory.Available), gigabytes(memory.CommitAvailable), strings.Join(waiting, "; "))
	if err := raiseFleetWake(s.Store.Home.State, "idle", "fleet", detail); err != nil {
		return err
	}
	w.woke("idle", now)
	return nil
}

// setScheduling keeps what the scheduler made of a reading, or that it did
// not schedule, and tells the board when its line changes.
func (s *Service) setScheduling(record *Scheduling) {
	s.mu.Lock()
	isChanged := (record == nil) != (s.scheduling == nil) || record != nil && record.Text != s.scheduling.Text
	s.scheduling = record
	s.mu.Unlock()
	if isChanged {
		s.notify()
	}
}

// lastStart is when the newest goblin now at work started or resumed.
func lastStart(stateDir string) time.Time {
	var newest time.Time
	for _, meta := range liveTasks(stateDir) {
		at := spawnTime(meta.SpawnGen)
		if record, err := state.ReadLifecycle(stateDir, meta.ID); err == nil && record.Generation == meta.SpawnGen {
			if record.Phase == "paused" || record.Phase == "stopped" {
				continue
			}
			if record.Action == "resume" && record.Updated.After(at) {
				at = record.Updated
			}
		}
		if at.After(newest) {
			newest = at
		}
	}
	return newest
}
