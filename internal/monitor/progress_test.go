package monitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type fakeProgress struct {
	sample ProgressSample
	err    error
	calls  int
	// cpuStep is the processor time the goblin's own processes use between
	// one reading and the next.
	cpuStep time.Duration
}

func (f *fakeProgress) InspectProgress(context.Context, state.TaskMeta, EndpointSample) (ProgressSample, error) {
	f.calls++
	f.sample.JobCPU += f.cpuStep
	return f.sample, f.err
}

// progressService is a monitor whose gate shows no run, the shape of a long
// refactor or a goblin fixing a gate between runs, so only the goblin's own
// progress evidence can tell a long turn from a wedged one.
func progressService(t *testing.T, now *time.Time) (Service, *fakeProber, *fakeProgress, state.TaskMeta) {
	t.Helper()
	stateDir := t.TempDir()
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	progress := &fakeProgress{}
	service := testService(stateDir, probe, now)
	service.Gate = &fakeGate{sample: GateSample{Active: false}}
	service.Progress = progress
	return service, probe, progress, meta
}

func scanStatus(t *testing.T, service Service, probe *fakeProber, meta state.TaskMeta, status string, now *time.Time, step time.Duration) ScanResult {
	t.Helper()
	probe.samples[meta.ID] = sampleForStatus(meta, status, "pane")
	*now = now.Add(step)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// The first noise class of 2026-09-25: goblins plainly working - a refactor, a
// gate being fixed - woke the CFO because their turn had outlived the budget.
// A turn whose transcript keeps being written is progressing however old it is.
func TestWorkingPastBudgetStaysQuietWhileTranscriptMoves(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)

	scanWorking(t, service, probe, meta, &now, 0)
	for range 12 {
		progress.sample.TranscriptAt = now.Add(9 * time.Minute)
		if r := scanWorking(t, service, probe, meta, &now, 10*time.Minute); r.Event != nil {
			t.Fatalf("a turn writing its transcript woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
}

// A long foreground build or test run writes no transcript until it returns,
// but it burns the processor, and that is progress too.
func TestWorkingPastBudgetStaysQuietWhileItsOwnProcessesUseCPU(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"bash.exe (pid 40)"}}

	scanWorking(t, service, probe, meta, &now, 0)
	for range 90 {
		progress.sample.JobCPU += 30 * time.Second
		if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
			t.Fatalf("a turn whose test run is using the processor woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
}

// The wedge must still wake: no transcript write and no processor use by its
// own processes for the whole budget. It wakes once, and it is raised again
// only after its evidence has moved and then stopped again.
func TestWorkingPastBudgetWakesOnlyWhenEvidenceStops(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"bash.exe (pid 40)"}, JobCPU: time.Second}

	scanWorking(t, service, probe, meta, &now, 0)
	var woke *Event
	for range 12 {
		if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
			woke = r.Event
			break
		}
	}
	if woke == nil {
		t.Fatal("a turn with no transcript write and an idle process for the whole budget never woke")
	}
	for _, want := range []string{string(BusyTurnOverAge), "no progress evidence (transcript write or processor use by its own processes) for", "bash.exe (pid 40)"} {
		if !strings.Contains(woke.Detail, want) {
			t.Errorf("wake detail %q lacks %q", woke.Detail, want)
		}
	}
	if _, err := service.Publish(*woke); err != nil {
		t.Fatal(err)
	}
	if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
		t.Fatalf("re-woke for the same wedge: %+v", r.Event)
	}

	progress.sample.TranscriptAt = now.Add(time.Minute)
	resumed := scanWorking(t, service, probe, meta, &now, time.Minute)
	if resumed.Event != nil || resumed.Observations[0].Health != HealthBusy {
		t.Fatalf("a goblin whose transcript moved again = %+v, want busy and quiet", resumed.Observations[0])
	}

	var again *Event
	for range 12 {
		if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
			again = r.Event
			break
		}
	}
	if again == nil {
		t.Fatal("a goblin that resumed and then went silent for the budget again was never raised again")
	}
}

// A 75-minute foreground test run writes no transcript until it returns. Its
// processor use is first read once the budget has run out, and one quiet 15s
// reading - the tests waiting on a sleep or the network - proves nothing about
// the hour behind it. The goblin wakes only once its processes have stayed
// under the share for a whole stall window.
func TestLongTestRunIsJudgedOverAWindowNotOneReading(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	service.BusyTurnMax = time.Hour
	service.StallAfter = 10 * time.Minute
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"go.exe (pid 44)"}}

	scanWorking(t, service, probe, meta, &now, 0)
	for range 60 {
		progress.sample.JobCPU += 40 * time.Second
		if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
			t.Fatalf("a test run inside the budget woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
	if r := scanWorking(t, service, probe, meta, &now, 15*time.Second); r.Event != nil {
		t.Fatalf("one quiet 15s reading past the budget woke the CFO: %+v", r.Event)
	}

	windowEnds := now.Add(-15 * time.Second).Add(service.StallAfter)
	var woke *Event
	for range 60 {
		if r := scanWorking(t, service, probe, meta, &now, 15*time.Second); r.Event != nil {
			woke = r.Event
			break
		}
	}
	if woke == nil {
		t.Fatal("a test run that stayed under the share for a whole stall window never woke")
	}
	if now.Before(windowEnds) {
		t.Fatalf("woke at %s, before a whole stall window from %s was measured", now.Format(time.TimeOnly), windowEnds.Add(-service.StallAfter).Format(time.TimeOnly))
	}
	if !strings.Contains(woke.Detail, "go.exe (pid 44)") {
		t.Errorf("wake detail %q lacks the test run still going", woke.Detail)
	}
}

// A monitor loop polling for something that never comes - `until gh run view
// ...; do sleep 30; done` - starts a fresh child every poll and loses it
// between readings, so the summed processor time of its own processes drops
// at every other reading, each time to a new low. That churn is no progress,
// and it must not hold the goblin quiet past the budget and one stall
// interval, whether it is its foreground command or a job left running after
// its turn.
func TestChurningPollLoopStillWakesAfterTheBudgetAndOneStallInterval(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		reason Reason
	}{
		{"working", herdr.AgentWorking, BusyTurnOverAge},
		{"turn ended", herdr.AgentDone, AwaitingAnswer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
			now := start
			service, probe, progress, meta := progressService(t, &now)
			service.BusyTurnMax = time.Hour
			service.StallAfter = 10 * time.Minute
			progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"bash.exe (pid 47)"}, JobCPU: 200 * time.Millisecond}

			scanStatus(t, service, probe, meta, tc.status, &now, 0)
			var woke *Event
			for reading := range 120 {
				progress.sample.JobCPU = 200 * time.Millisecond
				if reading%2 == 1 {
					progress.sample.JobCPU = 100*time.Millisecond - time.Duration(reading/2)*time.Millisecond
				}
				if r := scanStatus(t, service, probe, meta, tc.status, &now, time.Minute); r.Event != nil {
					woke = r.Event
					break
				}
			}
			if woke == nil {
				t.Fatalf("a churning poll loop held the goblin quiet until %s", now.Format(time.Kitchen))
			}
			deadline := start.Add(service.BusyTurnMax + service.StallAfter + time.Minute)
			if now.After(deadline) {
				t.Fatalf("woke at %s, after the budget and one stall interval (%s)", now.Format(time.Kitchen), deadline.Format(time.Kitchen))
			}
			for _, want := range []string{string(tc.reason), "bash.exe (pid 47)"} {
				if !strings.Contains(woke.Detail, want) {
					t.Errorf("wake detail %q lacks %q", woke.Detail, want)
				}
			}
		})
	}
}

// cfo watch rescans at once when any goblin's status changes, so two readings
// of one goblin can land a second apart. A short-lived child caught in that
// second - a poll loop's `gh`, a dev server's garbage collection - uses more
// than 5% of the second but nothing like 5% of a minute, and it must not pass
// for progress and hold a wedged loop quiet past the budget.
func TestReadingsASecondApartDoNotTurnABlipIntoProgress(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status string
		reason Reason
	}{
		{"working", herdr.AgentWorking, BusyTurnOverAge},
		{"turn ended", herdr.AgentDone, AwaitingAnswer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
			now := start
			service, probe, progress, meta := progressService(t, &now)
			service.BusyTurnMax = time.Hour
			service.StallAfter = 10 * time.Minute
			progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"bash.exe (pid 49)"}, JobCPU: time.Second}

			scanStatus(t, service, probe, meta, tc.status, &now, 0)
			var woke *Event
			for range 90 {
				progress.sample.JobCPU = time.Second
				if r := scanStatus(t, service, probe, meta, tc.status, &now, 59*time.Second); r.Event != nil {
					woke = r.Event
					break
				}
				progress.sample.JobCPU = time.Second + 100*time.Millisecond
				if r := scanStatus(t, service, probe, meta, tc.status, &now, time.Second); r.Event != nil {
					woke = r.Event
					break
				}
			}
			if woke == nil {
				t.Fatalf("blips caught by readings a second apart held the goblin quiet until %s", now.Format(time.Kitchen))
			}
			deadline := start.Add(service.BusyTurnMax + service.StallAfter + 2*time.Minute)
			if now.After(deadline) {
				t.Fatalf("woke at %s, after the budget and one stall interval (%s)", now.Format(time.Kitchen), deadline.Format(time.Kitchen))
			}
			if !strings.Contains(woke.Detail, string(tc.reason)) {
				t.Errorf("wake detail %q lacks %q", woke.Detail, tc.reason)
			}
		})
	}
}

// A burst of real work late in a long quiet stretch is progress: one minute
// at a full processor is judged against that minute, not diluted across the
// quiet stretch before it.
func TestLateBurstOfProcessorUseCountsAsProgress(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	service.BusyTurnMax = time.Hour
	service.StallAfter = 10 * time.Minute
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"go.exe (pid 48)"}, JobCPU: time.Second}

	for range 70 {
		if now.Equal(time.Date(2026, 9, 25, 18, 45, 0, 0, time.UTC)) {
			progress.sample.JobCPU += time.Minute
		}
		if r := scanStatus(t, service, probe, meta, herdr.AgentDone, &now, time.Minute); r.Event != nil {
			t.Fatalf("a goblin whose own job used a full processor at 18:46 woke at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
}

// The gate-being-fixed shape: the goblin's gate alternates between a step
// running and no run at all while the goblin works on its findings. Every flip
// used to clear the wake and raise a fresh one.
func TestGateFlippingUnderAWorkingGoblinDoesNotRewake(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	gate := &fakeGate{}
	service.Gate = gate

	scanWorking(t, service, probe, meta, &now, 0)
	wakes := 0
	for step := range 40 {
		gate.sample = GateSample{Active: step%2 == 0, Step: "review", ActiveFor: 2 * time.Minute}
		progress.sample.TranscriptAt = now.Add(30 * time.Second)
		if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
			wakes++
			if _, err := service.Publish(*r.Event); err != nil {
				t.Fatal(err)
			}
		}
	}
	if wakes != 0 {
		t.Fatalf("a goblin fixing its gate woke the CFO %d times in 40 minutes, want none", wakes)
	}
}

// The second noise class: a goblin that ended its turn with a background job
// or a monitor still running is waiting on its own work, not on anybody's
// answer. It wakes when that work is gone and the goblin still has not moved.
func TestTurnEndedWithABackgroundJobStaysQuietUntilTheJobEnds(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"bash.exe (pid 41)"}}

	for range 5 {
		r := scanStatus(t, service, probe, meta, herdr.AgentDone, &now, time.Minute)
		if r.Event != nil {
			t.Fatalf("a goblin waiting on its own background job woke the CFO: %+v", r.Event)
		}
		if r.Observations[0].Health != HealthBusy {
			t.Fatalf("health = %s, want busy while its own job runs", r.Observations[0].Health)
		}
	}

	progress.sample.Jobs = nil
	r := scanStatus(t, service, probe, meta, herdr.AgentDone, &now, time.Minute)
	if r.Event == nil || r.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("job ended with the turn still over = %+v, want an awaiting-answer wake", r)
	}
}

// A background process that never reports back - a dev server left running,
// a sampler asleep - holds the goblin quiet for the busy budget and no longer:
// then the wake names what is still running.
func TestTurnEndedWithAnIdleBackgroundProcessWakesAfterTheBudget(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"node.exe (pid 42)"}, JobCPU: time.Second}

	var woke *Event
	for range 15 {
		if r := scanStatus(t, service, probe, meta, herdr.AgentDone, &now, time.Minute); r.Event != nil {
			woke = r.Event
			break
		}
	}
	if woke == nil {
		t.Fatal("a goblin whose only process sat idle past the budget never woke")
	}
	if !now.After(time.Date(2026, 9, 25, 18, 9, 0, 0, time.UTC)) {
		t.Fatalf("woke at %s, inside the busy budget", now.Format(time.Kitchen))
	}
	for _, want := range []string{string(AwaitingAnswer), "no progress evidence (transcript write or processor use by its own processes) for", "node.exe (pid 42)"} {
		if !strings.Contains(woke.Detail, want) {
			t.Errorf("wake detail %q lacks %q", woke.Detail, want)
		}
	}
}

// A blocked goblin is parked on a permission or approval dialog and cannot
// resume by itself when its own job reports back, so a job it left running
// does not hold the wake.
func TestBlockedGoblinWithAMovingJobOfItsOwnWakesAtOnce(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"go.exe (pid 46)"}, JobCPU: time.Minute}

	r := scanStatus(t, service, probe, meta, herdr.AgentBlocked, &now, time.Minute)
	if r.Event == nil || r.Observations[0].Reason != AwaitingAnswer {
		t.Fatalf("a blocked goblin with its own job running = %+v, want an awaiting-answer wake at once", r)
	}
}

// The idle reading of the same goblin, once the Overlord has looked at its
// pane, must not stall on its unchanged counters either.
func TestIdleGoblinWaitingOnItsOwnMovingJobDoesNotStall(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"python.exe (pid 43)"}}

	for range 30 {
		progress.sample.JobCPU += 20 * time.Second
		if r := scanStatus(t, service, probe, meta, herdr.AgentIdle, &now, time.Minute); r.Event != nil {
			t.Fatalf("an idle goblin whose own job is using the processor stalled at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
}

// The idle reading of a goblin whose own process sat idle past the budget
// wakes unchanged_idle naming that process, as the ended turn does.
func TestIdleGoblinWithAnIdleProcessOfItsOwnWakesNamingIt(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.sample = ProgressSample{TranscriptAt: now, Jobs: []string{"node.exe (pid 45)"}, JobCPU: time.Second}

	var woke *Event
	for range 20 {
		if r := scanStatus(t, service, probe, meta, herdr.AgentIdle, &now, time.Minute); r.Event != nil {
			woke = r.Event
			break
		}
	}
	if woke == nil {
		t.Fatal("an idle goblin whose only process sat idle past the budget never woke")
	}
	if !now.After(time.Date(2026, 9, 25, 18, 10, 0, 0, time.UTC)) {
		t.Fatalf("woke at %s, inside the busy budget", now.Format(time.Kitchen))
	}
	for _, want := range []string{string(UnchangedIdle), "no liveness signal", "node.exe (pid 45)"} {
		if !strings.Contains(woke.Detail, want) {
			t.Errorf("wake detail %q lacks %q", woke.Detail, want)
		}
	}
}

// Evidence that cannot be read is no evidence: the monitor wakes exactly as it
// did before there was any, and says why, rather than trusting the silence.
func TestUnreadableProgressEvidenceStillWakes(t *testing.T) {
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	progress.err = errors.New("pane process-info refused")

	scanWorking(t, service, probe, meta, &now, 0)
	r := scanWorking(t, service, probe, meta, &now, 11*time.Minute)
	if r.Event == nil || !strings.Contains(r.Event.Detail, "progress evidence unreadable") {
		t.Fatalf("working past budget with unreadable evidence = %+v, want a wake that says so", r.Event)
	}

	done, doneProbe, doneProgress, doneMeta := progressService(t, &now)
	doneProgress.err = progress.err
	if r := scanStatus(t, done, doneProbe, doneMeta, herdr.AgentDone, &now, time.Minute); r.Event == nil || !strings.Contains(r.Event.Detail, "progress evidence unreadable") {
		t.Fatalf("an ended turn with unreadable evidence = %+v, want a wake that says so", r.Event)
	}
}

// The noise of 2026-09-27: a goblin plainly working in its gate's test step
// woke the CFO every few minutes, because each scan on which Herdr's
// process-info timed out threw away the transcript already read. What could
// be read still counts: a transcript that keeps being written is progress
// whether or not the goblin's processes could be listed.
func TestAMovingTranscriptHoldsTheWakeWhenProcessesCannotBeRead(t *testing.T) {
	// Arrange
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	timeout := errors.New("herdr: pane process-info for default:w9:pBA: context deadline exceeded")
	scanWorking(t, service, probe, meta, &now, 0)

	for scan := range 12 {
		// Act
		progress.sample.TranscriptAt = now.Add(4 * time.Minute)
		progress.err = nil
		if scan%2 == 0 {
			progress.err = timeout
		}
		r := scanWorking(t, service, probe, meta, &now, 5*time.Minute)

		// Assert
		if r.Event != nil {
			t.Fatalf("a goblin writing its transcript woke the CFO at %s: %+v", now.Format(time.Kitchen), r.Event)
		}
	}
}

// A wedge the monitor loses sight of for one scan, such as a gate probe that
// timed out beside a fresh transcript, is not new when it is seen again: the
// CFO already has it. The same busy wake for the same task waits a whole
// budget after the last one, and a different one is raised at once.
func TestTheSameBusyWakeWaitsABudgetBeforeItIsRaisedAgain(t *testing.T) {
	// Arrange
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	gate := &fakeGate{sample: GateSample{Active: true, Step: "test", ActiveFor: 70 * time.Minute}}
	service.Gate = gate
	scanWorking(t, service, probe, meta, &now, 0)
	first := scanWorking(t, service, probe, meta, &now, 11*time.Minute)
	if first.Event == nil || !strings.Contains(first.Event.Detail, "gate step test") {
		t.Fatalf("a gate step past the budget = %+v, want a wake naming it", first.Event)
	}
	publish(t, service, first.Event)
	lostSight := func() {
		gate.err = errors.New("no-mistakes axi status: context deadline exceeded")
		progress.sample.TranscriptAt = now.Add(time.Minute)
		if r := scanWorking(t, service, probe, meta, &now, 2*time.Minute); r.Event != nil {
			t.Fatalf("a scan with a fresh transcript woke: %+v", r.Event)
		}
		gate.err = nil
	}

	// Act
	var repeats []string
	for range 2 {
		lostSight()
		if r := scanWorking(t, service, probe, meta, &now, 2*time.Minute); r.Event != nil {
			repeats = append(repeats, now.Format(time.Kitchen)+": "+r.Event.Detail)
			publish(t, service, r.Event)
		}
	}
	lostSight()
	later := scanWorking(t, service, probe, meta, &now, 2*time.Minute)

	// Assert
	if len(repeats) != 0 {
		t.Errorf("the same wedge was raised again within the budget: %q", repeats)
	}
	if later.Event == nil {
		t.Errorf("the same wedge seen a whole budget after its wake was not raised again")
	}
}

func TestADifferentBusyWakeIsRaisedAtOnce(t *testing.T) {
	// Arrange
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	service, probe, progress, meta := progressService(t, &now)
	gate := &fakeGate{sample: GateSample{Active: true, Step: "test", ActiveFor: 70 * time.Minute}}
	service.Gate = gate
	scanWorking(t, service, probe, meta, &now, 0)
	first := scanWorking(t, service, probe, meta, &now, 11*time.Minute)
	publish(t, service, first.Event)
	gate.err = errors.New("no-mistakes axi status: context deadline exceeded")
	progress.sample.TranscriptAt = now.Add(time.Minute)
	scanWorking(t, service, probe, meta, &now, 2*time.Minute)
	gate.err = nil
	gate.sample = GateSample{Active: true, Step: "ci", ActiveFor: 80 * time.Minute, NoCI: true}

	// Act
	r := scanWorking(t, service, probe, meta, &now, 2*time.Minute)

	// Assert
	if r.Event == nil || !strings.Contains(r.Event.Detail, "gate step ci") {
		t.Fatalf("a wedge in another gate step = %+v, want it raised at once", r.Event)
	}
}

// Herdr's process-info failing leaves the transcript readable, and the prober
// hands back what it read beside the error.
func TestHostProgressKeepsTheTranscriptWhenProcessInfoFails(t *testing.T) {
	// Arrange
	home := t.TempDir()
	transcript := filepath.Join(home, ".claude", "projects", "C--dev-app", "session-1.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	written := time.Date(2026, 9, 27, 9, 44, 0, 0, time.UTC)
	if err := os.Chtimes(transcript, written, written); err != nil {
		t.Fatal(err)
	}
	prober := HostProgress{Panes: failingPanes{errors.New("context deadline exceeded")}, Home: home}

	// Act
	progress, err := prober.InspectProgress(context.Background(), state.TaskMeta{}, EndpointSample{Harness: "claude", Session: "session-1"})

	// Assert
	if err == nil || !progress.TranscriptAt.Equal(written) {
		t.Fatalf("InspectProgress = %v, %v; want the transcript written at %v beside the process-info error", progress.TranscriptAt, err, written)
	}
}

// On 2026-09-29 the monitor woke the CFO about two native Codex goblins with
// no progress evidence for an hour while both were working. Codex keeps its
// rollout open and appends to it, and each rollout's write time stayed about
// ninety seconds after its creation for hours while its entries ran on, read
// alike with os.Stat and from an open handle. Every harness stamps each
// transcript entry, so the last complete entry says when the harness last
// wrote, whatever the file's write time says.
func TestTranscriptProgressIsReadFromItsLastEntry(t *testing.T) {
	const session = "01a0ed2e-53e9-7e83-9911-5091a162c513"
	frozen := time.Date(2026, 9, 29, 12, 41, 27, 0, time.UTC)
	last := time.Date(2026, 9, 29, 15, 22, 49, 970_000_000, time.UTC)
	for name, test := range map[string]struct {
		harness, path string
		entries       []string
	}{
		"codex": {"codex", filepath.Join(".codex", "sessions", "2026", "09", "29", "rollout-2026-09-29T07-40-31-"+session+".jsonl"), []string{
			`{"timestamp":"2026-09-29T15:22:31.251Z","ordinal":2499,"type":"event_msg","payload":{"type":"agent_message"}}`,
			`{"timestamp":"2026-09-29T15:22:49.970Z","ordinal":2500,"type":"event_msg","payload":{"type":"task_complete"}}`,
		}},
		"claude": {"claude", filepath.Join(".claude", "projects", "C--dev-app", session+".jsonl"), []string{
			`{"parentUuid":"a1","message":{"role":"user"},"timestamp":"2026-09-29T15:22:31.251Z"}`,
			`{"parentUuid":"a2","message":{"role":"assistant"},"timestamp":"2026-09-29T15:22:49.970Z"}`,
		}},
		"pi": {"pi", filepath.Join(".pi", "agent", "sessions", "--C--dev-app--", "2026-09-29T12-40-31-000Z_"+session+".jsonl"), []string{
			`{"type":"message","id":"107df141","timestamp":"2026-09-29T15:22:31.251Z","message":{"role":"user"}}`,
			`{"type":"message","id":"107df142","timestamp":"2026-09-29T15:22:49.970Z","message":{"role":"assistant"}}`,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			transcript := filepath.Join(home, test.path)
			if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
				t.Fatal(err)
			}
			writer, err := os.OpenFile(transcript, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			// The harness is part way through writing its next entry.
			if _, err := writer.WriteString(strings.Join(test.entries, "\n") + "\n" + `{"timestamp":"2026-09-29T15:23:`); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(transcript, frozen, frozen); err != nil {
				t.Fatal(err)
			}
			prober := HostProgress{Panes: failingPanes{errors.New("no pane")}, Home: home}

			// Act
			progress, _ := prober.InspectProgress(context.Background(), state.TaskMeta{}, EndpointSample{Harness: test.harness, Session: session})

			// Assert
			if !progress.TranscriptAt.Equal(last) {
				t.Fatalf("TranscriptAt = %v, want %v, the last complete entry's, not the frozen write time %v", progress.TranscriptAt, last, frozen)
			}
		})
	}
}

type failingPanes struct{ err error }

func (f failingPanes) PaneProcessInfo(context.Context, herdr.Target) (herdr.PaneProcessInfo, error) {
	return herdr.PaneProcessInfo{}, f.err
}

func TestHarnessJobsCountsOnlyWorkStartedAfterLaunch(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	table := processTable{
		{PID: 10, ParentPID: 1, ExeBase: "claude.exe"}:  {start: launched, cpu: time.Hour},
		{PID: 11, ParentPID: 10, ExeBase: "cmd.exe"}:    {start: launched.Add(13 * time.Second)},
		{PID: 12, ParentPID: 11, ExeBase: "python.exe"}: {start: launched.Add(14 * time.Second), cpu: 7 * time.Second},
		{PID: 13, ParentPID: 10, ExeBase: "bash.exe"}:   {start: launched.Add(20 * time.Minute), cpu: time.Second},
		{PID: 14, ParentPID: 13, ExeBase: "go.exe"}:     {start: launched.Add(21 * time.Minute), cpu: 40 * time.Second},
		// A process id the harness's own child once had, now reused by a
		// process older than the harness: never its child.
		{PID: 15, ParentPID: 10, ExeBase: "svchost.exe"}: {start: launched.Add(-time.Hour), cpu: time.Hour},
	}

	jobs, used := harnessJobs(10, table.entries(), harnessLaunch, table.start, table.cpu)
	if !slices.Equal(jobs, []string{"bash.exe (pid 13)"}) {
		t.Errorf("jobs = %v, want only the bash the harness started after launching", jobs)
	}
	if used != 41*time.Second {
		t.Errorf("processor time = %s, want the job and its child's 41s, not the MCP server's or the harness's own", used)
	}
}

func TestHarnessJobsWalksThroughALaunchShim(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	table := processTable{
		{PID: 20, ParentPID: 1, ExeBase: "node.exe"}:        {start: launched},
		{PID: 21, ParentPID: 20, ExeBase: "codex.exe"}:      {start: launched.Add(time.Second)},
		{PID: 22, ParentPID: 21, ExeBase: "node.exe"}:       {start: launched.Add(3 * time.Second)},
		{PID: 23, ParentPID: 21, ExeBase: "powershell.exe"}: {start: launched.Add(5 * time.Minute), cpu: 2 * time.Second},
	}

	jobs, _ := harnessJobs(20, table.entries(), harnessLaunch, table.start, table.cpu)
	if !slices.Equal(jobs, []string{"powershell.exe (pid 23)"}) {
		t.Errorf("jobs = %v, want the command the codex binary behind its node shim is running", jobs)
	}
}

// PowerShell is a shell, not a harness launcher: whatever it started as it
// opened belongs to it, and is not walked through as though it were the harness.
func TestHarnessJobsDoesNotWalkThroughPowerShell(t *testing.T) {
	launched := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	table := processTable{
		{PID: 30, ParentPID: 1, ExeBase: "pwsh.exe"}:    {start: launched},
		{PID: 31, ParentPID: 30, ExeBase: "claude.exe"}: {start: launched.Add(time.Second)},
		{PID: 32, ParentPID: 31, ExeBase: "bash.exe"}:   {start: launched.Add(5 * time.Minute), cpu: 2 * time.Second},
	}

	jobs, _ := harnessJobs(30, table.entries(), harnessLaunch, table.start, table.cpu)
	if len(jobs) != 0 {
		t.Errorf("jobs = %v, want none: pwsh is the foreground program and its child started with it", jobs)
	}
}

func TestTranscriptAtFindsEachHarnessTranscript(t *testing.T) {
	home := t.TempDir()
	written := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)
	transcript := func(at time.Time, parts ...string) {
		t.Helper()
		path := filepath.Join(append([]string{home}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	transcript(written, ".claude", "projects", "C--work-g1", "a1b2.jsonl")
	transcript(written.Add(5*time.Minute), ".claude", "projects", "C--work-g1", "a1b2", "subagents", "agent-1.jsonl")
	transcript(written, ".codex", "sessions", "2026", "09", "26", "rollout-2026-09-26T09-00-00-c3d4.jsonl")
	transcript(written, ".pi", "agent", "sessions", "--C--work-g1--", "2026-09-26T09-00-00-000Z_e5f6.jsonl")

	for _, tc := range []struct {
		harness, session string
		want             time.Time
	}{
		{"claude", "a1b2", written.Add(5 * time.Minute)},
		{"codex", "c3d4", written},
		{"pi", "e5f6", written},
		{"kimi", "a1b2", time.Time{}},
		{"claude", "missing", time.Time{}},
		{"claude", `..\a1b2`, time.Time{}},
		{"claude", "*", time.Time{}},
	} {
		if got := transcriptAt(home, tc.harness, tc.session); !got.Equal(tc.want) {
			t.Errorf("transcriptAt(%s, %q) = %s, want %s", tc.harness, tc.session, got, tc.want)
		}
	}
}

type processFacts struct {
	start time.Time
	cpu   time.Duration
}

type processTable map[proc.Entry]processFacts

func (table processTable) entries() []proc.Entry {
	entries := make([]proc.Entry, 0, len(table))
	for entry := range table {
		entries = append(entries, entry)
	}
	return entries
}

func (table processTable) facts(pid int) (processFacts, bool) {
	for entry, facts := range table {
		if entry.PID == pid {
			return facts, true
		}
	}
	return processFacts{}, false
}

func (table processTable) start(pid int) (time.Time, bool) {
	facts, ok := table.facts(pid)
	return facts.start, ok
}

func (table processTable) cpu(pid int) (time.Duration, bool) {
	facts, ok := table.facts(pid)
	return facts.cpu, ok
}
