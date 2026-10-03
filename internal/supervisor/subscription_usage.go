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

func (s *Service) keepSubscriptionUsage(ctx context.Context, every time.Duration) {
	if s.Options.Quota == nil {
		return
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		s.refreshSubscriptionUsage(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) refreshSubscriptionUsage(ctx context.Context) {
	probe, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	report, skipped := s.Options.Quota(probe)
	readings := map[string]quota.WeeklyReading{}
	for _, provider := range []string{"claude", "codex"} {
		reading := report.Weekly(provider, time.Now().UTC())
		if skipped != "" && reading.Status == "available" {
			reading.Status, reading.PercentRemaining = "unavailable", nil
		}
		readings[provider] = reading
	}
	s.mu.Lock()
	s.subscriptionReadings = readings
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
		case "active", "busy", "idle", "harness-erroring":
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
