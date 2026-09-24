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
// where it belongs. Working is the one reading that suppresses a re-ask even
// with the question still on the ledger: the goblin is executing, typically
// because the CFO answered it. An idle pane is not executing, so what keeps
// it quiet is the record itself - an informational notify is not a question.
func TestScanNeverReAsksAGoblinThatIsWorking(t *testing.T) {
	for _, test := range []struct {
		status string
		record string
	}{
		{herdr.AgentWorking, "blocked: unrelated older question"},
		{herdr.AgentIdle, "done: PR https://example.test/repo/pull/3"},
	} {
		t.Run(test.status, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			sample := sampleForStatus(meta, test.status, "first")
			probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
			service := decisionService(stateDir, probe, &now)

			if _, err := wake.Append(stateDir, "notify", "g1", test.record); err != nil {
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

// The suppression is the pending record, never the status line. A goblin's
// `done:` verb is the last thing it ever wrote and stays there forever, so a
// goblin the CFO acked and then steered back to work with `cfo send` would be
// silenced by a verb from hours ago - the same silence, arriving by a
// different door. Once the notify is acked, a turn that ends at the prompt is
// a standing question again.
func TestScanReAsksAfterAnAckedDoneLeavesTheGoblinWaitingAgain(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	notifyFrom(t, service, stateDir, "g1", "done: PR https://example.test/repo/pull/7")
	cycle(t, service, &now, 1)

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}

	// The CFO steers the acked goblin back to work; its turn then ends at the
	// prompt with a question, and it files no new notify. The `done:` line it
	// wrote earlier is still the latest verb in its status file.
	working := sampleForStatus(meta, herdr.AgentWorking, "moved")
	working.StateChangeSeq++
	probe.samples["g1"] = working
	cycle(t, service, &now, 1)

	waiting := sampleForStatus(meta, herdr.AgentDone, "moved")
	waiting.StateChangeSeq = working.StateChangeSeq + 1
	probe.samples["g1"] = waiting

	reAsks := 0
	for _, event := range cycle(t, service, &now, 60) {
		if strings.Contains(event.Detail, "still unanswered") {
			reAsks++
		}
	}
	if reAsks < 3 {
		t.Fatalf("re-asks over an hour = %d, want a stale done verb to silence nothing once its notify is acked", reAsks)
	}
	heartbeat, err := ReadHeartbeat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.NoChangeStreak != 0 {
		t.Fatalf("no_change_streak = %d, want 0 while the goblin waits", heartbeat.NoChangeStreak)
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

// The failure this change exists to prevent, on the path every other decision
// test misses: a routine state_change_seq advance releases the status-verb
// gate, and a goblin parked on an unanswered question is relabelled active
// between cycles. The question is still on the ledger, so the re-asks must
// survive the relabelling and the streak must stay flat. Every other test
// here pins StateChangeSeq; this one advances it the way herdr does.
func TestScanKeepsReAskingWhenCountersAdvancePastTheStatusVerbGate(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	sample := sampleFor(meta, herdr.BusyIdle, "same")
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sample}}
	service := decisionService(stateDir, probe, &now)

	notifyFrom(t, service, stateDir, "g1", "blocked: merge or hold?")

	var events []Event
	for i := 0; i < 60; i++ {
		now = now.Add(time.Minute)
		sample.StateChangeSeq++
		probe.samples["g1"] = sample
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		if result.Heartbeat.NoChangeStreak != 0 {
			t.Fatalf("cycle %d no_change_streak = %d, want 0 while the question stands", i, result.Heartbeat.NoChangeStreak)
		}
		if result.Event == nil {
			continue
		}
		events = append(events, *result.Event)
		if _, err := service.Publish(*result.Event); err != nil {
			t.Fatalf("cycle %d publish: %v", i, err)
		}
	}

	if len(events) < 3 {
		t.Fatalf("re-asks over an hour = %d (%+v), want the question to survive the counter advance", len(events), events)
	}
	for _, event := range events {
		if !strings.Contains(event.Detail, "still unanswered") {
			t.Fatalf("event = %+v, want every wake here to be a re-ask of the outstanding question", event)
		}
	}
}

// The class a notify-shaped predicate cannot see: a goblin whose turn simply
// ended at its prompt. It filed no notify and wrote no decision verb, so the
// notify arm and the watcher's signal arm both miss it - and the Overlord
// owes it an answer all the same. Its only evidence is the monitor's own
// awaiting-answer stall, and until the predicate counted that, this goblin
// asked once and went quiet. It happened twice on 2026-09-18, to a goblin
// parked on a ruling while the supervision meant to surface it was down.
func TestScanReAsksAGoblinWaitingAtItsPromptWithNoNotify(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentDone, "same")}}
	service := decisionService(stateDir, probe, &now)

	// The ended turn becomes the awaiting-answer stall and the watcher
	// publishes it. Nothing else is ever written for this goblin: no notify,
	// no status verb, no decision signal.
	opening := cycle(t, service, &now, 1)
	if len(opening) != 1 || !strings.Contains(opening[0].Detail, string(AwaitingAnswer)) {
		t.Fatalf("first scan = %+v, want one awaiting-answer stall", opening)
	}

	reAsks := 0
	for _, event := range cycle(t, service, &now, 60) {
		if strings.Contains(event.Detail, "still unanswered") {
			reAsks++
		}
	}
	if reAsks < 3 {
		t.Fatalf("re-asks over an hour = %d, want a goblin waiting at its prompt to keep asking", reAsks)
	}
}

// wake matches the awaiting-answer stall by a literal prefix, because the
// queue stores rendered text and wake cannot import this package. Rename the
// Reason constant and that match stops silently, taking the whole class back
// into the silence this change exists to end. Fail here, loudly, instead.
func TestAwaitingAnswerStallIsRecognisedByWake(t *testing.T) {
	event := taskEvent("g1", AwaitingAnswer, "agent turn ended; waiting on input")
	record := wake.Record{Kind: event.Kind, Key: event.Key, Detail: event.Detail}
	if !wake.AwaitingAnswerStall(record, "g1") {
		t.Fatalf("wake does not recognise the monitor's own awaiting-answer stall: %+v", record)
	}
}

// The re-ask must never become its own evidence. Each re-ask appends another
// stall record for the same goblin, so a predicate that counted those would
// hold the goblin unanswered forever - asking about an answer it had already
// been given. Acking the queue must stop it dead.
func TestScanStopsReAskingTheWaitingPromptOnceItIsAcked(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleForStatus(meta, herdr.AgentDone, "same")}}
	service := decisionService(stateDir, probe, &now)

	cycle(t, service, &now, 1)
	before := 0
	for _, event := range cycle(t, service, &now, 30) {
		if strings.Contains(event.Detail, "still unanswered") {
			before++
		}
	}
	if before == 0 {
		t.Fatal("no re-ask before the ack, so the ack proves nothing")
	}

	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := wake.AckThrough(stateDir, records[len(records)-1].Seq); err != nil {
		t.Fatal(err)
	}
	for _, event := range cycle(t, service, &now, 60) {
		if strings.Contains(event.Detail, "still unanswered") {
			t.Fatalf("re-asked after the ack: %+v", event)
		}
	}
}

// A question the Overlord answered on the board is owed nothing more: the
// goblin has its answer, so the monitor stops re-asking while the record
// waits for the CFO's ordinary ack.
func TestScanStopsReAskingAQuestionAnsweredOnTheBoard(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 18, 2, 40, 49, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{"g1": sampleFor(meta, herdr.BusyIdle, "same")}}
	service := decisionService(stateDir, probe, &now)

	park(t, service, stateDir, "g1", "merge or hold? options: merge | hold")
	if len(cycle(t, service, &now, 30)) == 0 {
		t.Fatal("no re-ask before the answer, so the answer proves nothing")
	}
	records, err := wake.Pending(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if _, ok := wake.BlockingNotify(record); ok && record.Key == "g1" {
			if err := wake.MarkAnswered(stateDir, record.Seq, wake.AnsweredByOverlord, "hold"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, event := range cycle(t, service, &now, 60) {
		if strings.Contains(event.Detail, "still unanswered") {
			t.Fatalf("re-asked after the board answer: %+v", event)
		}
	}
}
