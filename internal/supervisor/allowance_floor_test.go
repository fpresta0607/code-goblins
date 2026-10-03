package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/quota"
)

func TestAllowanceFloorUsesTheMeasuredModelScope(t *testing.T) {
	now := time.Now().UTC()
	report := quota.Report{Providers: map[string]quota.Provider{"claude": {Known: true, Scopes: map[string]quota.Scope{
		"all_models":    {Name: "all_models", Known: true, PercentRemaining: 40, ResetsAt: now.Add(time.Hour)},
		"model:limited": {Name: "model:limited", Known: true, PercentRemaining: 2, ResetsAt: now.Add(2 * time.Hour)},
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
			report := quota.Report{Providers: map[string]quota.Provider{"codex": {Known: true, Stale: testCase.isStale, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: testCase.isKnown, PercentRemaining: 2, ResetsAt: testCase.reset}}}}}
			if reset, isLow := AllowanceReset(report, "codex", "", now); isLow {
				t.Fatalf("blocked on unusable evidence: %s", reset)
			}
		})
	}
}
