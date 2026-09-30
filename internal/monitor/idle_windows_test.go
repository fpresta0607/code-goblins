package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// A native goblin runs no hook the supervisor can rely on - a Codex goblin
// continues without trusting its hooks - so what its own screen shows decides.
// Claude Code, Codex and pi goblins sitting at their composers with a process
// of their own left idle wake the CFO once, three minutes in, as goblin_idle;
// on 2026-09-30 the same Codex goblin was flagged only after about an hour.
func TestANativeGoblinIdleAtItsComposerWakesWithoutHooks(t *testing.T) {
	for harness, screen := range map[string][]string{
		"claude": {"● Tests pass. Next I will open the pull request.", "❯ Try \"fix typecheck errors\"", "  ⏵⏵ bypass permissions on (shift+tab to cycle)"},
		"codex":  {"• Tests pass. Next I will open the pull request.", "› Ask Codex to do anything", "  gpt-6-astra low · ~\\work\\g1"},
		"pi":     {"Tests pass. Next I will open the pull request.", "────", "↑7.8k ↓895 R31k CH94.9% $0.003 0.8%/1.0M (auto)"},
	} {
		t.Run(harness, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
			meta := nativeMeta("g1", harness)
			writeTask(t, stateDir, meta)
			launched := now.Add(-time.Hour)
			if err := os.Chtimes(filepath.Join(stateDir, "g1.meta"), launched, launched); err != nil {
				t.Fatal(err)
			}
			recordNativeHost(t, stateDir, "g1")
			service := testService(stateDir, BackendProber{Herdr: &fakeProber{}, Native: NativeProber{StateDir: stateDir, ReadScreen: screenOf(screen...)}}, &now)
			service.StallAfter, service.BusyTurnMax = 10*time.Minute, 10*time.Minute
			service.Progress = &fakeProgress{sample: ProgressSample{Jobs: []string{"node.exe (pid 52)"}, JobCPU: time.Second}}

			var wakes []Event
			for minute := 1; minute <= 16; minute++ {
				now = now.Add(time.Minute)
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event == nil {
					continue
				}
				if minute < 3 {
					t.Fatalf("woke %d minute(s) in: %+v", minute, result.Event)
				}
				wakes = append(wakes, *result.Event)
				record, err := service.Publish(*result.Event)
				if err != nil {
					t.Fatal(err)
				}
				if err := wake.AckThrough(stateDir, record.Seq); err != nil {
					t.Fatal(err)
				}
			}

			if len(wakes) != 1 || !strings.HasPrefix(wakes[0].Detail, string(GoblinIdle)+":") || !strings.Contains(wakes[0].Detail, "Tests pass. Next I will open the pull request.") {
				t.Fatalf("wakes = %+v, want one goblin_idle wake carrying the end of its screen", wakes)
			}
		})
	}
}

// A native screen read that fails is marked so the monitor reads it again on
// its next scan before waking.
func TestANativeProberMarksAFailedScreenRead(t *testing.T) {
	stateDir := t.TempDir()
	recordNativeHost(t, stateDir, "g1")
	prober := NativeProber{
		StateDir:   stateDir,
		ReadScreen: func(host.Record) ([]string, error) { return nil, errors.New("the screen reader failed (exit status 1)") },
		Dial:       func(host.Record) error { return nil },
	}

	sample, err := prober.Inspect(context.Background(), nativeMeta("g1", "codex"))

	if err != nil {
		t.Fatal(err)
	}
	if sample.Verdict != ProbeUnknown || !sample.ReadFailed || !strings.Contains(sample.Detail, "screen reader failed") {
		t.Fatalf("sample = %+v, want an unknown sample marked as a failed read", sample)
	}
}
