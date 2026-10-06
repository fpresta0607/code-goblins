package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
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
	var reset time.Time
	isLow := false
	for _, window := range reading.Windows {
		isWeekly := window.Kind == "weekly" || window.Kind == "model" && window.WindowSeconds == 7*24*60*60
		if !isWeekly || !slices.Contains(scope.BoundedBy, window.ID) || window.PercentUsed < 100-5 {
			continue
		}
		if !window.ResetsAt.IsZero() && !now.Before(window.ResetsAt) {
			continue
		}
		isLow = true
		if window.ResetsAt.IsZero() {
			return time.Time{}, true
		}
		if window.ResetsAt.After(reset) {
			reset = window.ResetsAt
		}
	}
	return reset, isLow
}

func allowanceBlocked(watched *fleetWakes, provider, model string, now time.Time) bool {
	floor, ok := watched.AllowanceFloors[provider+"/model:"+model]
	if !ok || model == "" {
		floor = watched.AllowanceFloors[provider+"/all_models"]
	}
	return floor.IsLow && (floor.Reset.IsZero() || now.Before(floor.Reset))
}

func (s *Service) pauseAtAllowanceFloor(ctx context.Context, watched *fleetWakes, now time.Time) error {
	watched.AllowanceFloors = map[string]allowanceFloor{}
	if s.Options.Quota == nil || s.Options.Dispatch == nil || s.Options.Dispatch.Spawn == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	report, skipped := s.Options.Quota(probe)
	cancel()
	if skipped != "" {
		return nil
	}
	for provider, reading := range report.Providers {
		if reading.Stale || !reading.Known {
			continue
		}
		for name := range reading.Scopes {
			model := strings.TrimPrefix(name, "model:")
			if name == "all_models" {
				model = ""
			}
			reset, isLow := AllowanceReset(report, provider, model, now)
			watched.AllowanceFloors[provider+"/"+name] = allowanceFloor{IsLow: isLow, Reset: reset}
		}
	}
	var problems error
	for _, meta := range liveTasks(s.Store.Home.State) {
		reset, isLow := AllowanceReset(report, meta.Harness, meta.Model, now)
		if !isLow {
			continue
		}
		if reset.IsZero() {
			problems = errors.Join(problems, fmt.Errorf("%s allowance is at the 5 percent weekly floor without a reset time; no pause condition can be recorded", meta.Harness))
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
		evidence := fmt.Sprintf("%s's weekly allowance is at the 5 percent floor until it resets at %s", meta.Harness, reset.UTC().Format(time.RFC3339))
		if _, err := s.pauseAtFloor(meta, record, "allowance", reset.UTC().Format(time.RFC3339), evidence); err != nil {
			problems = errors.Join(problems, err)
		}
	}
	return problems
}

// pauseAtFloor pauses meta's goblin for a floor in the background, through
// the lifecycle as the Overlord's own Pause does: reason is the floor's pause
// reason and until its resume condition, empty for none. While AFK mode is
// on its log keeps the pause with evidence, what it stands on, and then with
// how it went, for the report of the stretch. It begins nothing while a start
// or another change of the goblin is under way, and says whether it began.
func (s *Service) pauseAtFloor(meta state.TaskMeta, prior state.Lifecycle, reason, until, evidence string) (bool, error) {
	s.starts.Lock()
	if s.starting == meta.ID || s.changing[meta.ID] != "" {
		s.starts.Unlock()
		return false, nil
	}
	if s.changing == nil {
		s.changing = map[string]string{}
		s.changeErrors = map[string]taskChangeError{}
	}
	s.changing[meta.ID] = "pause"
	s.starts.Unlock()
	what := "at the " + reason + " floor"
	logged := s.logFloorPause(afk.Entry{Task: meta.ID, What: what, Evidence: evidence})
	go func() {
		args := []string{"pause", meta.ID, "--generation", meta.SpawnGen, "--operation", fmt.Sprintf("%s-pause-%d", reason, time.Now().UnixNano()), "--reason", reason}
		if until != "" {
			args = append(args, "--until", until)
		}
		output, err := s.Options.Dispatch.Spawn(context.Background(), args)
		outcome := "paused"
		if err != nil {
			outcome = "the pause failed: " + spawnFailure(output, err)
		}
		unlogged := s.logFloorPause(afk.Entry{Task: meta.ID, What: what, Outcome: outcome})
		problem := ""
		switch {
		case err != nil:
			problem = spawnFailure(output, err)
		case unlogged != nil:
			problem = "paused at the " + reason + " floor, but " + unlogged.Error()
		}
		s.starts.Lock()
		delete(s.changing, meta.ID)
		if problem != "" {
			s.changeErrors[meta.ID] = taskChangeError{Message: problem, Generation: meta.SpawnGen, Operation: prior.Operation, Updated: prior.Updated}
		}
		s.starts.Unlock()
		s.notify()
	}()
	return true, logged
}

// logFloorPause keeps a line of a pause at a floor in AFK mode's log while it
// is on; while it is off there is no stretch to keep it in.
func (s *Service) logFloorPause(entry afk.Entry) error {
	s.afkChange.Lock()
	defer s.afkChange.Unlock()
	if err := afk.Pause(s.Store.Home.State, entry, time.Now()); err != nil && !errors.Is(err, afk.ErrNotOn) {
		return fmt.Errorf("AFK mode's log did not take the pause of %s %s: %w", entry.Task, entry.What, err)
	}
	return nil
}
