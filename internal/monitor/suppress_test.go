package monitor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/wake"
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
	for range 15 {
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

// A tool the pane shows running holds busy_turn_over_age back for twice the
// busy budget and no longer: a turn whose pane reads "Running…" with nothing
// moving underneath for that long is wedged, and the wake names the row.
func TestARunningToolWithNothingMovingWakesBusyTurnOverAgeAfterTwiceTheBudget(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	scanPane(t, service, probe, herdr.AgentWorking, runningToolPane, &now, 0)
	if early := scanIdle(t, service, probe, herdr.AgentWorking, runningToolPane, &now, 19); len(early) != 0 {
		t.Fatalf("woke inside twice the busy budget: %+v", early)
	}
	wakes := scanIdle(t, service, probe, herdr.AgentWorking, runningToolPane, &now, 10)
	if len(wakes) != 1 {
		t.Fatalf("a turn with nothing moving past twice the budget raised %d wakes, want exactly one: %+v", len(wakes), wakes)
	}
	for _, want := range []string{string(BusyTurnOverAge) + ":", "no progress evidence (transcript write or processor use by its own processes) for 20m", "its pane still shows work running: ⎿  Running… (22s · timeout 10m)"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake detail %q lacks %q", wakes[0].Detail, want)
		}
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
// awaiting_answer wake past the busy budget while that work moves, and the
// wake comes once the count is gone.
func TestABackgroundShellOnThePaneHoldsBackAwaitingAnswerWhileItMoves(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, progress, _ := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"powershell.exe (pid 44)"}, JobCPU: time.Second}
	progress.cpuStep = 20 * time.Second

	for range 15 {
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

	progress.sample.Jobs, progress.cpuStep = nil, 0
	r := scanPane(t, service, probe, herdr.AgentDone, "❯", &now, time.Minute)
	if r.Event == nil || r.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("shell gone with the turn still over = %+v, want an awaiting-answer wake", r)
	}
}

// The footer's shell count holds the wake back only while something moves: a
// dev server left running shows "1 shell" for as long as it lives, so once
// the goblin has shown no progress evidence for the busy budget since its
// turn ended it wakes once as awaiting its answer, naming the row.
func TestABackgroundShellWithNothingMovingWakesAwaitingAnswerAfterTheBudget(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, progress, _ := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"node.exe (pid 44)"}, JobCPU: time.Second}

	if early := scanIdle(t, service, probe, herdr.AgentDone, backgroundShellPane, &now, 10); len(early) != 0 {
		t.Fatalf("woke inside the busy budget: %+v", early)
	}
	wakes := scanIdle(t, service, probe, herdr.AgentDone, backgroundShellPane, &now, 10)
	if len(wakes) != 1 {
		t.Fatalf("a background shell with nothing moving raised %d wakes, want exactly one: %+v", len(wakes), wakes)
	}
	for _, want := range []string{string(AwaitingAnswer) + ":", "its pane still shows work running (⏵⏵ bypass permissions on · 1 shell · ← 1 agent · ↓ to manage)", "no progress evidence (transcript write or processor use by its own processes) for 10m"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake detail %q lacks %q", wakes[0].Detail, want)
		}
	}
	if counted := readTally(t, service).Reasons[AwaitingAnswer]; counted == nil || counted.Suppressed != 1 || counted.Raised != 1 {
		t.Fatalf("tally = %+v, want one suppressed and one raised awaiting_answer", counted)
	}
}

// Idle between turns with the pane still showing work is not a stall while
// the busy budget runs; once nothing has moved for it, it is, and the wake
// names the row.
func TestARunningToolOnThePaneHoldsBackUnchangedIdleForTheBudget(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	service, probe, _, _ := progressService(t, &now)

	if early := scanIdle(t, service, probe, herdr.AgentIdle, runningToolPane, &now, 10); len(early) != 0 {
		t.Fatalf("an idle reading of a pane that shows a running tool woke inside the busy budget: %+v", early)
	}
	if counted := readTally(t, service).Reasons[UnchangedIdle]; counted == nil || counted.Suppressed != 1 {
		t.Fatalf("tally = %+v, want one suppressed unchanged_idle", counted)
	}
	wakes := scanIdle(t, service, probe, herdr.AgentIdle, runningToolPane, &now, 5)
	if len(wakes) != 1 {
		t.Fatalf("a running tool with nothing moving raised %d wakes, want exactly one: %+v", len(wakes), wakes)
	}
	for _, want := range []string{string(UnchangedIdle) + ":", "its pane still shows work running (⎿  Running… (22s · timeout 10m)) and no progress evidence"} {
		if !strings.Contains(wakes[0].Detail, want) {
			t.Errorf("wake detail %q lacks %q", wakes[0].Detail, want)
		}
	}
}

// A native terminal keeps no counters, so between turns the monitor read such
// a goblin by its status log alone, and a goblin whose screen was filling with
// output or whose transcript was being written woke unchanged_idle all the
// same. Output written to its screen, or its transcript written, within the
// stall window is liveness. A clock ticking in a row the harness redraws by
// itself is not: a tool its pane shows running still wakes once the busy
// budget has passed with nothing moving, and a goblin whose screen and records
// are still wakes once.
func TestANativeGoblinsOutputAndTranscriptAreLivenessBetweenTurns(t *testing.T) {
	for _, test := range []struct {
		name         string
		pane         func(minute int) string
		isTranscript bool
		wantWakes    int
	}{
		{"output keeps coming", func(minute int) string { return fmt.Sprintf("ok  \tinternal/package%d\t3.2s", minute) }, false, 0},
		{"transcript keeps growing", func(int) string { return "● Reading the suite's output." }, true, 0},
		{"screen and records still", func(int) string { return "● Reading the suite's output." }, false, 1},
		{"only a tool's clock moves", func(minute int) string { return fmt.Sprintf("  ⎿  Running… (%dm 0s · timeout 10m)", minute) }, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			now := time.Date(2026, 10, 8, 1, 30, 0, 0, time.UTC)
			service, probe, progress, meta := progressService(t, &now)

			// Act: fifteen scans a minute apart, past the stall window and the
			// busy budget.
			var wakes []string
			for minute := range 15 {
				now = now.Add(time.Minute)
				sample := sampleForStatus(meta, herdr.AgentUnknown, test.pane(minute))
				sample.CountersUnavailable = true
				probe.samples[meta.ID] = sample
				if test.isTranscript {
					progress.sample.TranscriptAt = now
				}
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if result.Event == nil {
					continue
				}
				wakes = append(wakes, result.Event.Detail)
				record, err := service.Publish(*result.Event)
				if err != nil {
					t.Fatal(err)
				}
				if err := wake.AckThrough(service.StateDir, record.Seq); err != nil {
					t.Fatal(err)
				}
			}

			// Assert
			if len(wakes) != test.wantWakes {
				t.Fatalf("wakes = %q, want %d", wakes, test.wantWakes)
			}
			for _, detail := range wakes {
				if !strings.HasPrefix(detail, string(UnchangedIdle)+":") {
					t.Errorf("wake %q, want unchanged_idle", detail)
				}
			}
		})
	}
}

// A monitor from before output was read kept a digest of the whole capture,
// which never equals the digest of the output on the same screen. Read as
// output written, it gave every native goblin between turns a fresh idle clock
// at the upgrade, so a wedged one woke a whole stall window late.
func TestAnOlderMonitorsDigestIsNotOutputBetweenTurns(t *testing.T) {
	// Arrange: an older monitor already found this screen idle past the stall
	// window.
	now := time.Date(2026, 10, 8, 13, 0, 0, 0, time.UTC)
	service, probe, _, meta := progressService(t, &now)
	screen := "● Reading the suite's output."
	idleSince := now.Add(-2 * service.StallAfter)
	if err := WriteObservation(service.StateDir, Observation{TaskID: meta.ID, Endpoint: endpointString(meta), EndpointVerdict: ProbePresent, Digest: fmt.Sprintf("%x", sha256.Sum256(capture(screen))), LastObserved: now.Add(-time.Minute), LastSeen: idleSince, LastProgress: idleSince, IdleSince: &idleSince, Health: HealthIdle, Reason: None}); err != nil {
		t.Fatal(err)
	}
	sample := sampleForStatus(meta, herdr.AgentUnknown, screen)
	sample.CountersUnavailable = true
	probe.samples[meta.ID] = sample

	// Act
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	if result.Event == nil || !strings.HasPrefix(result.Event.Detail, string(UnchangedIdle)+":") {
		t.Fatalf("event = %+v, observations = %+v, want unchanged_idle now", result.Event, result.Observations)
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
	// The heartbeat keeps an unknown endpoint in front of the CFO on its own
	// cadence; the goblin's own stale wake is not raised again.
	for range 5 {
		if r := scan(unreadable); r.Event != nil && r.Event.Kind == "stale" {
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
