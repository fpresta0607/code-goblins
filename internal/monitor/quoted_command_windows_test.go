package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

func TestScanIgnoresQuotedNativeCommandsAndKeepsGenuineFaults(t *testing.T) {
	// Command wrapping is a fixture, not recovered historical 4971/4972 screen data.
	for _, test := range []struct {
		name, quoted, refusal string
		fault                 routing.Fault
	}{
		{"search header then auth", "• Ran rg -n \"invalid_api_key\" internal/routing\n  └ no matches", "401 Unauthorized: invalid api key", routing.Auth},
		{"wrapped search then provider", "• Ran rg -n\n    \"invalid_api_key\"\n    internal/routing\n  └ no matches", "Error: 503 Service Unavailable", routing.Provider},
		{"saved explanatory sentence then third party", "The saved rollout confirms that `invalid_api_key` was in an `rg -n` tool command.", "gh: 429 API rate limit exceeded for user", routing.ThirdParty},
		{"constructed operator continuation then quota", "↳ CFO: Inspect the quoted search evidence.\n  invalid_api_key was a literal search token.", "API Error: 429 rate_limit_error: rate limit reached", routing.RateLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 4, 14, 20, 0, 0, time.UTC)
			meta := nativeMeta("quoted-command", "codex")
			writeTask(t, stateDir, meta)
			recordNativeHost(t, stateDir, meta.ID)
			screen := append(strings.Split(test.quoted, "\n"), "", "• Working (8s • esc to interrupt)", "› ", "75% context left")
			service := testService(stateDir, NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}, &now)
			quoted, err := service.Scan(context.Background())
			if err != nil || len(quoted.Observations) != 1 || quoted.Observations[0].Health != HealthBusy || quoted.Event != nil {
				t.Fatalf("quoted command scan = %+v err=%v, want busy without a fault wake", quoted, err)
			}
			persisted, err := ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.Health != HealthBusy || persisted.PendingEvent != nil {
				t.Fatalf("persisted quoted command = %+v err=%v, want busy without pending fault", persisted, err)
			}

			screen = append(screen, test.refusal)
			probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
			service.Probe = probe
			now = now.Add(time.Second)
			actual, err := service.Scan(context.Background())
			if err != nil || actual.Event == nil || actual.Event.Fault != test.fault || !strings.Contains(actual.Event.Detail, test.refusal) || actual.Observations[0].Health != HealthErroring {
				t.Fatalf("genuine refusal = %+v err=%v, want %s and exact refusal", actual, err, test.fault)
			}
			persisted, err = ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.PendingEvent == nil || persisted.PendingEvent.Fault != test.fault {
				t.Fatalf("genuine refusal not persisted: %+v err=%v", persisted, err)
			}
			if _, err := service.Publish(*actual.Event); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			service = testService(stateDir, probe, &now)
			again, err := service.Scan(context.Background())
			if err != nil || again.Event != nil || len(again.Observations) != 1 || again.Observations[0].Health != HealthErroring {
				t.Fatalf("unchanged genuine fault after reopen = %+v err=%v", again, err)
			}
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen[:len(screen)-1]...)}
			now = now.Add(time.Second)
			recovered, err := service.Scan(context.Background())
			if err != nil || len(recovered.Observations) != 1 || recovered.Observations[0].Health != HealthBusy || recovered.Observations[0].Reason == HarnessError || recovered.Observations[0].DemandDeepInspection || recovered.Event != nil {
				t.Fatalf("refusal cleared = %+v err=%v, want recovered working state", recovered, err)
			}
		})
	}
}

func TestScanReportsRemoteCompactCapacityOnceAndRecovers(t *testing.T) {
	for _, hasWorkingMarker := range []bool{false, true} {
		name := "ready"
		if hasWorkingMarker {
			name = "working"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 4, 15, 0, 0, 0, time.UTC)
			meta := nativeMeta("remote-compact-capacity", "codex")
			writeTask(t, stateDir, meta)
			recordNativeHost(t, stateDir, meta.ID)
			refusal := "■ Error running remote compact task: Selected model is at capacity. Please try a different model."
			screen := []string{"````text", "```nested", refusal, "````", "• Working (8s • esc to interrupt)", "› ", "75% context left"}
			service := testService(stateDir, NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}, &now)
			service.Progress = &fakeProgress{sample: ProgressSample{Jobs: []string{"node.exe (fixture)"}, JobCPU: time.Second}}
			quoted, err := service.Scan(context.Background())
			if err != nil || len(quoted.Observations) != 1 || quoted.Observations[0].Health != HealthBusy || quoted.Event != nil {
				t.Fatalf("quoted remote compact = %+v err=%v, want busy without fault", quoted, err)
			}
			now = now.Add(time.Second)
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf("› Ask Codex to do anything", "75% context left")}
			if ready, err := service.Scan(context.Background()); err != nil || ready.Event != nil {
				t.Fatalf("ready baseline = %+v err=%v, want no premature wake", ready, err)
			}
			now = now.Add(4 * time.Minute)
			screen = []string{refusal, "› Ask Codex to do anything", "75% context left"}
			if hasWorkingMarker {
				screen = append(screen, "• Working (8s • esc to interrupt)")
			}
			probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
			service.Probe = probe
			actual, err := service.Scan(context.Background())
			if err != nil || actual.Event == nil || actual.Event.Fault != routing.Provider || !strings.Contains(actual.Event.Detail, refusal) {
				t.Fatalf("remote compact refusal = %+v err=%v, want Provider instead of awaiting-answer", actual, err)
			}
			persisted, err := ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.Health != HealthErroring || persisted.Reason != HarnessError || !persisted.DemandDeepInspection || persisted.PendingEvent == nil || persisted.PendingEvent.Fault != routing.Provider {
				t.Fatalf("persisted remote compact = %+v err=%v, want erroring Provider evidence", persisted, err)
			}
			if _, err := service.Publish(*actual.Event); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			service = testService(stateDir, probe, &now)
			again, err := service.Scan(context.Background())
			if err != nil || again.Event != nil || len(again.Observations) != 1 || again.Observations[0].Health != HealthErroring {
				t.Fatalf("same remote compact after reopen = %+v err=%v, want one fault episode", again, err)
			}
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf("• Working (8s • esc to interrupt)", "› ", "75% context left")}
			now = now.Add(time.Second)
			recovered, err := service.Scan(context.Background())
			if err != nil || len(recovered.Observations) != 1 || recovered.Observations[0].Health != HealthBusy || recovered.Observations[0].Reason == HarnessError || recovered.Observations[0].DemandDeepInspection || recovered.Event != nil {
				t.Fatalf("remote compact refusal cleared = %+v err=%v, want recovered work", recovered, err)
			}
		})
	}
}
