package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
)

func TestActionsSpendReadsAtMostHourlyAndKeepsFailedReadOld(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	total := 8.5
	reads := 0
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time) actionsbilling.Reading {
		reads++
		if reads > 1 {
			return actionsbilling.Reading{Status: "unavailable", Month: at.Format("2006-01"), CheckedAt: at, Detail: "Check the token's user scope."}
		}
		return actionsbilling.Reading{Status: "available", Month: at.Format("2006-01"), CheckedAt: at, ReadAt: at, TotalUSD: &total, Repositories: []actionsbilling.Repository{{Name: "example-user/fern", SpentUSD: total}}}
	}}}
	service.readActionsSpend(context.Background(), now)
	for range 3 {
		service.actionsSpendUsage(now.Add(time.Minute))
		service.readActionsSpend(context.Background(), now.Add(59*time.Minute))
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	service.readActionsSpend(context.Background(), now.Add(time.Hour))
	reading := service.actionsSpendUsage(now.Add(time.Hour))
	if reads != 2 || reading.Status != "stale" || reading.TotalUSD == nil || *reading.TotalUSD != total || !reading.ReadAt.Equal(now) || reading.Detail == "" || !reading.CheckedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("failed read = %+v, reads = %d", reading, reads)
	}
}

func TestActionsSpendNeverCarriesLastMonthsTotal(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 45, 0, 0, time.UTC)
	total := 8.5
	for _, isFailed := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed read", false: "successful read"}[isFailed], func(t *testing.T) {
			service := &Service{actionsSpendReading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, TotalUSD: &total}, actionsSpendReadAt: now, Options: Options{ActionsSpend: func(_ context.Context, at time.Time) actionsbilling.Reading {
				if isFailed {
					return actionsbilling.Reading{Status: "unavailable", Month: "2026-11", CheckedAt: at, Detail: "Could not read usage."}
				}
				zero := 0.0
				return actionsbilling.Reading{Status: "available", Month: "2026-11", ReadAt: at, CheckedAt: at, TotalUSD: &zero}
			}}}
			at := now.Add(30 * time.Minute)
			reading := service.actionsSpendUsage(at)
			if reading.TotalUSD != nil || reading.Month != "2026-11" {
				t.Fatalf("old month leaked = %+v", reading)
			}
			service.readActionsSpend(context.Background(), now.Add(time.Hour))
			reading = service.actionsSpendUsage(now.Add(time.Hour))
			if isFailed && (reading.TotalUSD != nil || reading.Status != "unavailable") || !isFailed && (reading.TotalUSD == nil || *reading.TotalUSD != 0) {
				t.Fatalf("new month = %+v", reading)
			}
		})
	}
}

func TestActionsSpendFirstFailedReadHasNoFigure(t *testing.T) {
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time) actionsbilling.Reading {
		return actionsbilling.Reading{Status: "unavailable", Month: at.Format("2006-01"), CheckedAt: at, Detail: "Missing user scope."}
	}}}
	now := time.Now().UTC()
	service.readActionsSpend(context.Background(), now)
	reading := service.actionsSpendUsage(now)
	if reading.TotalUSD != nil || reading.Status != "unavailable" || reading.Detail != "Missing user scope." {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestActionsSpendTimerReadsWithoutAQuotaProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := make(chan struct{}, 1)
	service := &Service{Options: Options{ActionsSpend: func(ctx context.Context, at time.Time) actionsbilling.Reading {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Error("billing read has no deadline")
		}
		read <- struct{}{}
		return actionsbilling.Reading{Status: "unavailable", Month: at.UTC().Format("2006-01"), CheckedAt: at}
	}}}
	done := make(chan struct{})
	go func() { defer close(done); service.keepSubscriptionUsage(ctx, time.Hour) }()
	select {
	case <-read:
	case <-time.After(5 * time.Second):
		t.Fatal("timer made no first reading")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not stop")
	}
}

func TestActionsSpendUnavailableProviderMakesNoRead(t *testing.T) {
	service := &Service{}
	service.readActionsSpend(context.Background(), time.Now())
	if !service.actionsSpendReadAt.IsZero() {
		t.Fatal("read attempted without a provider")
	}
}

func TestActionsSpendExampleNeverReadsTheRealAccount(t *testing.T) {
	reads := 0
	service := &Service{Options: Options{Example: true, ActionsSpend: func(_ context.Context, _ time.Time) actionsbilling.Reading { reads++; return actionsbilling.Reading{} }}}
	service.readActionsSpend(context.Background(), time.Now())
	if reads != 0 {
		t.Fatalf("example made %d billing reads", reads)
	}
}

func TestActionsSpendMarksAnAgedReadingOld(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	total := 8.5
	service := &Service{actionsSpendReading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, TotalUSD: &total}}
	if got := service.actionsSpendUsage(now.Add(2 * time.Hour)); got.Status != "stale" || got.TotalUSD == nil || got.Detail == "" {
		t.Fatalf("reading = %+v", got)
	}
}
