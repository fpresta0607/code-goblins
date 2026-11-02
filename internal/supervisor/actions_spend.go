package supervisor

import (
	"context"
	"net/http"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
)

type actionsSpendEntry struct {
	reading actionsbilling.Reading
	started time.Time
	done    chan struct{}
}

func (s *Service) readActionsSpend(ctx context.Context, now time.Time) {
	months := []string{now.UTC().Format("2006-01")}
	s.mu.Lock()
	for month := range s.actionsSpendReadings {
		if month != months[0] && isActionsSpendMonth(now, month) {
			months = append(months, month)
		}
	}
	s.mu.Unlock()
	for _, month := range months {
		s.readActionsSpendMonth(ctx, now, month)
	}
}

func isActionsSpendMonth(now time.Time, month string) bool {
	return month >= now.UTC().Add(-12*time.Hour).Format("2006-01") && month <= now.UTC().Add(14*time.Hour).Format("2006-01")
}

func (s *Service) readActionsSpendMonth(ctx context.Context, now time.Time, month string) actionsbilling.Reading {
	if s.Options.ActionsSpend == nil || s.Options.Example {
		return actionsbilling.Reading{Status: "unavailable", Month: month, Detail: "GitHub Actions usage is unavailable."}
	}
	s.mu.Lock()
	if s.actionsSpendReadings == nil {
		s.actionsSpendReadings = map[string]*actionsSpendEntry{}
	}
	for cachedMonth := range s.actionsSpendReadings {
		if !isActionsSpendMonth(now, cachedMonth) {
			delete(s.actionsSpendReadings, cachedMonth)
		}
	}
	entry := s.actionsSpendReadings[month]
	if entry != nil && entry.done != nil {
		done := entry.done
		s.mu.Unlock()
		select {
		case <-done:
			return s.actionsSpendUsage(now, month)
		case <-ctx.Done():
			return actionsbilling.Reading{Status: "unavailable", Month: month, Detail: "The GitHub Actions usage request was interrupted."}
		}
	}
	if entry != nil && now.Sub(entry.started) < time.Hour {
		s.mu.Unlock()
		return s.actionsSpendUsage(now, month)
	}
	if entry == nil {
		entry = &actionsSpendEntry{}
		s.actionsSpendReadings[month] = entry
	}
	entry.started, entry.done = now, make(chan struct{})
	s.mu.Unlock()
	// One device disconnecting must not cancel the reading shared by all devices.
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	reading := s.Options.ActionsSpend(probe, now, month)
	if reading.Month != month {
		reading = actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: now, Detail: "GitHub Actions usage did not match the requested month."}
	}
	s.mu.Lock()
	before := entry.reading
	if reading.Status != "available" && before.TotalUSD != nil {
		before.Status, before.Detail, before.CheckedAt = "stale", reading.Detail, reading.CheckedAt
		reading = before
	}
	entry.reading = reading
	close(entry.done)
	entry.done = nil
	s.mu.Unlock()
	s.notify()
	return s.actionsSpendUsage(now, month)
}

func (s *Service) actionsSpendUsage(now time.Time, month string) actionsbilling.Reading {
	s.mu.Lock()
	entry := s.actionsSpendReadings[month]
	var reading actionsbilling.Reading
	if entry != nil {
		reading = entry.reading
	}
	s.mu.Unlock()
	if reading.Month != month {
		return actionsbilling.Reading{Status: "unavailable", Month: month, Detail: "Waiting for this month's GitHub Actions usage."}
	}
	if reading.Status == "available" && now.Sub(reading.ReadAt) >= 2*time.Hour {
		reading.Status, reading.Detail = "stale", "The last GitHub Actions usage reading is old."
	}
	return reading
}

func (h *HTTP) actionsSpend(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-CFO-Token") != h.Service.Instance || h.Service.Instance == "" {
		apiError(w, http.StatusForbidden, "Refresh the board before requesting Actions usage")
		return
	}
	month := r.URL.Query().Get("month")
	now := time.Now()
	if _, err := time.Parse("2006-01", month); err != nil || !isActionsSpendMonth(now, month) {
		apiError(w, http.StatusBadRequest, "Request the device's current calendar month as YYYY-MM")
		return
	}
	respond(w, http.StatusOK, h.Service.readActionsSpendMonth(r.Context(), now, month))
}
