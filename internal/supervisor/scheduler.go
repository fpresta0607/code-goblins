package supervisor

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// Scheduling is what the scheduler made of its last reading with memory
// free, for the board's memory meter and the CFO's idle wake: Text says what
// it started or resumed, else why waiting work did not start, and is empty
// when nothing waits. Waiting is the work that could run now and did not,
// each with why.
type Scheduling struct {
	At      time.Time     `json:"at"`
	Text    string        `json:"text"`
	Waiting []WaitingWork `json:"waiting,omitempty"`
}

// WaitingWork is a queued task or a paused goblin that could run now and did
// not start, and why.
type WaitingWork struct {
	ID  string `json:"id"`
	Why string `json:"why"`
}

// schedule takes one free slot, in the fleet's order: a reported production
// defect, then the oldest paused goblin whose pause cleared, then the top of
// the queue, unless a goblin paused for memory comes back first. It says what
// it did, and what could run and did not, with why; a board that cannot start
// goblins schedules nothing and says nothing.
func (s *Service) schedule(ctx context.Context, now time.Time, watched *fleetWakes, memory Memory) (*Scheduling, error) {
	if s.Options.Dispatch.Spawn == nil {
		return nil, nil
	}
	record := &Scheduling{At: now}
	// A pull request a queued row waits on that merged lets the row start
	// at this reading. One that cannot be read holds its own row only, and
	// the board says why, so the rest of the reading and its wakes go on.
	if err := s.learnAwaitedPulls(ctx); err != nil {
		s.publish(err)
	}
	reading := s.rowReading(now, &memory)
	s.starts.Lock()
	starting, changing := s.starting, maps.Clone(s.changing)
	failed, changeErrors := maps.Clone(s.startErrors), maps.Clone(s.changeErrors)
	asked := slices.Clone(s.asked)
	s.starts.Unlock()
	finished := s.finishedWork()
	var queued []string
	defect := ""
	for _, id := range queuedCandidates(s.Store.Home) {
		plan, err := planStart(s.Store.Home, id, finished, reading)
		var refusal StartRefusal
		if errors.As(err, &refusal) && refusal.Held {
			continue
		}
		// A queued task whose last start failed waits for a Start, so it
		// neither holds the slot nor fails again every reading.
		if failure, isFailed := failed[id]; isFailed {
			record.Waiting = append(record.Waiting, WaitingWork{ID: id, Why: "its last start failed: " + failure})
			continue
		}
		if err != nil {
			record.Waiting = append(record.Waiting, WaitingWork{ID: id, Why: err.Error()})
			continue
		}
		if allowanceBlocked(watched, plan.harness, plan.model, now) {
			continue
		}
		queued = append(queued, id)
		if plan.isProductionDefect && defect == "" {
			defect = id
		}
	}
	problems := tellRetiredRows(s.Store.Home.State, finished.retired)
	var ready []state.Lifecycle
	memoryPending := ""
	for _, meta := range liveTasks(s.Store.Home.State) {
		if allowanceBlocked(watched, meta.Harness, meta.Model, now) {
			continue
		}
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if record.Phase != "paused" || record.Generation != meta.SpawnGen || record.Pause == nil {
			continue
		}
		isReady, err := s.pauseCleared(ctx, *record.Pause, now, watched)
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("pause condition for %s: %w", meta.ID, err))
			continue
		}
		isWithParent, err := s.resumesWithItsParent(meta, record)
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("parent of %s: %w", meta.ID, err))
			continue
		}
		isReady = isReady || isWithParent
		if isReady {
			ready = append(ready, record)
		} else if record.Pause.Reason == "memory" && memoryPending == "" {
			memoryPending = meta.ID
		}
	}
	slices.SortFunc(ready, func(left, right state.Lifecycle) int {
		if order := left.Pause.At.Compare(right.Pause.At); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	for _, paused := range ready {
		if failure, isFailed := changeErrors[paused.ID]; isFailed && failure.Generation == paused.Generation {
			record.Waiting = append(record.Waiting, WaitingWork{ID: paused.ID, Why: "its last resume failed: " + failure.Message})
		}
	}
	// What would start by itself waits while the performance cores are busy.
	// A reported production defect does not, nor what the Overlord started.
	waitsOnProcessors, busy := "", ""
	switch {
	case len(ready) > 0:
		waitsOnProcessors = ready[0].ID
	case len(queued) > 0 && memoryPending == "":
		waitsOnProcessors = queued[0]
	}
	if waitsOnProcessors != "" && starting == "" && len(changing) == 0 && len(asked) == 0 && defect == "" {
		busy = s.processorsBusy()
	}
	switch {
	case starting != "":
		record.Text = starting + " is starting"
	case len(changing) > 0:
		id := slices.Sorted(maps.Keys(changing))[0]
		record.Text = id + " is " + changingVerbs[changing[id]]
	case len(asked) > 0:
		record.Text = "starting " + asked[0].task + ", which the Overlord started"
		if asked[0].resume != nil {
			record.Text = "resuming " + asked[0].task + ", which the Overlord resumed"
		}
		s.runAsked()
	case defect != "":
		record.Text = "starting " + defect + ", a reported production defect"
		problems = errors.Join(problems, s.startQueued(defect, false))
	case busy != "":
		record.Waiting = append(record.Waiting, WaitingWork{ID: waitsOnProcessors, Why: busy})
		record.Text = "nothing starts: " + bounded(busy, 120)
	case len(ready) > 0:
		record.Text = "resuming " + ready[0].ID
		problems = errors.Join(problems, s.resumeAutomatically(ready[0]))
	case len(queued) > 0 && memoryPending == "":
		record.Text = "starting " + queued[0]
		problems = errors.Join(problems, s.startQueued(queued[0], false))
	case memoryPending != "":
		record.Text = memoryPending + " resumes first, once memory reads 5 GB twice"
	case len(record.Waiting) > 0:
		// The whole reason is on the task's card; the meter's line is short.
		record.Text = "nothing starts: " + record.Waiting[0].ID + ": " + bounded(record.Waiting[0].Why, 120)
		if len(record.Waiting) > 1 {
			record.Text += fmt.Sprintf(" (and %d more)", len(record.Waiting)-1)
		}
	default:
		// Nothing waits, so the board says nothing under its meters, as the
		// Overlord asked on 2026-10-08.
		record.Text = ""
	}
	var refusal StartRefusal
	if errors.As(problems, &refusal) {
		record.Text = "nothing starts: " + bounded(refusal.Reason, 120)
	}
	return record, problems
}

// changingVerbs says what a task in the middle of a change is doing.
var changingVerbs = map[string]string{"resume": "resuming", "switch": "switching", "pause": "pausing", "stop": "stopping", "save": "being edited", "note": "being edited"}

func (s *Service) pauseCleared(ctx context.Context, condition state.PauseCondition, now time.Time, watched *fleetWakes) (bool, error) {
	switch condition.Reason {
	case "memory":
		return watched.MemoryAbove >= 2, nil
	case "allowance":
		return pauseClearedHere(s.Store.Home.State, condition, now)
	case "dependency":
		kind, target, _ := strings.Cut(condition.Until, ":")
		if kind != "pr" {
			return pauseClearedHere(s.Store.Home.State, condition, now)
		}
		if s.Options.PullRequestState == nil {
			return false, nil
		}
		probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
		defer cancel()
		pull, err := s.Options.PullRequestState(probe, target)
		return err == nil && pull.State == "MERGED", err
	case "question":
		for _, question := range s.Store.Snapshot().Questions {
			if question.ID == condition.Until {
				return question.AnsweredBy == "overlord" && question.AnsweredAt != nil && question.Status == "succeeded", nil
			}
		}
	case "ci", "deploy":
		wait, head, _ := strings.Cut(condition.Until, "@")
		_, target, _ := strings.Cut(wait, ":")
		completed := watched.Checks[target]
		isCurrent := !completed.ReportedAt.Before(condition.At)
		return strings.HasPrefix(completed.Signature, head+"|") && !completed.ReportedAt.IsZero() && isCurrent, nil
	case "overlord":
		return false, nil
	}
	return false, nil
}

func (s *Service) resumeAutomatically(record state.Lifecycle) error {
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.starting != "" || len(s.changing) > 0 {
		return nil
	}
	current, err := state.ReadLifecycle(s.Store.Home.State, record.ID)
	if err != nil {
		return err
	}
	if current.Phase != "paused" || current.Operation != record.Operation || current.Generation != record.Generation {
		return nil
	}
	if s.changing == nil {
		s.changing = map[string]string{}
		s.changeErrors = map[string]taskChangeError{}
	}
	s.changing[record.ID] = "resume"
	delete(s.changeErrors, record.ID)
	operation := fmt.Sprintf("auto-resume-%d", time.Now().UnixNano())
	go func() {
		launched := make(chan struct{})
		go s.watchLaunch(record.ID, launched)
		output, err := s.runPastTheSpawnLock(s.Options.Dispatch, []string{"resume", record.ID, "--generation", record.Generation, "--operation", operation, "--reason", "Pause condition cleared"})
		close(launched)
		s.starts.Lock()
		delete(s.changing, record.ID)
		if err != nil {
			s.changeErrors[record.ID] = taskChangeError{Message: spawnFailure(output, err), Generation: record.Generation, Operation: record.Operation, Updated: record.Updated}
		}
		s.starts.Unlock()
		s.notify()
		s.runAsked()
	}()
	s.notify()
	return nil
}
