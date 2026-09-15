package monitor

import (
	"context"
	"github.com/fpresta0607/code-goblins/internal/state"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

func TestMissingAgentEpisodeSurvivesRestartWithoutRepeatedWake(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Add(time.Hour)
	meta := metaFor("restored-shell")
	writeTask(t, dir, meta)
	sample := sampleFor(meta, herdr.BusyUnknown, "shell")
	sample.Agent = herdr.AgentDead
	probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: sample}}
	service := testService(dir, probe, &now)
	first, err := service.Scan(context.Background())
	if err != nil || first.Event == nil {
		t.Fatalf("first scan = %+v, %v", first, err)
	}
	if first.Observations[0].Reason != AgentMissing {
		t.Fatalf("reason = %s", first.Observations[0].Reason)
	}
	if _, err := service.Publish(*first.Event); err != nil {
		t.Fatal(err)
	}
	restarted := testService(dir, probe, &now)
	again, err := restarted.Scan(context.Background())
	if err != nil || again.Event != nil {
		t.Fatalf("unchanged episode woke again: %+v, %v", again.Event, err)
	}
	probe.samples[meta.ID] = sampleFor(meta, herdr.BusyWorking, "working")
	if _, err := restarted.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	probe.samples[meta.ID] = sample
	next, err := restarted.Scan(context.Background())
	if err != nil || next.Event == nil {
		t.Fatalf("new death did not wake: %+v, %v", next, err)
	}
}

func TestParkedNativeReviewIsVisibleWithoutWorker(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Add(time.Hour)
	meta := metaFor("parked")
	meta.Mode = "no-mistakes"
	writeTask(t, dir, meta)
	sample := sampleFor(meta, herdr.BusyUnknown, "restored shell")
	sample.Agent = herdr.AgentDead
	service := testService(dir, &fakeProber{samples: map[string]EndpointSample{meta.ID: sample}}, &now)
	service.Gate = &fakeGate{sample: GateSample{RunID: "native", Status: "running", Step: "review", Parked: true}}
	first, err := service.Scan(context.Background())
	if err != nil || first.Event == nil || first.Observations[0].Gate == nil || !first.Observations[0].Gate.Parked || first.Observations[0].Reason != AgentMissing {
		t.Fatalf("collapsed gate/worker state: %+v %v", first, err)
	}
	if _, err := service.Publish(*first.Event); err != nil {
		t.Fatal(err)
	}
	next, err := service.Scan(context.Background())
	if err != nil || next.Event != nil {
		t.Fatalf("unchanged gate replayed: %+v %v", next.Event, err)
	}
}

type slowTaskProbe struct {
	slowID   string
	released chan struct{}
}

func (p slowTaskProbe) Inspect(ctx context.Context, meta state.TaskMeta) (EndpointSample, error) {
	if meta.ID == p.slowID {
		select {
		case <-p.released:
		case <-ctx.Done():
			return EndpointSample{}, ctx.Err()
		}
	}
	return sampleFor(meta, herdr.BusyWorking, "healthy"), nil
}
func TestSlowProbeDoesNotDelayIndependentObservation(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	for _, id := range []string{"slow", "healthy"} {
		writeTask(t, dir, metaFor(id))
	}
	released := make(chan struct{})
	service := testService(dir, slowTaskProbe{slowID: "slow", released: released}, &now)
	done := make(chan error, 1)
	go func() { _, err := service.Scan(context.Background()); done <- err }()
	deadline := time.Now().Add(time.Second)
	var observed bool
	for time.Now().Before(deadline) {
		if _, err := ReadObservation(dir, "healthy"); err == nil {
			observed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(released)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("healthy task waited for slow task")
	}
}
