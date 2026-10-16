package quota

import (
	"testing"
	"time"
)

func TestStartFloorUsesFreshBoundedWindows(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name, kind, binding string
		used                float64
		age, reset          time.Duration
		stale, want         bool
	}{
		{"session at floor", "session", "window", 90, 0, time.Hour, false, true},
		{"week below floor", "weekly", "window", 91, 0, time.Hour, false, true},
		{"above floor", "session", "window", 89, 0, time.Hour, false, false},
		{"unrelated model", "model", "other", 99, 0, time.Hour, false, false},
		{"reset", "weekly", "window", 99, 0, -time.Second, false, false},
		{"old snapshot", "session", "window", 99, 2 * time.Hour, time.Hour, false, false},
		{"stale provider", "session", "window", 99, 0, time.Hour, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := Report{GeneratedAt: now.Add(-test.age), Providers: map[string]Provider{"codex": {
				Known: true, Stale: test.stale,
				Windows: []Window{{ID: "window", Kind: test.kind, PercentUsed: test.used, ResetsAt: now.Add(test.reset)}},
				Scopes:  map[string]Scope{"all_models": {Known: true, BoundedBy: []string{test.binding}}},
			}}}
			got, note := report.StartFloor("codex", "gpt-6-astra", 10, now)
			if got != test.want || got && note == "" {
				t.Errorf("got %v %q, want %v", got, note, test.want)
			}
		})
	}
}
