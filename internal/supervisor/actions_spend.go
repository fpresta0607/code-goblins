package supervisor

import (
	"context"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
)

func (s *Service) readActionsSpend(ctx context.Context, now time.Time) {
	if s.Options.ActionsSpend == nil || s.Options.Example {
		return
	}
	now = now.UTC()
	s.mu.Lock()
	if !s.actionsSpendReadAt.IsZero() && now.Sub(s.actionsSpendReadAt) < time.Hour {
		s.mu.Unlock()
		return
	}
	s.actionsSpendReadAt = now
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	reading := s.Options.ActionsSpend(probe, now)
	s.mu.Lock()
	before := s.actionsSpendReading
	if reading.Status != "available" && before.Month == reading.Month && before.TotalUSD != nil {
		before.Status, before.Detail, before.CheckedAt = "stale", reading.Detail, reading.CheckedAt
		reading = before
	}
	s.actionsSpendReading = reading
	s.mu.Unlock()
	s.notify()
}

func (s *Service) actionsSpendUsage(now time.Time) actionsbilling.Reading {
	s.mu.Lock()
	reading := s.actionsSpendReading
	s.mu.Unlock()
	month := now.UTC().Format("2006-01")
	if reading.Month != month {
		return actionsbilling.Reading{Status: "unavailable", Month: month, Detail: "Waiting for this month's GitHub Actions usage."}
	}
	if reading.Status == "available" && now.Sub(reading.ReadAt) >= 2*time.Hour {
		reading.Status, reading.Detail = "stale", "The last GitHub Actions usage reading is old."
	}
	return reading
}
