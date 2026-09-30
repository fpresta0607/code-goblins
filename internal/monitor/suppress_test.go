package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

const (
	runningToolPane     = "  ⎿  Running… (22s · timeout 10m)"
	backgroundShellPane = "  ⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage"
)

func scanPane(t *testing.T, service Service, probe *fakeProber, status, pane string, now *time.Time, step time.Duration) ScanResult {
	t.Helper()
	probe.samples["g1"] = sampleForStatus(metaFor("g1"), status, pane)
	*now = now.Add(step)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readTally(t *testing.T, service Service) Tally {
	t.Helper()
	tally, err := ReadTally(service.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	return tally
}

// On 2026-09-30 the CFO was woken with "busy_turn_over_age ... no progress
// evidence" for goblins whose panes showed a tool running. A pane showing a
// running tool is evidence the goblin is working, so no progress-based stale
// wake fires past the budget while it shows, and each held-back wake is
// counted once for cfo doctor.
func TestARunningToolOnThePaneHoldsBackBusyTurnOverAge(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	scanPane(t, service, probe, herdr.AgentWorking, runningToolPane, &now, 0)
	for range 30 {
		if r := scanPane(t, service, probe, herdr.AgentWorking, runningToolPane, &now, time.Minute); r.Event != nil {
			t.Fatalf("a goblin whose pane shows a running tool woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}

	tally := readTally(t, service)
	counted := tally.Reasons[BusyTurnOverAge]
	if counted == nil || counted.Suppressed != 1 || counted.LastTask != "g1" || !strings.Contains(counted.LastWhy, "Running… (22s · timeout 10m)") {
		t.Fatalf("tally = %+v, want one suppressed busy_turn_over_age for g1 naming the running tool", counted)
	}
}

// The pane holds back only what it is evidence against. A gate step active
// past the budget is the gate's own evidence of a wedge, so it still wakes.
func TestARunningToolDoesNotHoldBackAWedgedGateStep(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: true, Step: "ci", ActiveFor: 2 * time.Hour}}

	scanPane(t, service, probe, herdr.AgentWorking, runningToolPane, &now, 0)
	var woke *Event
	for range 12 {
		if r := scanPane(t, service, probe, herdr.AgentWorking, runningToolPane, &now, time.Minute); r.Event != nil {
			woke = r.Event
			break
		}
	}
	if woke == nil || !strings.Contains(woke.Detail, "gate step ci") {
		t.Fatalf("event = %+v, want the wedged gate step raised", woke)
	}
}

// A Claude Code goblin that ended its turn with a background shell still
// running is waiting on its own work: the footer's shell count holds back the
// awaiting_answer wake however long the shell sits idle, and the wake comes
// once the count is gone.
func TestABackgroundShellOnThePaneHoldsBackAwaitingAnswer(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, progress, _ := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"powershell.exe (pid 44)"}, JobCPU: time.Second}

	for range 90 {
		r := scanPane(t, service, probe, herdr.AgentDone, backgroundShellPane, &now, time.Minute)
		if r.Event != nil {
			t.Fatalf("a goblin waiting on its background shell woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
		if r.Observations[0].Health != HealthBusy {
			t.Fatalf("health = %s, want busy while its background shell runs", r.Observations[0].Health)
		}
	}
	if counted := readTally(t, service).Reasons[AwaitingAnswer]; counted == nil || counted.Suppressed != 1 || !strings.Contains(counted.LastWhy, "1 shell") {
		t.Fatalf("tally = %+v, want one suppressed awaiting_answer naming the shell", counted)
	}

	progress.sample.Jobs = nil
	r := scanPane(t, service, probe, herdr.AgentDone, "❯", &now, time.Minute)
	if r.Event == nil || r.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("shell gone with the turn still over = %+v, want an awaiting-answer wake", r)
	}
}

// Idle between turns with the pane still showing work is not a stall either.
func TestARunningToolOnThePaneHoldsBackUnchangedIdle(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	for range 30 {
		if r := scanPane(t, service, probe, herdr.AgentIdle, runningToolPane, &now, time.Minute); r.Event != nil {
			t.Fatalf("an idle reading of a pane that shows a running tool woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
	if counted := readTally(t, service).Reasons[UnchangedIdle]; counted == nil || counted.Suppressed != 1 {
		t.Fatalf("tally = %+v, want one suppressed unchanged_idle", counted)
	}
}

// "screen reader failed" woke the CFO over and over on 2026-09-30 for
// goblins that were working. One failed read is read again on the next scan
// before it can wake, a read that works in between clears it, and a screen
// that stays unreadable wakes once for the episode rather than every scan.
func TestAFailedScreenReadIsReadAgainBeforeAnEndpointUnknownWake(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, meta := progressService(t, &now)
	unreadable := EndpointSample{Verdict: ProbeUnknown, ReadFailed: true, Detail: "native terminal g1's screen is unreadable: the screen reader failed (exit status 1)"}
	scan := func(sample EndpointSample) ScanResult {
		t.Helper()
		probe.samples[meta.ID] = sample
		now = now.Add(time.Minute)
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Event != nil {
			if _, err := service.Publish(*result.Event); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	working := sampleForStatus(meta, herdr.AgentWorking, "pane")

	scan(working)
	for round := range 3 {
		if r := scan(unreadable); r.Event != nil || r.Observations[0].Health != HealthBusy {
			t.Fatalf("round %d: one failed read = %+v, want no wake and the last reading kept", round, r)
		}
		if r := scan(working); r.Event != nil {
			t.Fatalf("round %d: a read that worked again woke the CFO: %+v", round, r.Event)
		}
	}
	if counted := readTally(t, service).Reasons[EndpointUnknown]; counted == nil || counted.Suppressed != 3 || !strings.Contains(counted.LastWhy, "screen reader failed") {
		t.Fatalf("tally = %+v, want three suppressed endpoint_unknown wakes naming the failed read", counted)
	}

	scan(unreadable)
	second := scan(unreadable)
	if second.Event == nil || second.Observations[0].Reason != EndpointUnknown || !strings.Contains(second.Event.Detail, "screen reader failed") {
		t.Fatalf("second failed read in a row = %+v, want an endpoint_unknown wake", second)
	}
	for range 5 {
		if r := scan(unreadable); r.Event != nil {
			t.Fatalf("a screen that stayed unreadable woke the CFO again: %+v", r.Event)
		}
	}
}

// The tally counts the stale wakes raised beside the ones held back, so a
// detector that stopped seeing reads as silence with nothing examined rather
// than as a healthy fleet.
func TestTheTallyCountsRaisedWakesBesideSuppressedOnes(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	r := scanPane(t, service, probe, herdr.AgentDone, "❯", &now, time.Minute)
	if r.Event == nil {
		t.Fatal("a turn that ended with nothing running did not wake")
	}
	if counted := readTally(t, service).Reasons[AwaitingAnswer]; counted == nil || counted.Raised != 1 || counted.Suppressed != 0 {
		t.Fatalf("tally = %+v, want one raised awaiting_answer", counted)
	}
	if tally := readTally(t, service); tally.Since.IsZero() {
		t.Fatalf("tally = %+v, want the time counting began", tally)
	}
}
