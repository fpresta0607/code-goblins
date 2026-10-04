package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

func TestScanReportsNativeCodexCapacityOnceAndRecovers(t *testing.T) {
	for _, working := range []bool{false, true} {
		name := "ready"
		if working {
			name = "working"
		}
		t.Run(name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 3, 15, 6, 0, 0, time.UTC)
			meta := nativeMeta("capacity", "codex")
			writeTask(t, stateDir, meta)
			recordNativeHost(t, stateDir, meta.ID)
			service := testService(stateDir, NativeProber{StateDir: stateDir, ReadScreen: screenOf("• Working (8s • esc to interrupt)", "› ", "100% context left")}, &now)
			service.Progress = &fakeProgress{sample: ProgressSample{Jobs: []string{"node.exe (fixture)"}, JobCPU: time.Second}}
			if _, err := service.Scan(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf("› Ask Codex to do anything", "100% context left")}
			if ready, err := service.Scan(context.Background()); err != nil || ready.Event != nil {
				t.Fatalf("seeded ready state = %+v, %v, want no wake yet", ready, err)
			}
			now = now.Add(4 * time.Minute)
			screen := []string{"■ Selected model is at capacity. Please try a different model.", "› Ask Codex to do anything", "100% context left"}
			if working {
				screen = append(screen, "• Working (8s • esc to interrupt)")
			}
			probe := NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
			service.Probe = probe

			first, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if first.Event == nil || first.Event.Fault != routing.Provider || !strings.Contains(first.Event.Detail, "harness_error: provider:") {
				t.Fatalf("capacity scan = %+v, want a Provider harness-error event", first)
			}
			persisted, err := ReadObservation(stateDir, meta.ID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Health != HealthErroring || persisted.Reason != HarnessError || !persisted.DemandDeepInspection || persisted.PendingEvent == nil || persisted.PendingEvent.Fault != routing.Provider {
				t.Fatalf("persisted capacity = %+v, want erroring Provider event", persisted)
			}
			if _, err := service.Publish(*first.Event); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			service = testService(stateDir, probe, &now)
			second, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if second.Event != nil || second.Observations[0].Health != HealthErroring {
				t.Fatalf("same capacity after reopen = %+v, want erroring without another event", second)
			}
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf("• Working (8s • esc to interrupt)", "› ", "100% context left")}
			now = now.Add(time.Second)
			recovered, err := service.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Observations[0].Health != HealthBusy || recovered.Observations[0].Reason == HarnessError || recovered.Observations[0].DemandDeepInspection {
				t.Fatalf("capacity cleared = %+v, want recovered working state", recovered)
			}
		})
	}
}

func TestScanKeepsNestedQuotedCapacityOutOfProviderEpisodes(t *testing.T) {
	for _, fence := range []struct {
		name, opening, inner, closing string
	}{
		{"backtick", "````text", "```nested", "````"},
		{"tilde", "~~~~text", "~~~nested", "~~~~"},
	} {
		t.Run(fence.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
			meta := nativeMeta("quoted-capacity", "codex")
			writeTask(t, stateDir, meta)
			recordNativeHost(t, stateDir, meta.ID)
			banner := "■ Selected model is at capacity. Please try a different model."
			screen := []string{fence.opening, fence.inner, banner, fence.closing, "• Working (8s • esc to interrupt)", "› ", "75% context left"}
			service := testService(stateDir, NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}, &now)
			quoted, err := service.Scan(context.Background())
			if err != nil || len(quoted.Observations) != 1 || quoted.Observations[0].Health != HealthBusy || quoted.Event != nil {
				t.Fatalf("quoted capacity scan = %+v err=%v, want busy without a fault wake", quoted, err)
			}
			persisted, err := ReadObservation(stateDir, meta.ID)
			if err != nil || persisted.Health != HealthBusy || persisted.PendingEvent != nil {
				t.Fatalf("persisted quoted capacity = %+v err=%v, want busy without a pending fault", persisted, err)
			}
			screen = append(screen, banner)
			service.Probe = NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}
			now = now.Add(time.Second)
			actual, err := service.Scan(context.Background())
			if err != nil || actual.Event == nil || actual.Event.Fault != routing.Provider || !strings.Contains(actual.Event.Detail, banner) || actual.Observations[0].Health != HealthErroring {
				t.Fatalf("actual capacity after quote = %+v err=%v, want Provider with genuine evidence", actual, err)
			}
		})
	}
}
