package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/actionsbilling"
)

func TestActionsSpendReadsAtMostHourlyAndKeepsFailedReadOld(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	total := 8.5
	reads := 0
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
		reads++
		if reads > 1 {
			return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at, Detail: "Check the token's user scope."}
		}
		return actionsbilling.Reading{Status: "available", Month: month, CheckedAt: at, ReadAt: at, Total: &actionsbilling.Usage{Minutes: 120, UsedUSD: 12, BilledUSD: total}, Repositories: []actionsbilling.Repository{{Name: "fern", Usage: actionsbilling.Usage{Minutes: 120, UsedUSD: 12, BilledUSD: total}}}}
	}}}
	service.readActionsSpend(context.Background(), now)
	for range 3 {
		service.actionsSpendUsage(now.Add(time.Minute), "2026-10", nil)
		service.readActionsSpend(context.Background(), now.Add(59*time.Minute))
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	service.readActionsSpend(context.Background(), now.Add(time.Hour))
	reading := service.actionsSpendUsage(now.Add(time.Hour), now.Add(time.Hour).Format("2006-01"), nil)
	if reads != 2 || reading.Status != "stale" || reading.Total == nil || *reading.Total != (actionsbilling.Usage{Minutes: 120, UsedUSD: 12, BilledUSD: total}) || len(reading.Repositories) != 1 || !reading.ReadAt.Equal(now) || reading.Detail == "" || !reading.CheckedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("failed read = %+v, reads = %d", reading, reads)
	}
}

func TestActionsSpendNeverCarriesLastMonthsTotal(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 45, 0, 0, time.UTC)
	total := 8.5
	for _, isFailed := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed read", false: "successful read"}[isFailed], func(t *testing.T) {
			service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{"2026-10": {reading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, Total: &actionsbilling.Usage{BilledUSD: total}}, started: now}}, Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
				if isFailed {
					return actionsbilling.Reading{Status: "unavailable", Month: "2026-11", CheckedAt: at, Detail: "Could not read usage."}
				}
				return actionsbilling.Reading{Status: "available", Month: "2026-11", ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{}}
			}}}
			at := now.Add(30 * time.Minute)
			reading := service.actionsSpendUsage(at, "2026-11", nil)
			if reading.Total != nil || reading.Month != "2026-11" {
				t.Fatalf("old month leaked = %+v", reading)
			}
			service.readActionsSpend(context.Background(), now.Add(time.Hour))
			reading = service.actionsSpendUsage(now.Add(time.Hour), now.Add(time.Hour).Format("2006-01"), nil)
			if isFailed && (reading.Total != nil || reading.Status != "unavailable") || !isFailed && (reading.Total == nil || *reading.Total != (actionsbilling.Usage{})) {
				t.Fatalf("new month = %+v", reading)
			}
		})
	}
}

func TestActionsSpendFirstFailedReadHasNoFigure(t *testing.T) {
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
		return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at, Detail: "Missing user scope."}
	}}}
	now := time.Now().UTC()
	service.readActionsSpend(context.Background(), now)
	reading := service.actionsSpendUsage(now, now.Format("2006-01"), nil)
	if reading.Total != nil || reading.Status != "unavailable" || reading.Detail != "Missing user scope." {
		t.Fatalf("reading = %+v", reading)
	}
}

func TestActionsSpendTimerReadsWithoutAQuotaProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	read := make(chan struct{}, 1)
	service := &Service{Options: Options{ActionsSpend: func(ctx context.Context, at time.Time, month string) actionsbilling.Reading {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Error("billing read has no deadline")
		}
		read <- struct{}{}
		return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at}
	}}}
	done := make(chan struct{})
	go func() { defer close(done); service.keepSubscriptionUsage(ctx, time.Hour) }()
	select {
	case <-read:
	case <-time.After(30 * time.Second):
		t.Fatal("timer made no first reading")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("timer did not stop")
	}
}

func TestActionsSpendUnavailableProviderMakesNoRead(t *testing.T) {
	service := &Service{}
	service.readActionsSpend(context.Background(), time.Now())
	if len(service.actionsSpendReadings) != 0 {
		t.Fatal("read attempted without a provider")
	}
}

func TestActionsSpendExampleNeverReadsTheRealAccount(t *testing.T) {
	reads := 0
	service := &Service{Options: Options{Example: true, ActionsSpend: func(_ context.Context, _ time.Time, _ string) actionsbilling.Reading {
		reads++
		return actionsbilling.Reading{}
	}}}
	service.readActionsSpend(context.Background(), time.Now())
	if reads != 0 {
		t.Fatalf("example made %d billing reads", reads)
	}
}

func TestActionsSpendMarksAnAgedReadingOld(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	total := 8.5
	service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{"2026-10": {reading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, Total: &actionsbilling.Usage{BilledUSD: total}}}}}
	if got := service.actionsSpendUsage(now.Add(2*time.Hour), "2026-10", nil); got.Status != "stale" || got.Total == nil || got.Detail != "" {
		t.Fatalf("reading = %+v", got)
	}
}

func TestActionsSpendSharesSelectedMonthReadsWithTheTimer(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	reads := map[string]int{}
	service := &Service{Options: Options{ActionsSpend: func(ctx context.Context, at time.Time, month string) actionsbilling.Reading {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			t.Error("selected-month read has no deadline")
		}
		reads[month]++
		total := map[string]float64{"2026-10": 8.5, "2026-11": 40}[month]
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: total}}
	}}}
	for range 3 {
		for month, total := range map[string]float64{"2026-10": 8.5, "2026-11": 40} {
			reading := service.readActionsSpendMonth(context.Background(), context.Background(), now, month)
			if reading.Month != month || reading.Total == nil || reading.Total.BilledUSD != total || !reading.ReadAt.Equal(now) || !reading.CheckedAt.Equal(now) {
				t.Fatalf("%s reading = %+v", month, reading)
			}
		}
		service.readActionsSpend(context.Background(), now.Add(59*time.Minute))
	}
	if reads["2026-10"] != 1 || reads["2026-11"] != 1 {
		t.Fatalf("duplicate monthly reads = %v", reads)
	}
	service.readActionsSpend(context.Background(), now.Add(time.Hour))
	if reads["2026-10"] != 2 || reads["2026-11"] != 2 {
		t.Fatalf("hourly monthly reads = %v", reads)
	}
	service.readActionsSpend(context.Background(), now.Add(62*24*time.Hour))
	if len(service.actionsSpendReadings) != 1 || service.actionsSpendReadings["2027-01"] == nil {
		t.Fatalf("expired month retained: %+v", service.actionsSpendReadings)
	}
}

func TestActionsSpendCoalescesConcurrentDevices(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	var reads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
		reads.Add(1)
		close(started)
		<-release
		total := 8.5
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: total}}
	}}}
	var devices sync.WaitGroup
	for range 8 {
		devices.Go(func() {
			reading := service.readActionsSpendMonth(context.Background(), context.Background(), now, "2026-10")
			if reading.Total == nil || reading.Total.BilledUSD != 8.5 {
				t.Errorf("device reading = %+v", reading)
			}
		})
	}
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("reader did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if reading := service.readActionsSpendMonth(ctx, context.Background(), now, "2026-10"); reading.Total != nil || reading.Status != "unavailable" {
		t.Fatalf("cancelled device reading = %+v", reading)
	}
	close(release)
	devices.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent reads = %d", reads.Load())
	}
}

func TestActionsSpendSharedReadSurvivesADeviceDisconnect(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	started, release := make(chan struct{}), make(chan struct{})
	service := &Service{Options: Options{ActionsSpend: func(ctx context.Context, at time.Time, month string) actionsbilling.Reading {
		close(started)
		<-release
		if ctx.Err() != nil {
			return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at}
		}
		total := 8.5
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: total}}
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan actionsbilling.Reading, 1)
	go func() { done <- service.readActionsSpendMonth(ctx, context.Background(), now, "2026-10") }()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	close(release)
	select {
	case reading := <-done:
		if reading.Status != "available" || reading.Total == nil {
			t.Fatalf("disconnected device poisoned shared reading: %+v", reading)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("reader did not finish")
	}
	if reading := service.readActionsSpendMonth(context.Background(), context.Background(), now, "2026-10"); reading.Total == nil {
		t.Fatalf("other device lost shared reading: %+v", reading)
	}
}

func TestActionsSpendStopsReadingWhenTheServiceCloses(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	total := 8.5
	var reads atomic.Int32
	started, ended := make(chan struct{}), make(chan error, 2)
	service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{"2026-10": {reading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now.Add(-time.Hour), Total: &actionsbilling.Usage{BilledUSD: total}}, started: now.Add(-time.Hour)}},
		Options: Options{ActionsSpend: func(ctx context.Context, at time.Time, month string) actionsbilling.Reading {
			if reads.Add(1) == 1 {
				close(started)
			}
			<-ctx.Done()
			ended <- ctx.Err()
			return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at}
		}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.readActionsSpend(ctx, now)
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the read outlived the service")
	}
	if err := <-ended; !errors.Is(err, context.Canceled) || reads.Load() != 1 {
		t.Fatalf("read ended with %v after %d reads", err, reads.Load())
	}
}

func TestActionsSpendLimitsSelectionToPossibleDeviceMonths(t *testing.T) {
	for _, test := range []struct {
		now, month string
		isValid    bool
	}{
		{"2026-11-01T00:30:00Z", "2026-09", true},
		{"2026-11-01T00:30:00Z", "2026-10", true},
		{"2026-11-01T00:30:00Z", "2026-11", true},
		{"2026-11-01T12:04:59Z", "2026-09", true},
		{"2026-11-01T12:05:00Z", "2026-09", false},
		{"2026-11-01T12:05:00Z", "2026-10", true},
		{"2026-12-31T10:00:00Z", "2027-01", true},
		{"2026-12-31T09:59:59.99Z", "2027-01", true},
		{"2026-12-31T09:55:00Z", "2027-01", true},
		{"2026-12-31T09:54:59Z", "2027-01", false},
		{"2028-02-29T23:30:00Z", "2028-03", true},
		{"2028-03-01T00:30:00Z", "2028-02", true},
		{"2026-11-15T00:30:00Z", "2026-10", true},
		{"2026-11-15T00:30:00Z", "2026-09", false},
		{"2027-01-15T12:00:00Z", "2026-12", true},
		{"2027-01-15T12:00:00Z", "2026-11", false},
	} {
		t.Run(test.now+"/"+test.month, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, test.now)
			if err != nil {
				t.Fatal(err)
			}
			if got := isActionsSpendMonth(now, test.month); got != test.isValid {
				t.Fatalf("month %s at %s accepted = %v", test.month, test.now, got)
			}
		})
	}
}

func TestActionsSpendEndpointReadsTheRequestedMonth(t *testing.T) {
	before := time.Now()
	month := before.UTC().Format("2006-01")
	reads := 0
	service := &Service{Instance: "instance-1", lifetime: context.Background(), Options: Options{ActionsSpend: func(_ context.Context, at time.Time, selectedMonth string) actionsbilling.Reading {
		reads++
		total := 8.5
		return actionsbilling.Reading{Status: "available", Month: selectedMonth, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: total}}
	}}}
	handler := NewHTTP(service, "board.local", nil)
	for range 2 {
		response := boardRequest(handler, "GET", "/api/actions-spend?month="+month, "")
		var reading actionsbilling.Reading
		if err := json.Unmarshal(response.Body.Bytes(), &reading); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || reading.Month != month || reading.Total == nil || reading.Total.BilledUSD != 8.5 || reading.ReadAt.Before(before) || reading.CheckedAt.After(time.Now()) {
			t.Fatalf("HTTP %d: %+v", response.Code, reading)
		}
	}
	if reads != 1 {
		t.Fatalf("endpoint reads = %d", reads)
	}
}

// The window offers last month beside this one (#2536), so both endpoints
// serve the calendar month before the device's.
func TestActionsSpendEndpointsServeLastMonth(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	lastMonth := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	reads := map[string]int{}
	service := &Service{Instance: "instance-1", lifetime: context.Background(), Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
		reads[month]++
		if reads[month] == 1 {
			return actionsbilling.Reading{Status: "unavailable", Month: month, CheckedAt: at, Detail: "GitHub could not be reached.", Problem: &actionsbilling.Problem{Kind: "transient", Action: "retry"}}
		}
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: 3.25}}
	}}}
	handler := NewHTTP(service, "board.local", nil)

	// Act
	read := boardRequest(handler, "GET", "/api/actions-spend?month="+lastMonth, "")
	retried := boardRequest(handler, "POST", "/api/actions-spend/repair", `{"month":"`+lastMonth+`","action":"retry"}`)

	// Assert
	var first, second actionsbilling.Reading
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &first) != nil || first.Month != lastMonth || first.Problem == nil {
		t.Fatalf("read HTTP %d: %s", read.Code, read.Body)
	}
	if retried.Code != http.StatusOK || json.Unmarshal(retried.Body.Bytes(), &second) != nil || second.Month != lastMonth || second.Total == nil || second.Total.BilledUSD != 3.25 {
		t.Fatalf("retry HTTP %d: %s", retried.Code, retried.Body)
	}
	if reads[lastMonth] != 2 || len(reads) != 1 {
		t.Fatalf("reads = %v", reads)
	}
}

// A closed month GitHub answered a day after it ended no longer changes: it
// is read once, never again on the hour and never shown old. One read on its
// first day after closing may miss its last day's use, so it is read again.
func TestActionsSpendKeepsASettledMonthAsRead(t *testing.T) {
	for _, test := range []struct {
		name      string
		readAt    time.Time
		isSettled bool
	}{
		{name: "read a day after it closed", readAt: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), isSettled: true},
		{name: "read on the day it closed", readAt: time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC), isSettled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			reads := 0
			service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
				if month == "2026-10" {
					reads++
				}
				return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, Total: &actionsbilling.Usage{BilledUSD: 8.5}}
			}}}
			service.readActionsSpendMonth(context.Background(), context.Background(), test.readAt, "2026-10")

			// Act
			later := test.readAt.Add(3 * time.Hour)
			service.readActionsSpend(context.Background(), later)
			reading := service.actionsSpendUsage(later, "2026-10", nil)

			// Assert
			wantReads, wantStatus := 2, "available"
			if test.isSettled {
				wantReads = 1
			}
			if reads != wantReads || reading.Status != wantStatus || reading.Total == nil || reading.Total.BilledUSD != 8.5 {
				t.Fatalf("reads = %d, reading = %+v", reads, reading)
			}
		})
	}
}

func TestActionsSpendEndpointRejectsInvalidOrUnauthenticatedRequests(t *testing.T) {
	reads := 0
	service := &Service{Instance: "instance-1", lifetime: context.Background(), Options: Options{ActionsSpend: func(context.Context, time.Time, string) actionsbilling.Reading {
		reads++
		return actionsbilling.Reading{}
	}}}
	handler := NewHTTP(service, "board.local", nil)
	for _, month := range []string{"", "2026-00", "2026-13", "2026-1", "2026-10-01", "2000-01", "2099-01"} {
		response := boardRequest(handler, "GET", "/api/actions-spend?month="+month, "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("month %q: HTTP %d", month, response.Code)
		}
	}
	for _, test := range []struct{ header, value string }{
		{"X-CEO-Token", ""}, {"X-CEO-Token", "old-instance"}, {"Origin", "http://other.local"}, {"Sec-Fetch-Site", "cross-site"},
	} {
		request := httptest.NewRequest("GET", "http://board.local/api/actions-spend?month="+time.Now().UTC().Format("2006-01"), nil)
		request.Header.Set("X-CEO-Token", "instance-1")
		request.Header.Set(test.header, test.value)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s=%q: HTTP %d", test.header, test.value, response.Code)
		}
	}
	if reads != 0 || len(service.actionsSpendReadings) != 0 {
		t.Fatalf("invalid requests made %d reads", reads)
	}
}
