package supervisor

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/openrouter"
	"github.com/fpresta0607/code-goblins/internal/quota"
)

// keySpendReading is what OpenRouter answered about Pi's key: dollars in the
// window the key's own limit counts over.
func keySpendReading(keyID string, spent, limit float64) openrouter.Reading {
	return openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: &spent, LimitUSD: &limit, RemainingUSD: new(limit - spent), Period: openrouter.PeriodWeekly, ReadAt: time.Now().UTC(), KeyID: keyID, Configured: true}
}

// livePi is a snapshot with one live Pi goblin, the harness the key spend is
// shown for.
func livePi(now time.Time) Snapshot {
	return Snapshot{At: now, Tasks: []Task{{ID: "goblin", Harness: "pi", Generation: "g1", Session: "session", Runtime: RuntimeEvidence{State: "busy", At: now}}}}
}

// The dial shows Pi's OpenRouter key only while a Pi harness runs, and only
// for a Pi that names OpenRouter or holds a key for it.
func TestKeySpendIsShownOnlyForAPiThatUsesOpenRouter(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name     string
		snapshot Snapshot
		reading  *openrouter.Reading
		want     int
	}{
		{name: "a live Pi goblin on OpenRouter", snapshot: livePi(now), reading: &openrouter.Reading{Status: openrouter.StatusAvailable, Configured: true}, want: 1},
		{name: "a live Pi goblin with a key", snapshot: livePi(now), reading: &openrouter.Reading{Status: openrouter.StatusAuthRequired, KeyID: "beef"}, want: 1},
		{name: "a live Pi goblin that names no OpenRouter", snapshot: livePi(now), reading: &openrouter.Reading{Status: openrouter.StatusMissing}, want: 0},
		{name: "no Pi at all", snapshot: Snapshot{At: now, Tasks: []Task{{Harness: "claude", Generation: "g1", Runtime: RuntimeEvidence{State: "busy", At: now}}}}, reading: &openrouter.Reading{Status: openrouter.StatusAvailable, Configured: true}, want: 0},
		{name: "nothing read yet", snapshot: livePi(now), reading: nil, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &Service{keySpend: test.reading}
			usage := service.keySpendUsage(activeHarnesses(cfoState{}, test.snapshot), test.snapshot)
			if len(usage) != test.want {
				t.Fatalf("key spend = %+v, want %d dial", usage, test.want)
			}
			if test.want == 1 && usage[0].Provider != "openrouter" {
				t.Fatalf("provider = %q", usage[0].Provider)
			}
		})
	}
}

// Every caller of one key shows one key total, and the reading is the key's
// own dollars against its own limit in the window that limit resets over.
func TestKeySpendIsOneTotalForEveryCallerOfTheKey(t *testing.T) {
	now := time.Now().UTC()
	snapshot := livePi(now)
	snapshot.Tasks = append(snapshot.Tasks, Task{ID: "other", Harness: "pi", Generation: "g2", Runtime: RuntimeEvidence{State: "idle", At: now}})
	service := &Service{keySpend: &openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), LimitUSD: new(50.0), RemainingUSD: new(40.96), Period: openrouter.PeriodWeekly, ReadAt: now, KeyID: "3f9c1a2b", Configured: true}}
	cfo := cfoState{registered: true, harness: "pi"}
	usage := service.keySpendUsage(activeHarnesses(cfo, snapshot), snapshot)
	if len(usage) != 1 {
		t.Fatalf("callers sharing one credential show %d dials, want one key total", len(usage))
	}
	if usage[0].SpentUSD == nil || *usage[0].SpentUSD != 9.04 || usage[0].LimitUSD == nil || *usage[0].LimitUSD != 50 || usage[0].Period != openrouter.PeriodWeekly {
		t.Fatalf("dial = %+v", usage[0])
	}
}

// A reading the dial cannot trust is stale rather than fresh dollars: past
// its age, and once the window it counted over has rolled over.
func TestKeySpendGoesStaleOnlyWithItsAgeOrItsWindow(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name    string
		reading openrouter.Reading
		want    string
	}{
		{name: "read now", reading: openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), ReadAt: now, KeyID: "a"}, want: openrouter.StatusAvailable},
		{name: "older than a reading can be trusted", reading: openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), ReadAt: now.Add(-quota.MaxAge - time.Minute), KeyID: "a"}, want: openrouter.StatusStale},
		{name: "its window rolled over", reading: openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), ReadAt: now, ResetsAt: now.Add(-time.Minute), KeyID: "a"}, want: openrouter.StatusStale},
		{name: "a window still to come", reading: openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), ReadAt: now, ResetsAt: now.Add(time.Hour), KeyID: "a"}, want: openrouter.StatusAvailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := livePi(now)
			service := &Service{keySpend: &test.reading}
			usage := service.keySpendUsage(activeHarnesses(cfoState{}, snapshot), snapshot)
			if len(usage) != 1 || usage[0].Status != test.want {
				t.Fatalf("dial = %+v, want %q", usage, test.want)
			}
		})
	}
}

// dollarsReading is a reading of keyID with dollars spent at an instant.
func dollarsReading(keyID string, spent float64, at time.Time) *openrouter.Reading {
	return &openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: &spent, LimitUSD: new(50.0), Period: openrouter.PeriodWeekly, ReadAt: at, KeyID: keyID, Configured: true}
}

// A key that was rotated never shows the dollars of the key it replaced, and
// a read that fails for the same key keeps them with the reason it failed.
func TestKeySpendIsKeptOnlyForTheCredentialItWasReadWith(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name    string
		before  *openrouter.Reading
		reading openrouter.Reading
		status  string
		dollars *float64
	}{
		{name: "a first read that failed", reading: openrouter.Reading{Status: openrouter.StatusRateLimited, KeyID: "a", ReadAt: now}, status: openrouter.StatusRateLimited},
		{name: "the same key at a rate limit", before: dollarsReading("a", 9.04, now), reading: openrouter.Reading{Status: openrouter.StatusRateLimited, KeyID: "a", ReadAt: now.Add(time.Minute)}, status: openrouter.StatusRateLimited, dollars: new(9.04)},
		{name: "a rotated key that cannot be read", before: dollarsReading("a", 9.04, now), reading: openrouter.Reading{Status: openrouter.StatusAuthRequired, KeyID: "b", ReadAt: now.Add(time.Minute)}, status: openrouter.StatusAuthRequired},
		{name: "no key at all after one was read", before: dollarsReading("a", 9.04, now), reading: openrouter.Reading{Status: openrouter.StatusMissing, ReadAt: now.Add(time.Minute)}, status: openrouter.StatusMissing},
		{name: "another key's answer", before: dollarsReading("a", 9.04, now), reading: keySpendReading("b", 2.5, 20), status: openrouter.StatusAvailable, dollars: new(2.5)},
	} {
		t.Run(test.name, func(t *testing.T) {
			kept := keptKeySpend(test.before, test.reading)
			if kept.Status != test.status {
				t.Fatalf("status = %q, want %q", kept.Status, test.status)
			}
			if test.dollars == nil {
				if kept.SpentUSD != nil {
					t.Fatalf("dollars = %v, want none: they belong to another credential", *kept.SpentUSD)
				}
				return
			}
			if kept.SpentUSD == nil || *kept.SpentUSD != *test.dollars {
				t.Fatalf("dollars = %v, want %v", kept.SpentUSD, *test.dollars)
			}
		})
	}
}

// The dial's snapshot is a projection of the reading, field by field: no auth
// document, header or provider error travels with it.
func TestKeySpendSnapshotProjectsOnlyTheDialFields(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	snapshot := livePi(now)
	service := &Service{keySpend: &openrouter.Reading{Status: openrouter.StatusAvailable, SpentUSD: new(9.04), LimitUSD: new(50.0), RemainingUSD: new(40.96), Period: openrouter.PeriodWeekly, ReadAt: now, ByokUSD: new(1.2), Blocking: &openrouter.BlockingBudget{Scope: "member", SpentUSD: 32.5, LimitUSD: 50, Period: openrouter.PeriodWeekly}, KeyID: "3f9c1a2b", Configured: true}}

	// Act
	usage := service.keySpendUsage(activeHarnesses(cfoState{}, snapshot), snapshot)
	raised, err := json.Marshal(usage)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var projected []map[string]any
	if err := json.Unmarshal(raised, &projected); err != nil {
		t.Fatal(err)
	}
	want := []string{"blocking", "byok_usd", "configured", "detail", "includes_byok", "key_id", "limit_usd", "period", "provider", "read_at", "remaining_usd", "resets_at", "spent_usd", "status"}
	if fields := slices.Sorted(maps.Keys(projected[0])); !slices.Equal(fields, want) {
		t.Fatalf("projection = %v, want %v", fields, want)
	}
	if len(projected) != 1 || projected[0]["provider"] != "openrouter" {
		t.Fatalf("dial = %s", raised)
	}
}

// The key is read on the supervisor's own timer, bounded and cancellable, and
// a board's snapshot never makes the request.
func TestKeySpendIsReadOnTheSupervisorsTimerAndNeverForASnapshot(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	var reads atomic.Int32
	unbounded := false
	service := &Service{Store: store, Options: Options{KeySpend: func(ctx context.Context) openrouter.Reading {
		reads.Add(1)
		if _, bounded := ctx.Deadline(); !bounded {
			unbounded = true
		}
		return openrouter.Reading{Status: openrouter.StatusMissing, ReadAt: time.Now().UTC()}
	}}}

	// Act
	if _, err := service.Snapshot(); err != nil {
		t.Fatal(err)
	}
	service.readKeySpend(context.Background(), 10*time.Second)

	// Assert
	if got := reads.Load(); got != 1 {
		t.Fatalf("key reads = %d, want only the timer's one and none for the snapshot", got)
	}
	if unbounded {
		t.Error("the read had no deadline to end it")
	}
	service.mu.Lock()
	kept, readAt := service.keySpend, service.keySpendReadAt
	service.mu.Unlock()
	if kept == nil || kept.Status != openrouter.StatusMissing || readAt.IsZero() {
		t.Fatalf("the reading was not kept: %+v", kept)
	}
}

// The dials' timer reads the key spend on its own first pass, within a bound,
// and stops with its context: one pass comes before it waits.
func TestTheDialsTimerReadsTheKeySpend(t *testing.T) {
	// Arrange
	var reads atomic.Int32
	unbounded := false
	service := &Service{Options: Options{KeySpend: func(ctx context.Context) openrouter.Reading {
		reads.Add(1)
		if _, bounded := ctx.Deadline(); !bounded {
			unbounded = true
		}
		return openrouter.Reading{Status: openrouter.StatusMissing, ReadAt: time.Now().UTC()}
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	service.keepSubscriptionUsage(ctx, time.Minute)

	// Assert
	if got := reads.Load(); got != 1 {
		t.Fatalf("key reads = %d, want the timer's one pass", got)
	}
	if unbounded {
		t.Error("the read had no deadline to end it")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.keySpend == nil || service.keySpendReadAt.IsZero() {
		t.Fatalf("the reading was not kept: %+v", service.keySpend)
	}
}
