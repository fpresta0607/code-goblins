package supervisor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/quota"
)

func TestAllowanceFloorUsesOnlyMeasuredWeeklyWindows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reset := now.Add(7 * 24 * time.Hour)
	for _, testCase := range []struct {
		name, kind, used, remaining           string
		seconds                               int
		sessionUsed                           int
		isStale, isUnbound, isDurationUnknown bool
		shouldPause                           bool
	}{
		{name: "weekly 4.99 remaining", kind: "weekly", seconds: 604800, used: "95.01", remaining: "4.99", shouldPause: true},
		{name: "weekly 5 remaining", kind: "weekly", seconds: 604800, used: "95", remaining: "5", shouldPause: true},
		{name: "weekly 5.01 remaining", kind: "weekly", seconds: 604800, used: "94.99", remaining: "5.01"},
		{name: "low session healthy week", kind: "weekly", seconds: 604800, used: "40", remaining: "2", sessionUsed: 98},
		{name: "session only", kind: "session", seconds: 18000, used: "98", remaining: "2"},
		{name: "short model window", kind: "model", seconds: 18000, used: "98", remaining: "2"},
		{name: "weekly model window", kind: "model", seconds: 604800, used: "95", remaining: "5", shouldPause: true},
		{name: "model duration unknown", kind: "model", used: "98", remaining: "2"},
		{name: "model duration unmeasured", kind: "model", used: "98", remaining: "2", isDurationUnknown: true},
		{name: "weekly use unknown", kind: "weekly", seconds: 604800, used: `"unknown"`, remaining: "2"},
		{name: "weekly use null", kind: "weekly", seconds: 604800, used: "null", remaining: "2"},
		{name: "window type unknown despite week label", seconds: 604800, used: "98", remaining: "2"},
		{name: "stale provider", kind: "weekly", seconds: 604800, used: "98", remaining: "2", isStale: true},
		{name: "scope does not bind week", kind: "weekly", seconds: 604800, used: "98", remaining: "2", isUnbound: true},
		{name: "measured week with unknown aggregate", kind: "weekly", seconds: 604800, used: "95", remaining: `"unknown"`, shouldPause: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			status := "known"
			if testCase.remaining == `"unknown"` {
				status = "unknown"
			}
			boundedBy := `["seven_day","five_hour"]`
			if testCase.isUnbound {
				boundedBy = `["five_hour"]`
			}
			limitingWindow := "seven_day"
			if testCase.sessionUsed > 0 {
				limitingWindow = "five_hour"
			}
			duration := fmt.Sprint(testCase.seconds)
			if testCase.isDurationUnknown {
				duration = `"unknown"`
			}
			data := fmt.Sprintf(`{"generatedAt":%q,"providers":[{"provider":"codex","state":{"stale":%t},
				"windows":[{"id":"seven_day","label":"week","kind":%q,"windowSeconds":%s,"percentUsed":%s,"resetsAt":%q},
					{"id":"five_hour","kind":"session","windowSeconds":18000,"percentUsed":%d,"resetsAt":%q}],
				"quotaSemantics":{"status":"known","effectiveAvailability":[{"scope":"all_models","status":%q,
					"effectivePercentRemaining":%s,"boundedBy":%s,"runway":{"limitingWindowId":%q}}]}}]}`,
				now.Format(time.RFC3339), testCase.isStale, testCase.kind, duration, testCase.used, reset.Format(time.RFC3339),
				testCase.sessionUsed, now.Add(time.Hour).Format(time.RFC3339), status, testCase.remaining, boundedBy, limitingWindow)
			report, err := quota.Parse([]byte(data), now)
			if err != nil {
				t.Fatal(err)
			}

			gotReset, isLow := AllowanceReset(report, "codex", "", now)

			if isLow != testCase.shouldPause || isLow && !gotReset.Equal(reset) {
				t.Fatalf("weekly reset=%s low=%v, want low=%v reset=%s", gotReset, isLow, testCase.shouldPause, reset)
			}
			service, _ := fleetService(t)
			service.Options.Quota = func(context.Context) (quota.Report, string) { return report, "" }
			service.Options.Dispatch = &Dispatch{Spawn: func(context.Context, []string) (string, error) {
				t.Fatal("quota cache check must not dispatch")
				return "", nil
			}}
			watched := &fleetWakes{}
			if err := service.pauseAtAllowanceFloor(t.Context(), watched, now); err != nil {
				t.Fatal(err)
			}
			if isBlocked := allowanceBlocked(watched, "codex", "", now); isBlocked != testCase.shouldPause {
				t.Fatalf("cached weekly admission blocked=%v, want %v", isBlocked, testCase.shouldPause)
			}
		})
	}
}

func TestAllowanceFloorWaitsForEveryLowWeeklyWindow(t *testing.T) {
	now := time.Now().UTC()
	for _, testCase := range []struct {
		name                   string
		week, model, wantReset time.Time
		shouldPause            bool
	}{
		{name: "latest applicable reset", week: now.Add(time.Hour), model: now.Add(2 * time.Hour), wantReset: now.Add(2 * time.Hour), shouldPause: true},
		{name: "expired account week", week: now, model: now.Add(time.Hour), wantReset: now.Add(time.Hour), shouldPause: true},
		{name: "reset unknown", model: now.Add(time.Hour), shouldPause: true},
		{name: "all expired", week: now, model: now},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			report := quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Windows: []quota.Window{
				{ID: "week", Kind: "weekly", PercentUsed: 98, ResetsAt: testCase.week},
				{ID: "model-week", Kind: "model", WindowSeconds: 604800, PercentUsed: 98, ResetsAt: testCase.model},
			}, Scopes: map[string]quota.Scope{"model:limited": {Known: true, PercentRemaining: 2, BoundedBy: []string{"week", "model-week"}}}}}}

			reset, isLow := AllowanceReset(report, "claude", "limited", now)

			if isLow != testCase.shouldPause || !reset.Equal(testCase.wantReset) {
				t.Fatalf("weekly reset=%s low=%v, want reset=%s low=%v", reset, isLow, testCase.wantReset, testCase.shouldPause)
			}
		})
	}
}

func TestAllowanceFloorClearsCachedEvidenceWhenQuotaIsMissing(t *testing.T) {
	now := time.Now().UTC()
	for _, isReaderMissing := range []bool{false, true} {
		t.Run(fmt.Sprintf("reader missing %v", isReaderMissing), func(t *testing.T) {
			service, _ := fleetService(t)
			if !isReaderMissing {
				service.Options.Quota = func(context.Context) (quota.Report, string) {
					return quota.Report{}, "quota snapshot unavailable"
				}
			}
			service.Options.Dispatch = &Dispatch{Spawn: func(context.Context, []string) (string, error) {
				t.Fatal("unknown quota must not dispatch a pause")
				return "", nil
			}}
			watched := &fleetWakes{AllowanceFloors: map[string]allowanceFloor{
				"codex/all_models": {IsLow: true, Reset: now.Add(time.Hour)},
			}}

			if err := service.pauseAtAllowanceFloor(t.Context(), watched, now); err != nil {
				t.Fatal(err)
			}

			if allowanceBlocked(watched, "codex", "", now) {
				t.Fatal("missing quota retained a previously exhausted cache")
			}
		})
	}
}

func TestAllowanceFloorUsesTheMeasuredModelScope(t *testing.T) {
	now := time.Now().UTC()
	report := quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Windows: []quota.Window{
		{ID: "week", Kind: "weekly", PercentUsed: 60, ResetsAt: now.Add(time.Hour)},
		{ID: "model-week", Kind: "model", WindowSeconds: 604800, PercentUsed: 98, ResetsAt: now.Add(2 * time.Hour)},
	}, Scopes: map[string]quota.Scope{
		"all_models":    {Name: "all_models", Known: true, PercentRemaining: 40, ResetsAt: now.Add(time.Hour), BoundedBy: []string{"week"}},
		"model:limited": {Name: "model:limited", Known: true, PercentRemaining: 2, ResetsAt: now.Add(2 * time.Hour), BoundedBy: []string{"week", "model-week"}},
	}}}}
	for _, testCase := range []struct {
		name, model string
		shouldPause bool
	}{
		{name: "limited model", model: "limited", shouldPause: true},
		{name: "other model", model: "other"},
		{name: "unspecified model"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reset, isLow := AllowanceReset(report, "claude", testCase.model, now)
			if isLow != testCase.shouldPause || isLow && !reset.Equal(now.Add(2*time.Hour)) {
				t.Fatalf("reset=%s low=%v, want low=%v", reset, isLow, testCase.shouldPause)
			}
		})
	}
}

func TestAllowanceFloorIgnoresUnknownStaleAndExpiredReadings(t *testing.T) {
	now := time.Now().UTC()
	for _, testCase := range []struct {
		name             string
		isKnown, isStale bool
		reset            time.Time
	}{
		{name: "unknown", reset: now.Add(time.Hour)},
		{name: "stale", isKnown: true, isStale: true, reset: now.Add(time.Hour)},
		{name: "expired", isKnown: true, reset: now},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var windows []quota.Window
			if testCase.isKnown {
				windows = []quota.Window{{ID: "week", Kind: "weekly", PercentUsed: 98, ResetsAt: testCase.reset}}
			}
			report := quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Stale: testCase.isStale, Windows: windows, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: testCase.isKnown, PercentRemaining: 2, ResetsAt: testCase.reset, BoundedBy: []string{"week"}}}}}}
			if reset, isLow := AllowanceReset(report, "codex", "", now); isLow {
				t.Fatalf("blocked on unusable evidence: %s", reset)
			}
		})
	}
}
