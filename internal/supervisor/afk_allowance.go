package supervisor

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/quota"
)

// keptAllowance is the last reading quota-axi gave of one provider's
// allowance, and when the supervisor took it.
type keptAllowance struct {
	readings []afk.Allowance
	at       time.Time
}

// readQuota reads quota-axi within timeout and keeps the allowance it gives of
// each provider for AFK mode's switch, and the weekly readings the dials show.
// The switch reads none of its own: on 2026-10-08 the read took up to 20
// seconds of each turn of AFK mode while his board waited on it, so a switch
// takes these readings and waits on no program.
func (s *Service) readQuota(ctx context.Context, timeout time.Duration) (quota.Report, string) {
	s.mu.Lock()
	s.quotaReadAt = time.Now()
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	report, skipped := s.Options.Quota(probe)
	s.keepSubscriptionReadings(report, skipped)
	if skipped != "" {
		return report, skipped
	}
	at := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.allowance == nil {
		s.allowance = map[string]keptAllowance{}
	}
	for name, provider := range report.Providers {
		// Numbers quota-axi itself calls old are no reading, so the reading
		// before them stands.
		if !provider.Stale {
			s.allowance[name] = keptAllowance{readings: allowanceOf(name, provider), at: at}
		}
	}
	return report, ""
}

// heldAllowance is the allowance the supervisor last read of each provider,
// in provider order, leaving out a reading older than quota.MaxAge, which is
// no evidence at all.
func (s *Service) heldAllowance(now time.Time) []afk.Allowance {
	s.mu.Lock()
	defer s.mu.Unlock()
	var readings []afk.Allowance
	for _, name := range slices.Sorted(maps.Keys(s.allowance)) {
		if kept := s.allowance[name]; now.Sub(kept.at) <= quota.MaxAge {
			readings = append(readings, kept.readings...)
		}
	}
	return readings
}

// allowanceOf is quota-axi's reading of one provider as AFK mode's report
// keeps it: every window's use and any credit balance.
func allowanceOf(name string, provider quota.Provider) []afk.Allowance {
	var readings []afk.Allowance
	for _, window := range provider.Windows {
		label := window.Label
		if label == "" {
			label = window.ID
		}
		readings = append(readings, afk.Allowance{Provider: name, Window: label, PercentUsed: window.PercentUsed, ResetsAt: window.ResetsAt})
	}
	if credits := provider.Credits; credits != nil {
		readings = append(readings, afk.Allowance{Provider: name, Window: "credits", Credits: true, Remaining: credits.Remaining, Unit: credits.Unit, Unlimited: credits.Unlimited})
	}
	return readings
}
