package supervisor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
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

			gotReset, isLow := AllowanceReset(report, "codex", "", fleetconfig.DefaultWeeklyFloorPercent, now)

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

			reset, isLow := AllowanceReset(report, "claude", "limited", fleetconfig.DefaultWeeklyFloorPercent, now)

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
			reset, isLow := AllowanceReset(report, "claude", testCase.model, fleetconfig.DefaultWeeklyFloorPercent, now)
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
			if reset, isLow := AllowanceReset(report, "codex", "", fleetconfig.DefaultWeeklyFloorPercent, now); isLow {
				t.Fatalf("blocked on unusable evidence: %s", reset)
			}
		})
	}
}

// weekReport is provider's week at used percent, resetting at reset, as the
// only window its every model is bound by.
func weekReport(provider string, used float64, reset time.Time) quota.Report {
	return quota.Report{Providers: map[string]quota.Provider{provider: {Known: true, Windows: []quota.Window{
		{ID: "seven_day", Kind: "weekly", WindowSeconds: 604800, PercentUsed: used, ResetsAt: reset},
	}, Scopes: map[string]quota.Scope{"all_models": {Name: "all_models", Known: true, PercentRemaining: 100 - used, ResetsAt: reset, BoundedBy: []string{"seven_day"}}}}}}
}

// The Overlord, 2026-10-09: "keep using claude until at 0". The weekly floor
// is the home's setting, one for each provider, 5 percent where the home
// never set one, and the pause says the percent it stood on.
func TestAllowanceFloorFollowsTheHomesSettingForEachProvider(t *testing.T) {
	for _, testCase := range []struct {
		name, settings, provider string
		used                     float64
		shouldPause              bool
		wantEvidence             string
	}{
		{name: "the default pauses at 5 percent left", provider: "claude", used: 95, shouldPause: true, wantEvidence: "claude's weekly allowance is at its 5 percent floor"},
		{name: "the default runs at 5.01 percent left", provider: "claude", used: 94.99},
		{name: "a floor of 0 runs at 1 percent left", settings: `{"weekly_floor_percent":{"claude":0}}`, provider: "claude", used: 99},
		{name: "a floor of 0 runs at 0.01 percent left", settings: `{"weekly_floor_percent":{"claude":0}}`, provider: "claude", used: 99.99},
		{name: "claude's floor leaves codex at the default", settings: `{"weekly_floor_percent":{"claude":0}}`, provider: "codex", used: 95, shouldPause: true, wantEvidence: "codex's weekly allowance is at its 5 percent floor"},
		{name: "a floor of 10 pauses at 8 percent left", settings: `{"weekly_floor_percent":{"codex":10}}`, provider: "codex", used: 92, shouldPause: true, wantEvidence: "codex's weekly allowance is at its 10 percent floor"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			if testCase.settings != "" {
				writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), testCase.settings)
			}
			now := time.Now().UTC().Truncate(time.Second)
			if _, _, err := afk.TurnOn(h.State, "his own board (goblins-window.exe pid 4242)", nil, now); err != nil {
				t.Fatal(err)
			}
			runningGoblin(t, h, "goblin", testCase.provider)
			reset := now.Add(time.Hour)
			watched := &fleetWakes{}

			// Act
			allowancePass(t, handler.Service, watched, weekReport(testCase.provider, testCase.used, reset), now)

			// Assert
			if isBlocked := allowanceBlocked(watched, testCase.provider, "", now); isBlocked != testCase.shouldPause {
				t.Fatalf("starts on %s blocked=%v, want %v", testCase.provider, isBlocked, testCase.shouldPause)
			}
			if !testCase.shouldPause {
				if calls := awaitRecorded(t, spawner, 1); len(calls) != 0 {
					t.Fatalf("paused above the floor: %v", calls)
				}
				return
			}
			calls := awaitDispatch(t, handler.Service, spawner, 1)
			if !strings.HasSuffix(strings.Join(calls[0], " "), "--reason allowance --until "+reset.Format(time.RFC3339)) {
				t.Fatalf("dispatch = %v, want an allowance pause until the reset", calls)
			}
			entries, _, err := afk.Entries(h.State, "")
			if err != nil {
				t.Fatal(err)
			}
			if pauses := afk.Pauses(entries); len(pauses) != 1 || !strings.Contains(pauses[0].Evidence, testCase.wantEvidence) {
				t.Fatalf("logged pauses = %+v, want evidence %q", pauses, testCase.wantEvidence)
			}
		})
	}
}

// A floor without a reset time cannot record what resumes the pause, and
// the supervisor says so with the floor it stood on.
func TestAllowanceFloorWithoutAResetSaysTheFloorItStoodOn(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	s.Options.Dispatch = &Dispatch{Spawn: func(context.Context, []string) (string, error) {
		t.Fatal("a floor without a reset must not pause")
		return "", nil
	}}
	writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), `{"weekly_floor_percent":{"codex":10}}`)
	runningGoblin(t, h, "goblin", "codex")
	s.Options.Quota = func(context.Context) (quota.Report, string) { return weekReport("codex", 92, time.Time{}), "" }

	// Act
	err := s.pauseAtAllowanceFloor(t.Context(), &fleetWakes{}, time.Now().UTC())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "codex allowance is at its 10 percent weekly floor without a reset time") {
		t.Fatalf("err = %v, want the floor's percent named", err)
	}
}

// At a floor of 0 the week runs until the provider refuses, and a week used
// up is then waited out as a used-up session is: nothing starts or resumes
// on it until it renews, no goblin is paused, and its renewal wakes the CFO
// naming the goblins it stopped.
func TestAWeekUsedUpUnderAFloorOf0IsWaitedOutAsASpentSessionIs(t *testing.T) {
	// Arrange
	s, h := fleetService(t)
	s.Options.Dispatch = &Dispatch{Spawn: func(context.Context, []string) (string, error) {
		t.Fatal("a used-up week under a floor of 0 is waited out, never paused")
		return "", nil
	}}
	writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), `{"weekly_floor_percent":{"claude":0}}`)
	runningGoblin(t, h, "claude-goblin", "claude")
	now := time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC)
	reset := now.Add(time.Hour)
	report := weekReport("claude", 100, reset)
	watched := &fleetWakes{}

	// Act
	allowancePass(t, s, watched, report, now)
	isHeld := allowanceBlocked(watched, "claude", "", now)
	allowancePass(t, s, watched, report, reset)
	isHeldAtReset := allowanceBlocked(watched, "claude", "", reset)

	// Assert
	if !isHeld || isHeldAtReset {
		t.Fatalf("held before the reset=%v and at it=%v, want held until it renews", isHeld, isHeldAtReset)
	}
	records := fleetWakeRecords(t, h, "allowance")
	if len(records) != 2 || !strings.Contains(records[1].Detail, "seven_day window was used up and renewed at "+reset.Format(time.RFC3339)) || !strings.Contains(records[1].Detail, "claude-goblin") {
		t.Fatalf("allowance wakes=%+v, want the warning and one at the renewal naming the goblin", records)
	}
}

// The scheduler reads the same setting: under a floor of 0 a queued Claude
// task starts with 1 percent of the week left, and under the default it
// waits for the reset.
func TestTheSchedulerStartsAQueuedGoblinByTheHomesFloor(t *testing.T) {
	for _, testCase := range []struct {
		name, settings string
		shouldStart    bool
	}{
		{name: "the default floor"},
		{name: "a floor of 0", settings: `{"weekly_floor_percent":{"claude":0}}`, shouldStart: true},
		{name: "a floor of 0 for codex alone", settings: `{"weekly_floor_percent":{"codex":0}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			queueBriefedTask(t, h, "- **next-task** - Ship it", plainBrief)
			if testCase.settings != "" {
				writeFile(t, filepath.Join(h.Root, "config", "fleet.json"), testCase.settings)
			}
			now := time.Now().UTC()
			handler.Service.Options.Quota = func(context.Context) (quota.Report, string) {
				return weekReport("claude", 99, now.Add(time.Hour)), ""
			}

			// Act
			if err := handler.Service.checkFleet(t.Context(), now); err != nil {
				t.Fatal(err)
			}

			// Assert
			if !testCase.shouldStart {
				if calls := awaitRecorded(t, spawner, 1); len(calls) != 0 {
					t.Fatalf("started under the floor: %v", calls)
				}
				return
			}
			if calls := awaitDispatch(t, handler.Service, spawner, 1); calls[0][0] != "spawn" || calls[0][1] != "next-task" {
				t.Fatalf("dispatches=%v, want next-task spawned", calls)
			}
		})
	}
}
