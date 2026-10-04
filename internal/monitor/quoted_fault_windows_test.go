package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

func TestScanKeepsANativeCodexWorkerHealthyWhileQuotingReviewerQuota(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 10, 3, 14, 15, 0, 0, time.UTC)
	meta := nativeMeta("quoted-review", "codex")
	writeTask(t, stateDir, meta)
	recordNativeHost(t, stateDir, meta.ID)
	screen := []string{
		"• Edited handoff.md (+1 -0)",
		"    64 +The review log states: You've hit your weekly limit · resets Oct 7, 4am (America/Chicago)",
		"• Working (8s • esc to interrupt)",
		"› ",
		"75% context left",
	}
	probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
	service := testService(stateDir, probe, &now)

	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 1 || result.Observations[0].Health != HealthBusy || result.Event != nil {
		t.Fatalf("quoted reviewer scan = %+v, want a working native worker without a fault wake", result)
	}
	persisted, err := ReadObservation(stateDir, meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Health != HealthBusy || persisted.PendingEvent != nil {
		t.Fatalf("persisted observation = %+v, want working without a pending fault", persisted)
	}

	screen = append(screen, "You've hit your usage limit. Upgrade to Plus or try again at Oct 7, 2026 4:00 AM.")
	service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
	now = now.Add(time.Second)
	refused, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refused.Event == nil || refused.Event.Fault != routing.RateLimit || refused.Observations[0].Health != HealthErroring {
		t.Fatalf("actual worker refusal = %+v, want its own rate-limit wake despite the working marker", refused)
	}
}
