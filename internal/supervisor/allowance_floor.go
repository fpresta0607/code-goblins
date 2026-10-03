package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type allowanceFloor struct {
	IsLow bool      `json:"is_low"`
	Reset time.Time `json:"reset,omitzero"`
}

func AllowanceReset(report quota.Report, provider, model string, now time.Time) (time.Time, bool) {
	reading, ok := report.Providers[provider]
	if !ok || reading.Stale || !reading.Known {
		return time.Time{}, false
	}
	scope, ok := reading.Scopes["model:"+model]
	if !ok || model == "" {
		scope = reading.Scopes["all_models"]
	}
	isLow := scope.Known && scope.PercentRemaining <= 3 && (scope.ResetsAt.IsZero() || now.Before(scope.ResetsAt))
	return scope.ResetsAt, isLow
}

func allowanceBlocked(watched *fleetWakes, provider, model string, now time.Time) bool {
	floor, ok := watched.AllowanceFloors[provider+"/model:"+model]
	if !ok || model == "" {
		floor = watched.AllowanceFloors[provider+"/all_models"]
	}
	return floor.IsLow && (floor.Reset.IsZero() || now.Before(floor.Reset))
}

func (s *Service) pauseAtAllowanceFloor(ctx context.Context, watched *fleetWakes, now time.Time) error {
	if s.Options.Quota == nil || s.Options.Dispatch == nil || s.Options.Dispatch.Spawn == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	report, skipped := s.Options.Quota(probe)
	cancel()
	if skipped != "" {
		return nil
	}
	watched.AllowanceFloors = map[string]allowanceFloor{}
	for provider, reading := range report.Providers {
		if reading.Stale || !reading.Known {
			continue
		}
		for name, scope := range reading.Scopes {
			watched.AllowanceFloors[provider+"/"+name] = allowanceFloor{IsLow: scope.Known && scope.PercentRemaining <= 3, Reset: scope.ResetsAt}
		}
	}
	var problems error
	for _, meta := range liveTasks(s.Store.Home.State) {
		reset, isLow := AllowanceReset(report, meta.Harness, meta.Model, now)
		if !isLow {
			continue
		}
		if reset.IsZero() {
			problems = errors.Join(problems, fmt.Errorf("%s allowance is at the 3 percent floor without a reset time; no pause condition can be recorded", meta.Harness))
			continue
		}
		if meta.Backend != "native" {
			continue
		}
		hostRecord, err := host.ReadRecord(s.Store.Home.State, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if !host.Running(hostRecord) {
			continue
		}
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		if record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			continue
		}
		if record.Generation == meta.SpawnGen && record.Action == "pause" && record.Phase == "failed" {
			if record.Pause != nil && record.Pause.Reason == "allowance" && record.Pause.Until == reset.UTC().Format(time.RFC3339) {
				continue
			}
		}
		s.starts.Lock()
		if s.starting == meta.ID || s.changing[meta.ID] != "" {
			s.starts.Unlock()
			continue
		}
		if s.changing == nil {
			s.changing = map[string]string{}
			s.changeErrors = map[string]taskChangeError{}
		}
		s.changing[meta.ID] = "pause"
		s.starts.Unlock()
		go s.runAllowancePause(meta, record, reset)
	}
	return problems
}

func (s *Service) runAllowancePause(meta state.TaskMeta, prior state.Lifecycle, reset time.Time) {
	operation := fmt.Sprintf("allowance-pause-%d", time.Now().UnixNano())
	output, err := s.Options.Dispatch.Spawn(context.Background(), []string{"pause", meta.ID, "--generation", meta.SpawnGen, "--operation", operation, "--reason", "allowance", "--until", reset.UTC().Format(time.RFC3339)})
	s.starts.Lock()
	delete(s.changing, meta.ID)
	if err != nil {
		s.changeErrors[meta.ID] = taskChangeError{Message: spawnFailure(output, err), Generation: meta.SpawnGen, Operation: prior.Operation, Updated: prior.Updated}
	}
	s.starts.Unlock()
	s.notify()
}
