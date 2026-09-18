package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// decisionService is the monitor with a re-ask cadence small enough to drive
// from a fake clock: first re-ask after 2 minutes, doubling, capped at 8.
func decisionService(stateDir string, probe Prober, now *time.Time) Service {
	service := testService(stateDir, probe, now)
	service.DecisionAskAfter = 2 * time.Minute
	service.DecisionAskMax = 8 * time.Minute
	// A working goblin stays working for the whole run: the busy-turn budget
	// is a different wake and would otherwise end these runs as a stall.
	service.BusyTurnMax = 24 * time.Hour
	return service
}

// park drives one scan so the task has an observation, then blocks it with an
// unacknowledged wake record, exactly as `cfo notify <id> --blocked` leaves it.
func park(t *testing.T, service Service, stateDir, id, question string) {
	t.Helper()
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, id, "blocked: "+question); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(stateDir, "notify", id, "blocked: "+question); err != nil {
		t.Fatal(err)
	}
}

// cycle runs count scans a minute apart, publishing whatever the monitor
// produces the way the watcher does, and returns the events it published.
func cycle(t *testing.T, service Service, now *time.Time, count int) []Event {
	t.Helper()
	var events []Event
	for i := 0; i < count; i++ {
		*now = now.Add(time.Minute)
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		if result.Event == nil {
			continue
		}
		events = append(events, *result.Event)
		if _, err := service.Publish(*result.Event); err != nil {
			t.Fatalf("cycle %d publish: %v", i, err)
		}
	}
	return events
}

// The incident: two goblins parked on a CFO decision and waited 8h47m while
// the watcher cycled the whole time. One wake fired, reached nobody, and
// nothing ever asked again, because the monitor treated "emitted once" as
// "answered". An unanswered question must keep asking.
func TestScanReAsksAnUnansweredDecisionInsteadOfGoingQuiet(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	park(t, service, stateDir, "g1", "merge or hold?")
	events := cycle(t, service, &now, 60)

	if len(events) < 3 {
		t.Fatalf("re-asks over an hour = %d (%+v), want the question asked again and again", len(events), events)
	}
	for _, event := range events {
		if event.TaskID != "g1" || event.Source != TaskEvent {
			t.Fatalf("re-ask = %+v, want a task event naming the goblin that is waiting", event)
		}
	}
	heartbeat, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.NoChangeStreak != 0 {
		t.Fatalf("no_change_streak = %d, want 0: quiet-because-waiting is not quiet-because-healthy", heartbeat.NoChangeStreak)
	}
}

// The noise protection the original comment was right to want, kept exactly
// where it belongs: a goblin that is working owes nobody an answer.
func TestScanNeverReAsksAGoblinThatIsWorking(t *testing.T) {
	for _, status := range []string{herdr.AgentWorking, herdr.AgentIdle} {
		t.Run(status, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			sample := sampleForStatus(meta, status, "first")
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
			service := decisionService(stateDir, probe, &now)

			if _, err := wake.Append(stateDir, "notify", "g1", "blocked: unrelated older question"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 60; i++ {
				now = now.Add(time.Minute)
				// Advancing counters keeps an idle pane classified active
				// rather than drifting into stale.
				sample.StateChangeSeq++
				probe.samples["g1"] = sample
				result, err := service.Scan(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if health := result.Observations[0].Health; health != HealthBusy && health != HealthActive {
					t.Fatalf("cycle %d health = %q, want a working goblin", i, health)
				}
				if result.Event != nil {
					t.Fatalf("cycle %d re-asked a working goblin: %+v", i, result.Event)
				}
			}
		})
	}
}

// The amplifier: with no event the streak climbed to 20 and stretched the
// heartbeat to roughly hourly, so a single missed wake became hourly silence.
// While a decision is outstanding the streak must not climb; once the record
// is acked the fleet is genuinely quiet again and it may.
func TestScanHoldsTheNoChangeStreakWhileADecisionIsUnanswered(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	park(t, service, stateDir, "g1", "merge or hold?")
	cycle(t, service, &now, 30)

	heartbeat, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.NoChangeStreak != 0 {
		t.Fatalf("no_change_streak while unanswered = %d, want 0", heartbeat.NoChangeStreak)
	}
	if backoff := heartbeat.NextDue.Sub(heartbeat.LastHeartbeat); backoff > service.heartbeat() {
		t.Fatalf("heartbeat cadence while unanswered = %s, want no longer than the base %s", backoff, service.heartbeat())
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}
	cycle(t, service, &now, 30)

	heartbeat, err = ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.NoChangeStreak == 0 {
		t.Fatal("no_change_streak after the ack = 0, want the fleet free to back off once nobody is waiting")
	}
}

// Surfaced means acknowledged. The ack floor is the ledger's answer column,
// so acking the record is what stops the asking - nothing else.
func TestScanStopsReAskingOnceTheDecisionIsAcked(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	park(t, service, stateDir, "g1", "merge or hold?")
	if len(cycle(t, service, &now, 30)) == 0 {
		t.Fatal("no re-ask before the ack, so the ack proves nothing")
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}
	if events := cycle(t, service, &now, 60); len(events) != 0 {
		t.Fatalf("re-asks after the ack = %+v, want silence once the question is answered", events)
	}
}
