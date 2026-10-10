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
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/update"
)

type allowanceFloor struct {
	IsLow bool      `json:"is_low"`
	Reset time.Time `json:"reset,omitzero"`
}

// AllowanceReset says whether provider's weekly allowance for model is at
// floor, the percent of it the home keeps back, and when the last week at it
// resets. A floor of 0 keeps nothing back: the week runs until the provider
// refuses, which usedUpReset waits out as it does a used-up session.
func AllowanceReset(report quota.Report, provider, model string, floor float64, now time.Time) (time.Time, bool) {
	reading, ok := report.Providers[provider]
	if floor <= 0 || !ok || reading.Stale || !reading.Known {
		return time.Time{}, false
	}
	scope := boundScope(reading, model)
	var reset time.Time
	isLow := false
	for _, window := range reading.Windows {
		if !isWeekly(window) || !slices.Contains(scope.BoundedBy, window.ID) || window.PercentUsed < 100-floor {
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

// isWeekly says whether window measures a week, the account's or a model's.
func isWeekly(window quota.Window) bool {
	return window.Kind == "weekly" || window.Kind == "model" && window.WindowSeconds == 7*24*60*60
}

// usedUpReset is when a used-up window that bounds provider's model scope
// frees again: a session, which is never kept as a reserve, or a week, which
// a floor of 0 keeps none of. Once it is used up, nothing starts or resumes
// on it until it renews, and the goblins it stopped are waited out rather
// than paused.
func usedUpReset(report quota.Report, provider, model string, now time.Time) (time.Time, bool) {
	reading, ok := report.Providers[provider]
	if !ok || reading.Stale || !reading.Known {
		return time.Time{}, false
	}
	scope := boundScope(reading, model)
	var reset time.Time
	for _, window := range reading.Windows {
		if (window.Kind == "session" || isWeekly(window)) && window.PercentUsed >= 100 && slices.Contains(scope.BoundedBy, window.ID) && window.ResetsAt.After(now) && window.ResetsAt.After(reset) {
			reset = window.ResetsAt
		}
	}
	return reset, !reset.IsZero()
}

// boundScope is the scope a goblin on model draws from: the model's own when
// quota-axi measures one, otherwise every model's.
func boundScope(reading quota.Provider, model string) quota.Scope {
	scope, ok := reading.Scopes["model:"+model]
	if !ok || model == "" {
		scope = reading.Scopes["all_models"]
	}
	return scope
}

func allowanceBlocked(watched *fleetWakes, provider, model string, now time.Time) bool {
	floor, ok := watched.AllowanceFloors[provider+"/model:"+model]
	if !ok || model == "" {
		floor = watched.AllowanceFloors[provider+"/all_models"]
	}
	return floor.IsLow && (floor.Reset.IsZero() || now.Before(floor.Reset))
}

// pauseAtAllowanceFloor is the fleet reading's allowance pass: it reads each
// provider's weekly floor from config/fleet.json and keeps it for the dials,
// holds starts and resumes on a provider at its floor or with a window used
// up, and pauses the goblins running on a provider at its floor. A floor that
// cannot be read pauses nothing, and the board says why, while a window used
// up is still waited out.
func (s *Service) pauseAtAllowanceFloor(ctx context.Context, watched *fleetWakes, now time.Time) error {
	watched.AllowanceFloors = map[string]allowanceFloor{}
	settings, problems := fleetconfig.Read(s.Store.Home.Root)
	isFloorKnown := problems == nil
	var floors map[string]float64
	if isFloorKnown {
		floors = map[string]float64{}
		for _, provider := range fleetconfig.WeeklyFloorProviders {
			floors[provider] = settings.WeeklyFloor(provider)
		}
	} else {
		problems = fmt.Errorf("the weekly allowance floor cannot be read, so no goblin pauses at it: %w", problems)
	}
	s.mu.Lock()
	s.weeklyFloors = floors
	s.mu.Unlock()
	report, skipped := quota.Report{}, "no quota reader"
	if s.Options.Quota != nil {
		report, skipped = s.readQuota(ctx, 20*time.Second)
	}
	if skipped != "" {
		// A window seen used up still wakes the CFO when it renews.
		return errors.Join(problems, s.raiseAllowanceWakes(quota.Report{}, watched, now))
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
			reset, isLow := time.Time{}, false
			if isFloorKnown {
				reset, isLow = AllowanceReset(report, provider, model, settings.WeeklyFloor(provider), now)
			}
			if !isLow {
				reset, isLow = usedUpReset(report, provider, model, now)
			}
			watched.AllowanceFloors[provider+"/"+name] = allowanceFloor{IsLow: isLow, Reset: reset}
		}
	}
	problems = errors.Join(problems, s.raiseAllowanceWakes(report, watched, now))
	if !isFloorKnown || s.Options.Dispatch == nil || s.Options.Dispatch.Spawn == nil {
		return problems
	}
	for _, meta := range liveTasks(s.Store.Home.State) {
		floor := settings.WeeklyFloor(meta.Harness)
		reset, isLow := AllowanceReset(report, meta.Harness, meta.Model, floor, now)
		if !isLow {
			continue
		}
		if reset.IsZero() {
			problems = errors.Join(problems, fmt.Errorf("%s allowance is at its %v percent weekly floor without a reset time, so no pause condition can be recorded", meta.Harness, floor))
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
		evidence := fmt.Sprintf("%s's weekly allowance is at its %v percent floor until it resets at %s", meta.Harness, floor, reset.UTC().Format(time.RFC3339))
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
	// The pause is refused while an update installs, and tried again at the
	// floor's next reading.
	if update.Installing(s.Store.Home.State) {
		return false, nil
	}
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
