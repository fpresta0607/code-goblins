package supervisor

import (
	"context"
	"slices"
	"strings"
	"time"
)

const (
	// errorWakeGap is the least time between two wakes for the supervisor's
	// own errors; a new line the gap holds waits for it and is never dropped.
	errorWakeGap = 5 * time.Minute
	// errorRemembered is how long a line of the supervisor's errors stays
	// known after it was last met, so one that stays, or comes back with each
	// cycle, wakes once, and one gone that long is new again.
	errorRemembered = time.Hour
	// timeoutHeld is how long a read must keep running out of time before
	// the CFO is told of it, and how far apart two such reads may be and
	// still be one run of them.
	timeoutHeld = 5 * time.Minute
)

// timeoutRun is one read's run of passes that ran out of time: when the run
// began and when the read last ran out of time.
type timeoutRun struct {
	began, met time.Time
}

// wakeForNewErrors tells the CFO, as one check wake keyed supervisor, each
// line of err it was not told of within errorRemembered. The board is no
// place for them: on 2026-10-07 a failure the page sweep met every ten
// minutes filled it with a box the Overlord could do nothing about. A wake
// the queue does not take keeps its lines for the next publish to tell, and
// its own failure stays off the board: the queue lives in the state folder,
// whose lasting failure the store's own storage failure reports.
//
// A read that ran out of time is made again on the next pass, so one that
// did so once is nothing to tell: on 2026-10-10, with every processor busy,
// three goblins' progress reads and a pull request's read each ran out of
// time once and woke the CFO twice in minutes. Such a line is told once the
// read has kept running out of time for timeoutHeld.
func (s *Service) wakeForNewErrors(err error, now time.Time) {
	s.mu.Lock()
	if s.errorLines == nil {
		s.errorLines, s.timeouts = map[string]time.Time{}, map[string]timeoutRun{}
	}
	for line, seen := range s.errorLines {
		if now.Sub(seen) >= errorRemembered {
			delete(s.errorLines, line)
		}
	}
	for line, run := range s.timeouts {
		if now.Sub(run.met) > timeoutHeld {
			delete(s.timeouts, line)
		}
	}
	if err != nil {
		for _, line := range strings.Split(err.Error(), "\n") {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			if strings.Contains(line, context.DeadlineExceeded.Error()) {
				run, isRunning := s.timeouts[line]
				if !isRunning {
					run.began = now
				}
				run.met = now
				s.timeouts[line] = run
				if now.Sub(run.began) < timeoutHeld {
					continue
				}
			}
			if _, isKnown := s.errorLines[line]; !isKnown && !slices.Contains(s.errorsUntold, line) {
				s.errorsUntold = append(s.errorsUntold, line)
			}
			s.errorLines[line] = now
		}
	}
	untold := s.errorsUntold
	if len(untold) == 0 || now.Sub(s.errorsWoke) < errorWakeGap {
		s.mu.Unlock()
		return
	}
	s.errorsUntold, s.errorsWoke = nil, now
	s.mu.Unlock()
	if err := raiseFleetWake(s.Store.Home.State, "check", "supervisor", "supervisor_error: "+bounded(strings.Join(untold, "; "), 2000)); err != nil {
		s.mu.Lock()
		s.errorsUntold, s.errorsWoke = append(untold, s.errorsUntold...), time.Time{}
		s.mu.Unlock()
	}
}
