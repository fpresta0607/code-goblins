package supervisor

import (
	"context"
	"maps"
	"time"

	"github.com/fpresta0607/code-goblins/internal/quota"
)

type SubscriptionUsage struct {
	Provider string `json:"provider"`
	quota.WeeklyReading
	FloorPercent *float64 `json:"floor_percent"`
}

// keepSubscriptionUsage reads what the dials show, each source on its own
// clock and only when the supervisor made no read of it for a minute and a
// half: every read refreshes the dials, and the fleet's reading reads quota
// once a minute for the allowance floor. On 2026-10-08 both read in the same
// second each minute, Anthropic's usage endpoint refused one of the two, and
// the dial showed a reading a minute old as stale. The half minute over keeps
// a fleet reading that runs late from bringing the dials' own read beside it.
func (s *Service) keepSubscriptionUsage(ctx context.Context, every time.Duration) {
	if s.Options.Quota == nil && s.Options.KeySpend == nil {
		return
	}
	isDue := func(at time.Time) bool { return at.IsZero() || time.Since(at) >= every*3/2 }
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		lastQuota, lastSpend := s.quotaReadAt, s.keySpendReadAt
		s.mu.Unlock()
		if s.Options.Quota != nil && isDue(lastQuota) {
			s.readQuota(ctx, 15*time.Second)
		}
		if s.Options.KeySpend != nil && isDue(lastSpend) {
			s.readKeySpends(ctx, 10*time.Second)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// keepSubscriptionReadings takes the dials' readings from one quota-axi read.
// A measured week replaces a provider's reading, and so does a sign-in the
// provider asks for. A read that measured nothing, rate limited or failed,
// leaves the reading before it, which subscriptionUsage shows stale once it
// is older than quota.MaxAge.
func (s *Service) keepSubscriptionReadings(report quota.Report, skipped string) {
	now := time.Now().UTC()
	s.mu.Lock()
	if s.subscriptionReadings == nil {
		s.subscriptionReadings = map[string]quota.WeeklyReading{}
	}
	for _, provider := range []string{"claude", "codex"} {
		reading := report.Weekly(provider, now)
		if skipped != "" && reading.Status == "available" {
			reading.Status, reading.PercentRemaining = "unavailable", nil
		}
		if s.subscriptionReadings[provider].Status == "available" && reading.Status != "available" && reading.Status != "auth_required" {
			continue
		}
		s.subscriptionReadings[provider] = reading
	}
	s.mu.Unlock()
	s.notify()
}

// subscriptionUsage is the dials' reading of each provider the CFO or a
// goblin runs on now, with the weekly floor the last fleet reading read for
// it, the one the supervisor paused by, or none while it could not be read.
func (s *Service) subscriptionUsage(cfo cfoState, snapshot Snapshot) []SubscriptionUsage {
	active := activeHarnesses(cfo, snapshot)
	s.mu.Lock()
	readings, floors := maps.Clone(s.subscriptionReadings), maps.Clone(s.weeklyFloors)
	s.mu.Unlock()
	usage := []SubscriptionUsage{}
	for _, provider := range []string{"claude", "codex"} {
		if !active[provider] {
			continue
		}
		reading, exists := readings[provider]
		if !exists {
			reading.Status = "unavailable"
		}
		if reading.Status == "available" && (snapshot.At.Sub(reading.ReadAt) > quota.MaxAge || !reading.ResetsAt.IsZero() && !reading.ResetsAt.After(snapshot.At)) {
			reading.Status, reading.PercentRemaining = "stale", nil
		}
		shown := SubscriptionUsage{Provider: provider, WeeklyReading: reading}
		if floor, isRead := floors[provider]; isRead {
			shown.FloorPercent = &floor
		}
		usage = append(usage, shown)
	}
	return usage
}

// activeHarnesses is each harness the CFO or a goblin runs on now, by name:
// the dials show a provider only while one of them runs on it.
func activeHarnesses(cfo cfoState, snapshot Snapshot) map[string]bool {
	active := map[string]bool{}
	if cfo.registered && cfo.problem == "" && snapshot.Registration == "" {
		active[cfo.harness] = true
	}
	for _, task := range snapshotLiveTasks(snapshot) {
		active[task.Harness] = true
	}
	return active
}

// snapshotLiveTasks are the snapshot's tasks whose terminal the monitor saw at
// work within the last two minutes: a paused, ended, archived or launching
// task is no live harness, and neither is a saved model.
func snapshotLiveTasks(snapshot Snapshot) []Task {
	var tasks []Task
	for _, task := range snapshot.Tasks {
		if task.Archived || task.Generation == "" || task.Runtime.At.IsZero() || snapshot.At.Sub(task.Runtime.At) > 2*time.Minute || task.Runtime.At.After(snapshot.At.Add(time.Minute)) {
			continue
		}
		isInactive := false
		for _, session := range snapshot.Sessions {
			if session.ID == task.Session && session.TaskID == task.ID && session.Generation == task.Generation && (session.Phase == "ended" || session.Phase == "paused" || session.Phase == "stopped") {
				isInactive = true
				break
			}
		}
		if isInactive {
			continue
		}
		switch task.Runtime.State {
		case "active", "busy", "idle", "parked", "harness-erroring":
			tasks = append(tasks, task)
		}
	}
	return tasks
}
