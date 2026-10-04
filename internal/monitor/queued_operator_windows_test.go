package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

func TestScanQueuedNativeOperatorPromptKeepsFaultContext(t *testing.T) {
	for _, test := range []struct {
		name, refusal string
		fault         routing.Fault
	}{
		{"auth", "401 Unauthorized: invalid api key", routing.Auth},
		{"provider", "Error: 503 Service Unavailable", routing.Provider},
		{"third party", "gh: 429 API rate limit exceeded for user", routing.ThirdParty},
		{"quota", "API Error: 429 rate_limit_error: rate limit reached", routing.RateLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 3, 23, 0, 0, 0, time.UTC)
			meta := nativeMeta("queued-operator", "codex")
			writeTask(t, stateDir, meta)
			recordNativeHost(t, stateDir, meta.ID)
			screen := []string{
				"\u21b3 " + routing.SteerPrefix + "Inspect the quoted 401 Unauthorized: invalid api key failure.",
				"  " + test.refusal + " was earlier tool output.",
				"\u2022 Working (8s \u2022 esc to interrupt)",
				"\u203a ",
				"75% context left",
			}
			service := testService(stateDir, NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}, &now)
			result, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Observations) != 1 || result.Observations[0].Health != HealthBusy || result.Event != nil {
				t.Fatalf("queued operator scan = %+v, want busy without a fault wake", result)
			}
			persisted, err := ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.Health != HealthBusy || persisted.PendingEvent != nil {
				t.Fatalf("persisted queued operator observation = %+v err=%v", persisted, err)
			}

			screen = append(screen, test.refusal)
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
			now = now.Add(time.Second)
			refused, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if refused.Event == nil || refused.Event.Fault != test.fault || !strings.Contains(refused.Event.Detail, test.refusal) || refused.Observations[0].Health != HealthErroring {
				t.Fatalf("actual refusal = %+v, want %s with the genuine evidence", refused, test.fault)
			}
			persisted, err = ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.PendingEvent == nil || persisted.PendingEvent.Fault != test.fault {
				t.Fatalf("genuine fault was not persisted: %+v err=%v", persisted, err)
			}
			if _, err := service.Publish(*refused.Event); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			again, err := service.Scan(context.Background())
			if err != nil || again.Event != nil {
				t.Fatalf("unchanged fault repeated its event: %+v err=%v", again, err)
			}
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen[:len(screen)-1]...)}
			now = now.Add(time.Second)
			recovered, err := service.Scan(context.Background())
			if err != nil || len(recovered.Observations) != 1 || recovered.Observations[0].Health != HealthBusy || recovered.Event != nil {
				t.Fatalf("worker did not recover after actual refusal left: %+v err=%v", recovered, err)
			}
		})
	}
}
