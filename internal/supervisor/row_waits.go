package supervisor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// rowReading is what a queued row's waits are read against: the time, the
// memory reading, and the state the scheduler last read of each pull request
// a row waits on. A reading with no memory leaves a wait for memory to the
// start that reads it, as a Start does in its turn.
type rowReading struct {
	now    time.Time
	memory *Memory
	pulls  map[string]string
}

// rowReading reads queued rows' waits at now against memory, with the pull
// request states the scheduler last read.
func (s *Service) rowReading(now time.Time, memory *Memory) rowReading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rowReading{now: now, memory: memory, pulls: maps.Clone(s.awaitedPulls)}
}

// rowWaits is what a queued row still waits for at reading: each blocker on
// it that has not cleared. A time clears once it passes, free memory once the
// reading has that much memory and commit free, or with no memory read, a
// task once its outcome says it delivered and a pull request once it merged.
// A blocker the scheduler cannot read never clears and says why, and so does
// one that can never clear, which would otherwise hold its row unseen: a
// task the home never heard of or that stopped without delivering, and a pull
// request closed without merging.
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
			switch reading.pulls[blocker.Target] {
			case "MERGED":
				continue
			case "CLOSED":
				blocker.Problem = "PR #" + path.Base(blocker.Target) + " closed without merging"
			}
		case blocker.Kind == "task":
			switch awaitedTask(h, backlog, blocker.Target) {
			case "done":
				continue
			case "stopped":
				blocker.Problem = blocker.Target + " stopped without delivering"
			case "":
				blocker.Problem = `No task is named "` + blocker.Target + `"`
			}
		}
		waits = append(waits, blocker)
	}
	return waits
}

// awaitedTask is where the task id stands for a row that waits on it: its
// outcome's phase, done or stopped, else "known" for a task the home knows by
// a row in the backlog, a brief or a live record, else "" for none.
func awaitedTask(h home.Home, backlog fleet.BacklogRows, id string) string {
	if outcome, err := state.ReadOutcome(h.State, id); err == nil {
		return outcome.Phase
	}
	if backlog.Lists(id) {
		return "known"
	}
	for _, file := range []string{filepath.Join(h.Data, id, "brief.md"), filepath.Join(h.State, id+".meta")} {
		if _, err := os.Stat(file); err == nil {
			return "known"
		}
	}
	return ""
}

// waitRefusal is why a row with waits does not start: the first wait the
// scheduler cannot read, which the CFO must fix, or else what it waits for,
// which clears by itself and holds the row.
func waitRefusal(id string, row fleet.BacklogRow, waits []fleet.Blocker) StartRefusal {
	targets := make([]string, len(waits))
	for index, wait := range waits {
		if wait.Problem != "" {
			return StartRefusal{Reason: wait.Problem, Waits: true}
		}
		targets[index] = wait.Target
	}
	reason := id + " waits for " + strings.Join(targets, ", ")
	if row.BlockedReason != "" {
		reason += ": " + row.BlockedReason
	}
	return StartRefusal{Reason: reason, Held: true, Waits: true}
}

// learnAwaitedPulls asks the forge about each pull request a queued row
// waits on and keeps what it said, so a row whose pull request merged starts
// with no edit to it and one closed without merging says so. A merged pull
// request stays merged and is not asked about again.
func (s *Service) learnAwaitedPulls(ctx context.Context) error {
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
			isMerged := s.awaitedPulls[blocker.Target] == "MERGED"
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
			s.mu.Lock()
			if s.awaitedPulls == nil {
				s.awaitedPulls = map[string]string{}
			}
			s.awaitedPulls[blocker.Target] = pull.State
			s.mu.Unlock()
		}
	}
	return problems
}
