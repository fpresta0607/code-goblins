package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
)

// scanAndPublish scans once a minute for minutes, publishing each wake the
// way the watcher does, and returns the details of the task wakes published.
// A scan that fails, as one does on a record it cannot write, fails the test.
func scanAndPublish(t *testing.T, service Service, now *time.Time, minutes int) []string {
	t.Helper()
	var woke []string
	for minute := 0; minute < minutes; minute++ {
		result, err := service.Scan(context.Background())
		if err != nil {
			t.Fatalf("scan at minute %d: %v", minute, err)
		}
		if result.Event != nil {
			if _, err := service.Publish(*result.Event); err != nil {
				t.Fatal(err)
			}
			if result.Event.Source == TaskEvent {
				woke = append(woke, result.Event.Detail)
			}
		}
		*now = now.Add(time.Minute)
	}
	return woke
}

// writeRecord writes a goblin's last monitor record.
func writeRecord(t *testing.T, stateDir string, record Observation) {
	t.Helper()
	if err := WriteObservation(stateDir, record); err != nil {
		t.Fatal(err)
	}
}

// 2026-09-30, pd-connect-quickstart: the fleet died at 21:19Z with the
// goblin's terminal gone, its endpoint_missing wake written to its record but
// not yet published, and the clock of the turn the crash cut short still
// running. Once `cfo switch` brought it back, the monitor read it working
// three hours into that old turn, its busy wake found an event already
// pending and returned without stamping its stale timestamps, and the scan
// failed on that record every cycle from then on, so the pending wake that
// would have cleared it was never published. A goblin back from a missing
// endpoint runs a new harness: it starts a new turn, the wake about its
// missing terminal no longer applies, and what it does now is what wakes the
// CFO.
func TestAGoblinBackFromAMissingEndpointIsReadAsItIsNow(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		health Health
		reason Reason
	}{
		{"working", herdr.AgentWorking, HealthBusy, None},
		{"awaiting an answer", herdr.AgentDone, HealthStale, AwaitingAnswer},
		{"idle", herdr.AgentIdle, HealthStale, UnchangedIdle},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			missing := taskEvent(meta.ID, EndpointMissing, "native terminal g1's host does not answer")
			writeRecord(t, stateDir, Observation{
				Schema: Schema, TaskID: meta.ID, Endpoint: endpointString(meta), EndpointVerdict: ProbeMissing,
				Digest: "before the crash", LastObserved: now.Add(-2 * time.Minute), LastSeen: now.Add(-100 * time.Minute), LastProgress: now.Add(-105 * time.Minute),
				BusySince: timePointer(now.Add(-3 * time.Hour)),
				Health:    HealthUnknown, Reason: EndpointMissing, PendingEvent: &missing,
			})
			probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: sampleForStatus(meta, test.status, "back at work")}}
			service := testService(stateDir, probe, &now)

			woke := scanAndPublish(t, service, &now, 8)

			for _, detail := range woke {
				if strings.HasPrefix(detail, string(EndpointMissing)) {
					t.Errorf("a goblin back at its terminal woke the CFO about the terminal being gone: %q", detail)
				}
			}
			record, err := ReadObservation(stateDir, meta.ID)
			if err != nil {
				t.Fatalf("its record does not read back: %v", err)
			}
			if record.Health != test.health || record.Reason != test.reason {
				t.Errorf("record health %s reason %s, want %s %s", record.Health, record.Reason, test.health, test.reason)
			}
			// An unanswered wake is asked again later, so only the first is
			// what the goblin's return raised.
			if test.health == HealthStale && (len(woke) == 0 || !strings.HasPrefix(woke[0], string(test.reason))) {
				t.Errorf("wakes %q, want the first to be %s", woke, test.reason)
			}
			if test.health == HealthBusy && len(woke) != 0 {
				t.Errorf("a goblin working in the turn it began on its return woke the CFO: %q", woke)
			}
		})
	}
}

// A relaunched goblin can be read before its new harness registers: its pane
// is back but its agent is not, so that scan reads its endpoint unknown while
// the wake about its missing terminal still waits behind another goblin's.
// Once it is read working, that wake no longer applies and the turn the crash
// cut short is not the turn it is in.
func TestAGoblinBackFromAMissingEndpointByWayOfAnUnknownOneIsReadAsItIsNow(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	missing := taskEvent(meta.ID, EndpointMissing, "recorded pane pane-g1 is absent")
	writeRecord(t, stateDir, Observation{
		Schema: Schema, TaskID: meta.ID, Endpoint: endpointString(meta), EndpointVerdict: ProbeMissing,
		Digest: "before the crash", LastObserved: now.Add(-2 * time.Minute), LastSeen: now.Add(-100 * time.Minute), LastProgress: now.Add(-105 * time.Minute),
		BusySince: timePointer(now.Add(-3 * time.Hour)),
		Health:    HealthUnknown, Reason: EndpointMissing, PendingEvent: &missing,
	})
	registering := sampleForStatus(meta, herdr.AgentWorking, "registering")
	registering.Agent = herdr.AgentDead
	probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: registering}}
	service := testService(stateDir, probe, &now)
	if _, err := service.Scan(context.Background()); err != nil {
		t.Fatalf("scan before the harness registers: %v", err)
	}
	now = now.Add(time.Minute)
	probe.samples[meta.ID] = sampleForStatus(meta, herdr.AgentWorking, "back at work")

	woke := scanAndPublish(t, service, &now, 8)

	if len(woke) != 0 {
		t.Errorf("a goblin working in the turn it began on its return woke the CFO: %q", woke)
	}
	record, err := ReadObservation(stateDir, meta.ID)
	if err != nil {
		t.Fatalf("its record does not read back: %v", err)
	}
	if record.Health != HealthBusy || record.Reason != None {
		t.Errorf("record health %s reason %s, want %s %s", record.Health, record.Reason, HealthBusy, None)
	}
}

// A goblin already read back at work can still hold the wake about its
// missing terminal, raised before the crash and never published. That wake no
// longer applies whatever the record now says.
func TestAGoblinReadBackAtWorkDoesNotPublishTheWakeAboutItsMissingTerminal(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	meta := metaFor("g1")
	writeTask(t, stateDir, meta)
	missing := taskEvent(meta.ID, EndpointMissing, "recorded pane pane-g1 is absent")
	writeRecord(t, stateDir, Observation{
		Schema: Schema, TaskID: meta.ID, Endpoint: endpointString(meta), EndpointVerdict: ProbePresent,
		Digest: "back at work", LastObserved: now.Add(-time.Minute), LastSeen: now.Add(-time.Minute), LastProgress: now.Add(-time.Minute),
		BusySince: timePointer(now.Add(-time.Minute)),
		Health:    HealthBusy, Reason: None, PendingEvent: &missing,
	})
	probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: sampleForStatus(meta, herdr.AgentWorking, "back at work")}}
	service := testService(stateDir, probe, &now)

	woke := scanAndPublish(t, service, &now, 4)

	if len(woke) != 0 {
		t.Errorf("a goblin back at work woke the CFO: %q", woke)
	}
}

// One wake is published a cycle across the whole fleet, so a goblin's wake
// can still be waiting when its next reading is stale. That reading keeps the
// waiting wake rather than raising a second, but it still records when the
// goblin went stale and when it escalates: a stale record without them is
// refused, and the scan fails on it every cycle.
func TestAStaleReadingWhileAnEarlierWakeWaitsStillRecordsItsStaleTimestamps(t *testing.T) {
	for _, test := range []struct {
		name   string
		status string
		reason Reason
	}{
		{"idle past the stall window", herdr.AgentIdle, UnchangedIdle},
		{"working past the busy budget", herdr.AgentWorking, BusyTurnOverAge},
	} {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			now := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
			meta := metaFor("g1")
			writeTask(t, stateDir, meta)
			refused := taskEvent(meta.ID, HarnessError, "rate_limit: the provider refused the turn")
			writeRecord(t, stateDir, Observation{
				Schema: Schema, TaskID: meta.ID, Endpoint: endpointString(meta), EndpointVerdict: ProbePresent,
				Digest: "refused", LastObserved: now.Add(-time.Minute), LastSeen: now.Add(-time.Minute), LastProgress: now.Add(-time.Hour),
				BusySince: timePointer(now.Add(-time.Hour)), FaultSeen: timePointer(now.Add(-time.Minute)),
				Health: HealthErroring, Reason: HarnessError, DemandDeepInspection: true, PendingEvent: &refused,
			})
			probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: sampleForStatus(meta, test.status, "after the refusal")}}
			service := testService(stateDir, probe, &now)

			for minute := 0; minute < 4; minute++ {
				if _, err := service.Scan(context.Background()); err != nil {
					t.Fatalf("scan at minute %d: %v", minute, err)
				}
				now = now.Add(time.Minute)
			}

			record, err := ReadObservation(stateDir, meta.ID)
			if err != nil {
				t.Fatalf("its record does not read back: %v", err)
			}
			if record.Health != HealthStale || record.Reason != test.reason || record.StaleSince == nil || record.NextEscalation == nil {
				t.Errorf("record health %s reason %s stale since %v next escalation %v, want %s with both timestamps", record.Health, record.Reason, record.StaleSince, record.NextEscalation, test.reason)
			}
			if record.PendingEvent == nil || *record.PendingEvent != refused {
				t.Errorf("pending wake %+v, want the waiting harness_error wake kept", record.PendingEvent)
			}
		})
	}
}
