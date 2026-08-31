package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

type fakeGate struct {
	sample GateSample
	calls  int
}

func (f *fakeGate) InspectGate(_ context.Context, _ state.TaskMeta) (GateSample, error) {
	f.calls++
	return f.sample, nil
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

func scanWorking(t *testing.T, service Service, probe *fakeProber, meta state.TaskMeta, now *time.Time, step time.Duration) ScanResult {
	t.Helper()
	probe.samples[meta.ID] = sampleForStatus(meta, herdr.AgentWorking, "Working...")
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
	probe.samples["g1"] = sampleForStatus(meta, herdr.AgentIdle, "idle")
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
