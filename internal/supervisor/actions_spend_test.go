package supervisor

import (
	"context"
	"encoding/json"
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
		return actionsbilling.Reading{Status: "available", Month: month, CheckedAt: at, ReadAt: at, TotalUSD: &total, Repositories: []actionsbilling.Repository{{Name: "example-user/fern", SpentUSD: total}}}
	}}}
	service.readActionsSpend(context.Background(), now)
	for range 3 {
		service.actionsSpendUsage(now.Add(time.Minute), "2026-10")
		service.readActionsSpend(context.Background(), now.Add(59*time.Minute))
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
	service.readActionsSpend(context.Background(), now.Add(time.Hour))
	reading := service.actionsSpendUsage(now.Add(time.Hour), now.Add(time.Hour).Format("2006-01"))
	if reads != 2 || reading.Status != "stale" || reading.TotalUSD == nil || *reading.TotalUSD != total || !reading.ReadAt.Equal(now) || reading.Detail == "" || !reading.CheckedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("failed read = %+v, reads = %d", reading, reads)
	}
}

func TestActionsSpendNeverCarriesLastMonthsTotal(t *testing.T) {
	now := time.Date(2026, 10, 31, 23, 45, 0, 0, time.UTC)
	total := 8.5
	for _, isFailed := range []bool{false, true} {
		t.Run(map[bool]string{true: "failed read", false: "successful read"}[isFailed], func(t *testing.T) {
			service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{"2026-10": {reading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, TotalUSD: &total}, started: now}}, Options: Options{ActionsSpend: func(_ context.Context, at time.Time, month string) actionsbilling.Reading {
				if isFailed {
					return actionsbilling.Reading{Status: "unavailable", Month: "2026-11", CheckedAt: at, Detail: "Could not read usage."}
				}
				zero := 0.0
				return actionsbilling.Reading{Status: "available", Month: "2026-11", ReadAt: at, CheckedAt: at, TotalUSD: &zero}
			}}}
			at := now.Add(30 * time.Minute)
			reading := service.actionsSpendUsage(at, "2026-11")
			if reading.TotalUSD != nil || reading.Month != "2026-11" {
				t.Fatalf("old month leaked = %+v", reading)
			}
			service.readActionsSpend(context.Background(), now.Add(time.Hour))
			reading = service.actionsSpendUsage(now.Add(time.Hour), now.Add(time.Hour).Format("2006-01"))
			if isFailed && (reading.TotalUSD != nil || reading.Status != "unavailable") || !isFailed && (reading.TotalUSD == nil || *reading.TotalUSD != 0) {
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
	reading := service.actionsSpendUsage(now, now.Format("2006-01"))
	if reading.TotalUSD != nil || reading.Status != "unavailable" || reading.Detail != "Missing user scope." {
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
	service := &Service{actionsSpendReadings: map[string]*actionsSpendEntry{"2026-10": {reading: actionsbilling.Reading{Status: "available", Month: "2026-10", ReadAt: now, TotalUSD: &total}}}}
	if got := service.actionsSpendUsage(now.Add(2*time.Hour), "2026-10"); got.Status != "stale" || got.TotalUSD == nil || got.Detail == "" {
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
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, TotalUSD: &total}
	}}}
	for range 3 {
		for month, total := range map[string]float64{"2026-10": 8.5, "2026-11": 40} {
			reading := service.readActionsSpendMonth(context.Background(), now, month)
			if reading.Month != month || reading.TotalUSD == nil || *reading.TotalUSD != total || !reading.ReadAt.Equal(now) || !reading.CheckedAt.Equal(now) {
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
	service.readActionsSpend(context.Background(), now.Add(24*time.Hour))
	if len(service.actionsSpendReadings) != 1 || service.actionsSpendReadings["2026-11"] == nil {
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
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, TotalUSD: &total}
	}}}
	var devices sync.WaitGroup
	for range 8 {
		devices.Go(func() {
			reading := service.readActionsSpendMonth(context.Background(), now, "2026-10")
			if reading.TotalUSD == nil || *reading.TotalUSD != 8.5 {
				t.Errorf("device reading = %+v", reading)
			}
		})
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if reading := service.readActionsSpendMonth(ctx, now, "2026-10"); reading.TotalUSD != nil || reading.Status != "unavailable" {
		t.Fatalf("cancelled device reading = %+v", reading)
	}
	close(release)
	devices.Wait()
	if reads.Load() != 1 {
		t.Fatalf("concurrent reads = %d", reads.Load())
	}
}

func TestActionsSpendContainsWrongMonthProviderResponses(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	service := &Service{Options: Options{ActionsSpend: func(_ context.Context, at time.Time, _ string) actionsbilling.Reading {
		total := 40.0
		return actionsbilling.Reading{Status: "available", Month: "2026-11", ReadAt: at, CheckedAt: at, TotalUSD: &total}
	}}}
	reading := service.readActionsSpendMonth(context.Background(), now, "2026-10")
	if reading.Month != "2026-10" || reading.TotalUSD != nil || reading.Status != "unavailable" || !reading.CheckedAt.Equal(now) {
		t.Fatalf("wrong month escaped: %+v", reading)
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
		return actionsbilling.Reading{Status: "available", Month: month, ReadAt: at, CheckedAt: at, TotalUSD: &total}
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan actionsbilling.Reading, 1)
	go func() { done <- service.readActionsSpendMonth(ctx, now, "2026-10") }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not start")
	}
	cancel()
	close(release)
	select {
	case reading := <-done:
		if reading.Status != "available" || reading.TotalUSD == nil {
			t.Fatalf("disconnected device poisoned shared reading: %+v", reading)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not finish")
	}
	if reading := service.readActionsSpendMonth(context.Background(), now, "2026-10"); reading.TotalUSD == nil {
		t.Fatalf("other device lost shared reading: %+v", reading)
	}
}

func TestActionsSpendLimitsSelectionToPossibleDeviceMonths(t *testing.T) {
	for _, test := range []struct {
		now, month string
		isValid    bool
	}{
		{"2026-11-01T00:30:00Z", "2026-10", true},
		{"2026-11-01T00:30:00Z", "2026-11", true},
		{"2026-11-01T12:00:00Z", "2026-10", false},
		{"2026-12-31T10:00:00Z", "2027-01", true},
		{"2026-12-31T09:59:00Z", "2027-01", false},
		{"2028-02-29T23:30:00Z", "2028-03", true},
		{"2028-03-01T00:30:00Z", "2028-02", true},
		{"2026-11-15T00:30:00Z", "2026-10", false},
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
	service := &Service{Instance: "instance-1", Options: Options{ActionsSpend: func(_ context.Context, at time.Time, selectedMonth string) actionsbilling.Reading {
		reads++
		total := 8.5
		return actionsbilling.Reading{Status: "available", Month: selectedMonth, ReadAt: at, CheckedAt: at, TotalUSD: &total}
	}}}
	handler := NewHTTP(service, "board.local", nil)
	for range 2 {
		response := boardRequest(handler, "GET", "/api/actions-spend?month="+month, "")
		var reading actionsbilling.Reading
		if err := json.Unmarshal(response.Body.Bytes(), &reading); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || reading.Month != month || reading.TotalUSD == nil || *reading.TotalUSD != 8.5 || reading.ReadAt.Before(before) || reading.CheckedAt.After(time.Now()) {
			t.Fatalf("HTTP %d: %+v", response.Code, reading)
		}
	}
	if reads != 1 {
		t.Fatalf("endpoint reads = %d", reads)
	}
}

func TestActionsSpendEndpointRejectsInvalidOrUnauthenticatedRequests(t *testing.T) {
	reads := 0
	service := &Service{Instance: "instance-1", Options: Options{ActionsSpend: func(context.Context, time.Time, string) actionsbilling.Reading {
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
		{"X-CFO-Token", ""}, {"X-CFO-Token", "old-instance"}, {"Origin", "http://other.local"}, {"Sec-Fetch-Site", "cross-site"},
	} {
		request := httptest.NewRequest("GET", "http://board.local/api/actions-spend?month="+time.Now().UTC().Format("2006-01"), nil)
		request.Header.Set("X-CFO-Token", "instance-1")
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
