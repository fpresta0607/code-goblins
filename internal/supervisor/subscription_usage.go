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
}

// keepSubscriptionUsage reads quota-axi for the dials only when the
// supervisor made no read for a minute and a half: every read refreshes the
// dials, and the fleet's reading reads once a minute for the allowance floor.
// On 2026-10-08 both read in the same second each minute, Anthropic's usage
// endpoint refused one of the two, and the dial showed a reading a minute old
// as stale. The half minute over keeps a fleet reading that runs late from
// bringing the dials' own read beside it.
func (s *Service) keepSubscriptionUsage(ctx context.Context, every time.Duration) {
	if s.Options.Quota == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		last := s.quotaReadAt
		s.mu.Unlock()
		if last.IsZero() || time.Since(last) >= every*3/2 {
			s.refreshSubscriptionUsage(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) refreshSubscriptionUsage(ctx context.Context) {
	s.readQuota(ctx, 15*time.Second)
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

func (s *Service) subscriptionUsage(cfo cfoState, snapshot Snapshot) []SubscriptionUsage {
	active := map[string]bool{}
	if cfo.registered && cfo.problem == "" && snapshot.Registration == "" {
		active[cfo.harness] = true
	}
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
			active[task.Harness] = true
		}
	}
	s.mu.Lock()
	readings := maps.Clone(s.subscriptionReadings)
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
		usage = append(usage, SubscriptionUsage{Provider: provider, WeeklyReading: reading})
	}
	return usage
}
