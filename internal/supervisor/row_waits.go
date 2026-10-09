package supervisor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// rowReading is what a queued row's waits are read against: the time, the
// memory reading, and the pull requests the scheduler saw merged. A reading
// with no memory leaves a wait for memory to the start that reads it, as a
// Start does in its turn.
type rowReading struct {
	now    time.Time
	memory *Memory
	merged map[string]bool
}

// rowReading reads queued rows' waits at now against memory, with the pull
// requests the scheduler saw merged.
func (s *Service) rowReading(now time.Time, memory *Memory) rowReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowReading{now: now, memory: memory, merged: maps.Clone(s.awaitedMerges)}
}

// rowWaits is what a queued row still waits for at reading: each blocker on
// it that has not cleared. A time clears once it passes, free memory once the
// reading has that much memory and commit free, or with no memory read, a
// task once its outcome says it delivered and a pull request once it merged. A blocker the scheduler
// cannot read never clears and says why, and so does a task the home never
// heard of, which would otherwise wait forever.
func rowWaits(h home.Home, backlog fleet.BacklogRows, row fleet.BacklogRow, reading rowReading) []fleet.Blocker {
	var waits []fleet.Blocker
	for _, blocker := range row.Blockers {
		switch {
		case blocker.Problem != "":
		case blocker.Kind == "time":
			if !reading.now.Before(blocker.Until) {
				continue
			}
		case blocker.Kind == "memory":
			if reading.memory == nil || min(reading.memory.Available, reading.memory.CommitAvailable) >= blocker.Bytes {
				continue
			}
		case blocker.Kind == "pr":
			if reading.merged[blocker.Target] {
				continue
			}
		case blocker.Kind == "task":
			isDelivered, isKnown := awaitedTask(h, backlog, blocker.Target)
			if isDelivered {
				continue
			}
			if !isKnown {
				blocker.Problem = `No task is named "` + blocker.Target + `"`
			}
		}
		waits = append(waits, blocker)
	}
	return waits
}

// awaitedTask says whether the task id delivered, as its outcome says, and
// whether the home knows a task by that id at all: one with an outcome, a row
// in the backlog, a brief or a live record.
func awaitedTask(h home.Home, backlog fleet.BacklogRows, id string) (isDelivered, isKnown bool) {
	if outcome, err := state.ReadOutcome(h.State, id); err == nil {
		return outcome.Phase == "done", true
	}
	if backlog.Lists(id) {
		return false, true
	}
	for _, path := range []string{filepath.Join(h.Data, id, "brief.md"), filepath.Join(h.State, id+".meta")} {
		if _, err := os.Stat(path); err == nil {
			return false, true
		}
	}
	return false, false
}

// waitRefusal is why a row with waits does not start: the first wait the
// scheduler cannot read, which the CFO must fix, or else what it waits for,
// which clears by itself and holds the row.
func waitRefusal(id string, row fleet.BacklogRow, waits []fleet.Blocker) StartRefusal {
	targets := make([]string, len(waits))
	for index, wait := range waits {
		if wait.Problem != "" {
			return StartRefusal{Reason: wait.Problem}
		}
		targets[index] = wait.Target
	}
	reason := id + " waits for " + strings.Join(targets, ", ")
	if row.BlockedReason != "" {
		reason += ": " + row.BlockedReason
	}
	return StartRefusal{Reason: reason, Held: true}
}

// learnAwaitedMerges asks the forge about each pull request a queued row
// waits on and keeps those that merged, which stay merged, so a row whose
// pull request merged starts with no edit to it.
func (s *Service) learnAwaitedMerges(ctx context.Context) error {
	if s.Options.PullRequestState == nil {
		return nil
	}
	backlog, err := fleet.ReadBacklog(s.Store.Home)
	if err != nil {
		return err
	}
	var problems error
	for _, row := range backlog.Queued {
		for _, blocker := range row.Blockers {
			s.mu.Lock()
			isMerged := s.awaitedMerges[blocker.Target]
			s.mu.Unlock()
			if blocker.Kind != "pr" || blocker.Problem != "" || isMerged {
				continue
			}
			probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
			pull, err := s.Options.PullRequestState(probe, blocker.Target)
			cancel()
			if err != nil {
				problems = errors.Join(problems, fmt.Errorf("the pull request %s waits on, %s: %w", row.ID, blocker.Target, err))
				continue
			}
			if pull.State == "MERGED" {
				s.mu.Lock()
				if s.awaitedMerges == nil {
					s.awaitedMerges = map[string]bool{}
				}
				s.awaitedMerges[blocker.Target] = true
				s.mu.Unlock()
			}
		}
	}
	return problems
}
