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
	retried time.Time
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
		if ctx.Err() != nil {
			return
		}
		s.readActionsSpendMonth(ctx, ctx, now, month)
	}
}

// actionsSpendClockSkew is how far a device's clock may run from the
// supervisor's and still have its calendar month accepted, so a device a
// moment ahead at its local midnight is not refused its new month.
const actionsSpendClockSkew = 5 * time.Minute

// isActionsSpendMonth reports whether month is a device's calendar month, in
// any time zone, or the month before it, which the window offers as last
// month.
func isActionsSpendMonth(now time.Time, month string) bool {
	earliest := now.UTC().Add(-12*time.Hour - actionsSpendClockSkew)
	lastMonth := time.Date(earliest.Year(), earliest.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	return month >= lastMonth.Format("2006-01") && month <= now.UTC().Add(14*time.Hour+actionsSpendClockSkew).Format("2006-01")
}

// isSettledActionsSpendReading reports whether reading is of a month that had
// ended a day before GitHub answered it: GitHub has reported the month's last
// day by then, and a closed month's figures no longer change, so it is kept
// as read, never read again or shown old.
func isSettledActionsSpendReading(reading actionsbilling.Reading) bool {
	month, err := time.Parse("2006-01", reading.Month)
	return err == nil && reading.Status == "available" && !reading.ReadAt.Before(month.AddDate(0, 1, 1))
}

// readActionsSpendMonth returns month's reading, reading it again when its
// last read began an hour or more ago, unless the reading is settled. ctx bounds only this caller's wait for
// a read another caller started. A read this call starts is bounded by
// lifetime instead, so one device disconnecting never cancels the reading
// every device shares, while the service closing does.
func (s *Service) readActionsSpendMonth(ctx, lifetime context.Context, now time.Time, month string) actionsbilling.Reading {
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
			return s.actionsSpendUsage(now, month, s.billingRuns())
		case <-ctx.Done():
			return actionsbilling.Reading{Status: "unavailable", Month: month, Detail: "The GitHub Actions usage request was interrupted."}
		}
	}
	if entry != nil && (now.Sub(entry.started) < time.Hour || isSettledActionsSpendReading(entry.reading)) {
		s.mu.Unlock()
		return s.actionsSpendUsage(now, month, s.billingRuns())
	}
	if entry == nil {
		entry = &actionsSpendEntry{}
		s.actionsSpendReadings[month] = entry
	}
	entry.started, entry.done = now, make(chan struct{})
	s.mu.Unlock()
	probe, cancel := context.WithTimeout(lifetime, 30*time.Second)
	defer cancel()
	reading := s.Options.ActionsSpend(probe, now, month)
	s.mu.Lock()
	before := entry.reading
	if reading.Status != "available" && before.Total != nil {
		before.Status, before.Detail, before.CheckedAt = "stale", reading.Detail, reading.CheckedAt
		before.Problem = reading.Problem
		reading = before
	}
	entry.reading = reading
	close(entry.done)
	entry.done = nil
	s.mu.Unlock()
	s.notify()
	return s.actionsSpendUsage(now, month, s.billingRuns())
}

// billingRuns are the stored runs a reading looks through for its consent.
func (s *Service) billingRuns() []Run {
	if s.Store == nil {
		return nil
	}
	return s.Store.Snapshot().Runs
}

// actionsSpendUsage is month's cached reading with the billing consent that
// explains its problem: one still pending, or one that ended since the last
// reading GitHub answered, for the account and month it asks access for.
func (s *Service) actionsSpendUsage(now time.Time, month string, runs []Run) actionsbilling.Reading {
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
	// Old says itself where the reading is shown, so an aged reading
	// carries no problem line.
	if reading.Status == "available" && now.Sub(reading.ReadAt) >= 2*time.Hour && !isSettledActionsSpendReading(reading) {
		reading.Status = "stale"
	}
	if reading.Problem == nil {
		return reading
	}
	for _, run := range runs {
		if run.Billing == nil {
			continue
		}
		isPending := run.State == "ready" || run.State == "running"
		isExplaining := reading.Problem.Action == "allow" && run.Billing.Month == month && run.Billing.Account == reading.Problem.Account && run.FinishedAt != nil && run.FinishedAt.After(reading.ReadAt)
		if isPending || isExplaining {
			reading.Recovery = &actionsbilling.Recovery{RunID: run.ID, State: run.State}
		}
	}
	return reading
}

func (h *HTTP) actionsSpend(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-CEO-Token") != h.Service.Instance || h.Service.Instance == "" {
		apiError(w, http.StatusForbidden, "Refresh the board before requesting Actions usage")
		return
	}
	month := r.URL.Query().Get("month")
	now := time.Now()
	if _, err := time.Parse("2006-01", month); err != nil || !isActionsSpendMonth(now, month) {
		apiError(w, http.StatusBadRequest, "Request the device's current or last calendar month as YYYY-MM")
		return
	}
	respond(w, http.StatusOK, h.Service.readActionsSpendMonth(r.Context(), h.Service.lifetime, now, month))
}
