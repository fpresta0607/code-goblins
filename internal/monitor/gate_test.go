package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

type fakeGate struct {
	sample GateSample
	err    error
	calls  int
}

func (f *fakeGate) InspectGate(_ context.Context, _ state.TaskMeta) (GateSample, error) {
	f.calls++
	return f.sample, f.err
}

// The live shape this was built from: every step through pr completed, ci
// polling for checks a repo with no workflows will never produce.
const wedgedStatus = `run:
  id: "01M1AXJC7RP0ZGEFKBRCY6KE3Z"
  status: running
  findings: none
  steps[9]{step,status,findings,duration_ms}:
    pr,completed,0,28338
    ci,running,0,0
  active_steps[1]{step,status,active_for,last_activity,agent_pid,round}:
    ci,running,6h3m,"1m51s ago: log: no CI checks reported yet, waiting for checks to register...","",starting
branch_sync:
  state: behind
`

func TestParseGateStatusReadsActiveStep(t *testing.T) {
	got := parseGateStatus(wedgedStatus, GateSample{NoCI: true})
	if !got.Active || got.Step != "ci" || got.ActiveFor != 6*time.Hour+3*time.Minute {
		t.Fatalf("parsed = %+v, want active ci at 6h3m", got)
	}
	if !strings.Contains(got.LastActivity, "no CI checks reported yet") {
		t.Errorf("last activity = %q", got.LastActivity)
	}
	if got := parseGateStatus("run:\n  status: completed\n", GateSample{}); got.Active {
		t.Errorf("completed run parsed as active: %+v", got)
	}
}

// A goblin driving a gate repair round has an active step whose agent is
// fixing what the step found, and axi status names each column of its row
// in the block's header, which gained round_active_for after the format the
// parser was written for. Read live on 2026-09-30 from a run in a ci fix
// round.
func TestParseGateStatusReadsAStepBeingFixedInTheCurrentFormat(t *testing.T) {
	for name, test := range map[string]struct {
		row          string
		wantActive   time.Duration
		wantActivity string
	}{
		"a step whose agent is fixing it":                    {`ci,fixing,19m24s,36s,"1s ago: claude producing output","48204",fix 1`, 19*time.Minute + 24*time.Second, "1s ago: claude producing output"},
		"a running step":                                     {`test,running,22m39s,22m39s,"quiet 22m30s ago: log: running tests: cfo gate test","",starting`, 22*time.Minute + 39*time.Second, "quiet 22m30s ago: log: running tests: cfo gate test"},
		"a fixing step whose activity ends in a quote":       {`ci,fixing,19m24s,36s,"1s ago: log: running \"go test ./...\"","48204",fix 1`, 19*time.Minute + 24*time.Second, `1s ago: log: running "go test ./..."`},
		"a fixing step whose activity quotes before a comma": {`ci,fixing,19m24s,36s,"1s ago: log: got \"x\", retrying","48204",fix 1`, 19*time.Minute + 24*time.Second, `1s ago: log: got "x", retrying`},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			status := "run:\n  status: running\n  active_steps[1]{step,status,active_for,round_active_for,last_activity,agent_pid,round}:\n    " + test.row + "\nbranch_sync:\n  state: pipeline_owned\n"

			// Act
			got := parseGateStatus(status, GateSample{})

			// Assert
			if !got.Active || got.ActiveFor != test.wantActive || got.LastActivity != test.wantActivity {
				t.Fatalf("parsed = %+v, want active for %v with last activity %q", got, test.wantActive, test.wantActivity)
			}
		})
	}
}

func scanWorking(t *testing.T, service Service, probe *fakeProber, meta state.TaskMeta, now *time.Time, step time.Duration) ScanResult {
	t.Helper()
	probe.samples[meta.ID] = sampleForStatus(meta, StatusWorking, "Working...")
	*now = now.Add(step)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A goblin wedged in a gate step reads working forever. Past the busy budget
// the monitor must consult the gate and wake once with the step named and,
// for a ci step on a repo with no workflows, the abort spelled out.
func TestWorkingPastBudgetWakesOnWedgedGate(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	gate := &fakeGate{sample: GateSample{Active: true, Step: "ci", ActiveFor: 6 * time.Hour,
		LastActivity: "no CI checks reported yet", NoCI: true}}
	service := testService(stateDir, probe, &now)
	service.Gate = gate

	if r := scanWorking(t, service, probe, meta, &now, 0); r.Event != nil {
		t.Fatalf("first working scan woke: %+v", r.Event)
	}
	if r := scanWorking(t, service, probe, meta, &now, 5*time.Minute); r.Event != nil || gate.calls != 0 {
		t.Fatalf("inside budget: event=%+v gate calls=%d, want none", r.Event, gate.calls)
	}
	r := scanWorking(t, service, probe, meta, &now, 6*time.Minute)
	if r.Event == nil {
		t.Fatal("past budget with a wedged gate did not wake")
	}
	for _, want := range []string{"ci", "no .github/workflows", "axi abort"} {
		if !strings.Contains(r.Event.Detail, want) {
			t.Errorf("detail %q lacks %q", r.Event.Detail, want)
		}
	}
	if r.Observations[0].Reason != BusyTurnOverAge {
		t.Errorf("reason = %s, want busy_turn_over_age", r.Observations[0].Reason)
	}
	if _, err := service.Publish(*r.Event); err != nil {
		t.Fatal(err)
	}
	if r := scanWorking(t, service, probe, meta, &now, time.Minute); r.Event != nil {
		t.Fatalf("re-woke for the same wedge: %+v", r.Event)
	}
}

// A gate that is genuinely moving between steps must not wake even when the
// pane has been busy past the budget - a long real review is not a stall.
func TestWorkingPastBudgetStaysQuietWhileGateMoves(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	service := testService(stateDir, probe, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: true, Step: "review", ActiveFor: 2 * time.Minute}}

	scanWorking(t, service, probe, meta, &now, 0)
	if r := scanWorking(t, service, probe, meta, &now, 30*time.Minute); r.Event != nil {
		t.Fatalf("moving gate woke: %+v", r.Event)
	}
}

// Leaving working resets the busy clock, so a goblin that idles and resumes
// gets a fresh budget instead of inheriting the old stretch.
func TestBusyClockResetsWhenAgentLeavesWorking(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{}}
	service := testService(stateDir, probe, &now)
	service.Gate = &fakeGate{sample: GateSample{Active: false}}

	scanWorking(t, service, probe, meta, &now, 0)
	probe.samples["g1"] = sampleForStatus(meta, StatusIdle, "idle")
	now = now.Add(8 * time.Minute)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	// 8m idle + 5m working is past the 10m budget in wall clock, but only 5m
	// of unbroken working: no wake.
	if r := scanWorking(t, service, probe, meta, &now, 5*time.Minute); r.Event != nil {
		t.Fatalf("reset clock still woke: %+v", r.Event)
	}
}

// The monitor asks a long-busy goblin's gate once a minute, and each
// `no-mistakes axi status` starts about ten git processes. One reading of a
// worktree stands for gateProbeEvery exactly as it was read, and each
// worktree is read on its own.
func TestRecentGateProberReadsEachWorktreeOncePerPeriod(t *testing.T) {
	// Arrange
	probe := &fakeGate{sample: GateSample{Active: true, Step: "test", ActiveFor: 10 * time.Minute}}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	gate := &RecentGateProber{Probe: probe, Now: func() time.Time { return now }}
	first := state.TaskMeta{ID: "a", Worktree: `C:\wt\a`}
	second := state.TaskMeta{ID: "b", Worktree: `C:\wt\b`}

	// Act and Assert
	if _, err := gate.InspectGate(context.Background(), first); err != nil || probe.calls != 1 {
		t.Fatalf("first reading: err %v, probes %d; want one probe", err, probe.calls)
	}
	now = now.Add(3 * time.Minute)
	again, err := gate.InspectGate(context.Background(), first)
	if err != nil || probe.calls != 1 || !again.Active || again.Step != "test" || again.ActiveFor != 10*time.Minute {
		t.Fatalf("within the period: %+v, err %v, probes %d; want the first reading unchanged and no new probe", again, err, probe.calls)
	}
	if _, err := gate.InspectGate(context.Background(), second); err != nil || probe.calls != 2 {
		t.Fatalf("another worktree: err %v, probes %d; want its own probe", err, probe.calls)
	}
	now = now.Add(gateProbeEvery)
	if _, err := gate.InspectGate(context.Background(), first); err != nil || probe.calls != 3 {
		t.Fatalf("after the period: err %v, probes %d; want a fresh probe", err, probe.calls)
	}
}

// A kept reading is never aged: a step read at 57m that finishes right after
// still answers 57m within the period, so busyOverAge cannot name a wedge the
// gate never showed.
func TestRecentGateProberDoesNotAgeAKeptReadingPastTheBudget(t *testing.T) {
	// Arrange
	probe := &fakeGate{sample: GateSample{Active: true, Step: "test", ActiveFor: 57 * time.Minute}}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	gate := &RecentGateProber{Probe: probe, Now: func() time.Time { return now }}
	meta := state.TaskMeta{ID: "a", Worktree: `C:\wt\a`}
	if _, err := gate.InspectGate(context.Background(), meta); err != nil {
		t.Fatalf("first reading: %v", err)
	}
	probe.sample = GateSample{}
	now = now.Add(3 * time.Minute)

	// Act
	kept, err := gate.InspectGate(context.Background(), meta)

	// Assert
	if err != nil || probe.calls != 1 || kept.Step != "test" || kept.ActiveFor != 57*time.Minute || kept.ActiveFor >= time.Hour {
		t.Fatalf("kept reading = %+v, err %v, probes %d; want step test at 57m, not past the hour", kept, err, probe.calls)
	}
}
