package monitor

import (
	"context"
	"strings"
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
	notifyFrom(t, service, stateDir, id, "blocked: "+question)
}

// notifyFrom is park for any notify verb, so a `--failed` notify - which
// `cfo drain` also renders as a decision and refuses to ack unread - can be
// driven through the same path.
func notifyFrom(t *testing.T, service Service, stateDir, id, detail string) {
	t.Helper()
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, id, detail); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(stateDir, "notify", id, detail); err != nil {
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

// A `--failed` notify asks a question ("ci can never pass, abort or fix?")
// exactly as a `--blocked` one does: drain renders both as decisions and
// refuses to ack either unread. Treating failed as terminal left that class
// of question with no re-ask at all and let the streak climb back towards
// hourly. It is terminal only once its record has been acked.
func TestScanReAsksAnUnansweredFailedNotify(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	notifyFrom(t, service, stateDir, "g1", "failed: ci step can never pass; abort or fix?")
	events := cycle(t, service, &now, 60)

	if len(events) < 3 {
		t.Fatalf("re-asks over an hour = %d (%+v), want a failed notify to keep asking", len(events), events)
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
		t.Fatalf("no_change_streak = %d, want 0 while a failed notify is unanswered", heartbeat.NoChangeStreak)
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}
	if events := cycle(t, service, &now, 60); len(events) != 0 {
		t.Fatalf("re-asks after the ack = %+v, want an acked failure to be terminal", events)
	}
}

// herdr reports a pane's activity as momentarily indeterminate as a matter of
// course, and that reading routes an already-stale awaiting-answer goblin
// back through staleObservation. Wiping the re-ask schedule there deferred
// the question by a full interval every time, so a blip arriving faster than
// the interval silenced the goblin entirely - the exact failure being fixed.
func TestScanKeepsTheReAskClockAcrossAnIndeterminateReading(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	done := sampleForStatus(meta, herdr.AgentDone, "same")
	blip := sampleForStatus(meta, herdr.AgentUnknown, "same")
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": done}}
	service := decisionService(stateDir, probe, &now)

	if _, err := wake.Append(stateDir, "notify", "g1", "blocked: merge or hold?"); err != nil {
		t.Fatal(err)
	}

	var events []Event
	for i := 0; i < 30; i++ {
		// Every other cycle reads indeterminate: more often than the two
		// minute re-ask interval this service is configured with.
		probe.samples["g1"] = done
		if i%2 == 1 {
			probe.samples["g1"] = blip
		}
		events = append(events, cycle(t, service, &now, 1)...)
	}

	// The first event is the goblin's own first wake; everything after it is
	// a re-ask that the reset clock used to swallow.
	if len(events) < 3 {
		t.Fatalf("events across the blips = %d (%+v), want the question asked again", len(events), events)
	}
}

// A `--done` notify is a goblin reporting a PR, not asking a question: drain
// acks it without ceremony and never renders it as a DECISION. The terminal
// verb gate releases as soon as herdr advances the pane's state_change_seq,
// and an unacknowledged done record must not turn that release into an hourly
// re-ask for a goblin that asked nothing.
func TestScanNeverReAsksAnInformationalDoneNotify(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	notifyFrom(t, service, stateDir, "g1", "done: PR https://example.test/repo/pull/7")
	if events := cycle(t, service, &now, 1); len(events) != 0 {
		t.Fatalf("a freshly reported done woke the CFO: %+v", events)
	}

	// The pane's own counter advances, which releases the gate the terminal
	// verb was holding, and herdr reports the turn ended again.
	released := sampleForStatus(meta, herdr.AgentDone, "same")
	released.StateChangeSeq++
	probe.samples["g1"] = released

	for _, event := range cycle(t, service, &now, 60) {
		if strings.Contains(event.Detail, "still unanswered") {
			t.Fatalf("re-asked a goblin that reported done and asked nothing: %+v", event)
		}
	}
	heartbeat, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.NoChangeStreak == 0 {
		t.Fatal("no_change_streak = 0, want the fleet free to back off when nobody asked a question")
	}
}

// The other arm: a needs-decision goblin files no notify of its own, so the
// watcher's signal record keyed "<id>.status" is the only evidence it is
// waiting. Drop that arm and this goblin goes silent again, which is the
// original defect.
func TestScanReAsksAGoblinWaitingBehindTheWatchersDecisionSignal(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(stateDir, "g1", "needs-decision: adopt the migration or revert?"); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(stateDir, "signal", "g1.status", "signal:g1.status"); err != nil {
		t.Fatal(err)
	}

	events := cycle(t, service, &now, 60)
	if len(events) < 3 {
		t.Fatalf("re-asks over an hour = %d (%+v), want the goblin behind a decision signal to keep asking", len(events), events)
	}
}

// The backoff has to actually widen and then rest exactly at its cap. A
// constant wearing a function's name satisfies every other test here, and an
// earlier round of this review found a path that silently reset the interval
// to its base.
func TestDecisionAskIntervalWidensThenRestsAtTheCap(t *testing.T) {
	service := Service{DecisionAskAfter: 5 * time.Minute, DecisionAskMax: time.Hour}
	previous := time.Duration(0)
	capped := false
	for asks := 0; asks < 12; asks++ {
		interval := service.decisionAskInterval(asks)
		if interval > service.DecisionAskMax {
			t.Fatalf("interval after %d re-asks = %s, want no longer than the %s cap", asks, interval, service.DecisionAskMax)
		}
		switch {
		case interval == service.DecisionAskMax:
			capped = true
		case capped:
			t.Fatalf("interval after %d re-asks = %s, want it to rest at the %s cap once it reaches it", asks, interval, service.DecisionAskMax)
		case interval <= previous:
			t.Fatalf("interval after %d re-asks = %s, want it wider than the previous %s", asks, interval, previous)
		}
		previous = interval
	}
	if !capped {
		t.Fatal("the interval never reached its cap, so an unanswered question has no guaranteed asking floor")
	}
}
