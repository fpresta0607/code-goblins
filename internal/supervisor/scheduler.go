package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func (s *Service) schedule(ctx context.Context, now time.Time, memory Memory, watched *fleetWakes) error {
	if s.Options.Dispatch.Spawn == nil || memory.shortfall() != "" {
		return nil
	}
	s.starts.Lock()
	isChanging := s.starting != "" || len(s.changing) > 0
	s.starts.Unlock()
	if isChanging {
		return nil
	}
	capacity, err := ReadFleetCapacity(s.Store.Home, memory)
	if err != nil {
		return err
	}
	if capacity.Slots == 0 {
		return nil
	}
	queued, _ := memoryWork(s.Store.Home)
	queued = slices.DeleteFunc(queued, func(id string) bool {
		plan, err := planStart(s.Store.Home, id)
		if err != nil {
			return true
		}
		return allowanceBlocked(watched, plan.harness, plan.model, now)
	})
	backlog, err := fleet.ReadBacklog(s.Store.Home)
	if err != nil {
		return err
	}
	for _, row := range backlog.Queued {
		if row.Priority == "production-defect" && slices.Contains(queued, row.ID) {
			return s.startQueued(row.ID, false)
		}
	}
	var problems error
	var ready []state.Lifecycle
	isMemoryPending := false
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
		if isReady {
			ready = append(ready, record)
		} else if record.Pause.Reason == "memory" {
			isMemoryPending = true
		}
	}
	slices.SortFunc(ready, func(left, right state.Lifecycle) int {
		if order := left.Pause.At.Compare(right.Pause.At); order != 0 {
			return order
		}
		return strings.Compare(left.ID, right.ID)
	})
	if len(ready) > 0 {
		return errors.Join(problems, s.resumeAutomatically(ready[0]))
	}
	if len(queued) > 0 && !isMemoryPending {
		return errors.Join(problems, s.startQueued(queued[0], false))
	}
	return problems
}

func (s *Service) pauseCleared(ctx context.Context, condition state.PauseCondition, now time.Time, watched *fleetWakes) (bool, error) {
	switch condition.Reason {
	case "memory":
		return watched.MemoryAbove >= 2, nil
	case "allowance":
		at, err := time.Parse(time.RFC3339, condition.Until)
		return err == nil && !now.Before(at), err
	case "dependency":
		kind, target, _ := strings.Cut(condition.Until, ":")
		switch kind {
		case "date":
			at, err := time.Parse(time.RFC3339, target)
			return err == nil && !now.Before(at), err
		case "task":
			outcome, err := state.ReadOutcome(s.Store.Home.State, target)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return err == nil && outcome.Phase == "done", err
		case "pr":
			if s.Options.PullRequestState == nil {
				return false, nil
			}
			probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
			defer cancel()
			pull, err := s.Options.PullRequestState(probe, target)
			return err == nil && pull.State == "MERGED", err
		}
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
		output, err := s.Options.Dispatch.Spawn(context.Background(), []string{"resume", record.ID, "--generation", record.Generation, "--operation", operation, "--reason", "Pause condition cleared"})
		s.starts.Lock()
		delete(s.changing, record.ID)
		if err != nil {
			s.changeErrors[record.ID] = taskChangeError{Message: spawnFailure(output, err), Generation: record.Generation, Operation: record.Operation, Updated: record.Updated}
		}
		s.starts.Unlock()
		s.notify()
	}()
	s.notify()
	return nil
}
