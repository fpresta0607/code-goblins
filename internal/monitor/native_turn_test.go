package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// turnsFake is a reader that also reports whether the last turn is open, as
// HostProgress does from the harness's own record of the conversation.
type turnsFake struct {
	reply string
	open  bool
	known bool
}

func (f turnsFake) LastReply(context.Context, state.TaskMeta, EndpointSample) string {
	return f.reply
}

func (f turnsFake) TurnOpen(context.Context, state.TaskMeta, EndpointSample) (bool, bool) {
	return f.open, f.known
}

// nativeTask is a goblin in a native terminal, whose task carries no Herdr
// identity of its own.
func nativeTask(id string) state.TaskMeta {
	return state.TaskMeta{ID: id, Backend: "native", Harness: "codex", Worktree: `C:\work\` + id}
}

func nativeSample(meta state.TaskMeta, status, text string) EndpointSample {
	busy := herdr.BusyIdle
	if status == herdr.AgentWorking {
		busy = herdr.BusyWorking
	}
	return EndpointSample{
		Verdict: ProbePresent, TabLabel: "gb-" + meta.ID, Agent: herdr.AgentAlive,
		Capture: capture(text), Harness: meta.Harness, CountersUnavailable: true,
		Status: status, Busy: busy,
	}
}

// nativeTurnScan reads a goblin whose screen first shows work and then shows
// what status says, which is how a redraw that lands mid-turn is seen: the
// working reading is what puts the task past its launch budget.
func nativeTurnScan(t *testing.T, service Service, probe *fakeProber, meta state.TaskMeta, now *time.Time, status, text string) ScanResult {
	t.Helper()
	*now = now.Add(time.Minute)
	probe.samples[meta.ID] = nativeSample(meta, status, text)
	result, err := service.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// nativeTurnFixture is a native goblin already seen working, with the reader
// that says whether its turn is still open.
func nativeTurnFixture(t *testing.T, turns turnsFake) (Service, *fakeProber, state.TaskMeta, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 23, 14, 16, 0, 0, time.UTC)
	clock := &now
	stateDir := t.TempDir()
	meta := nativeTask("task-1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: nativeSample(meta, herdr.AgentWorking, "working")}}
	service := testService(stateDir, probe, clock)
	service.Replies = turns
	if result := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentWorking, "working"); result.Event != nil {
		t.Fatalf("a working goblin woke the CFO: %+v", *result.Event)
	}
	return service, probe, meta, clock
}

// A native screen is one frame of a redraw. A frame caught mid-turn shows the
// input prompt beneath partial commentary while the turn is still working, and
// on 2026-10-23 that frame was read as a finished turn and woke the CFO for an
// answer nobody owed. The harness's own record of the conversation says the
// turn is open, so it is read as work in progress. Issue 1423.
func TestANativeTurnTheHarnessRecordsAsOpenIsNotAnEndedTurn(t *testing.T) {
	// Arrange
	service, probe, meta, clock := nativeTurnFixture(t, turnsFake{open: true, known: true})

	// Act
	result := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, "partial commentary, then the input prompt")

	// Assert
	if result.Event != nil {
		t.Fatalf("a turn the harness records as open woke the CFO as awaiting an answer: %+v", *result.Event)
	}
}

// A turn the harness records as ended still wakes at its prompt, on the first
// reading that shows it, so a genuine wait is never delayed. Issue 1423.
func TestAGenuineNativeTurnEndStillWakesAtOnce(t *testing.T) {
	// Arrange
	service, probe, meta, clock := nativeTurnFixture(t, turnsFake{open: false, known: true})

	// Act
	result := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, "which store should I use")

	// Assert
	if result.Event == nil || !strings.HasPrefix(result.Event.Detail, string(AwaitingAnswer)) {
		t.Fatalf("event = %+v, want the goblin woken as awaiting an answer", result.Event)
	}
}

// Evidence that cannot say whether the turn is open is no evidence: the screen
// reading stands as it was, so a goblin waiting on an answer is never held
// quiet because its record could not be read. Issue 1423.
func TestANativeTurnWithNoEvidenceStillWakesAtItsPrompt(t *testing.T) {
	// Arrange
	service, probe, meta, clock := nativeTurnFixture(t, turnsFake{known: false})

	// Act
	result := nativeTurnScan(t, service, probe, meta, clock, herdr.AgentDone, "which store should I use")

	// Assert
	if result.Event == nil || !strings.HasPrefix(result.Event.Detail, string(AwaitingAnswer)) {
		t.Fatalf("event = %+v, want the goblin woken as awaiting an answer", result.Event)
	}
}

// A goblin in Herdr reports its own agent status, which is not a frame of a
// redraw, so the native screen evidence this holds back does not apply to it.
func TestAHerdrTurnIsJudgedByItsOwnAgentStatus(t *testing.T) {
	// Arrange
	now := time.Date(2026, 10, 23, 14, 16, 0, 0, time.UTC)
	clock := &now
	stateDir := t.TempDir()
	meta := metaFor("task-1")
	writeTask(t, stateDir, meta)
	probe := &fakeProber{samples: map[string]EndpointSample{meta.ID: sampleForStatus(meta, herdr.AgentDone, "which store should I use")}}
	service := testService(stateDir, probe, clock)
	service.Replies = turnsFake{open: true, known: true}

	// Act
	result, err := service.Scan(context.Background())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if result.Event == nil || !strings.HasPrefix(result.Event.Detail, string(AwaitingAnswer)) {
		t.Fatalf("event = %+v, want a Herdr goblin judged by its own agent status", result.Event)
	}
}

// The reader reads turn boundaries from the rollout's own events: a turn that
// started and has not completed is open, one that completed or was aborted is
// ended, and a record with neither says nothing.
func TestCodexTurnStateReadsTheRolloutsOwnTurnBoundaries(t *testing.T) {
	for _, c := range []struct {
		name   string
		events []string
		open   bool
		known  bool
	}{
		{name: "no turn boundary at all", events: []string{`{"type":"session_meta"}`}},
		{name: "a turn still running", events: []string{`{"type":"event_msg","payload":{"type":"task_started"}}`}, open: true, known: true},
		{name: "a turn running after an earlier one ended", events: []string{`{"type":"event_msg","payload":{"type":"task_started"}}`, `{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"done"}}`, `{"type":"event_msg","payload":{"type":"task_started"}}`}, open: true, known: true},
		{name: "a turn that completed", events: []string{`{"type":"event_msg","payload":{"type":"task_started"}}`, `{"type":"event_msg","payload":{"type":"task_complete","last_agent_message":"done"}}`}, known: true},
		{name: "a turn that was aborted", events: []string{`{"type":"event_msg","payload":{"type":"task_started"}}`, `{"type":"event_msg","payload":{"type":"turn_aborted"}}`}, known: true},
		{name: "a prompt handed to the model opens a turn", events: []string{`{"type":"event_msg","payload":{"type":"task_complete"}}`, `{"type":"response_item","payload":{"role":"user"}}`}, open: true, known: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Arrange
			entries := make([][]byte, 0, len(c.events))
			for _, event := range c.events {
				entries = append(entries, []byte(event))
			}

			// Act
			open, known := codexTurnState(entries)

			// Assert
			if open != c.open || known != c.known {
				t.Fatalf("codexTurnState = open %t known %t, want open %t known %t", open, known, c.open, c.known)
			}
		})
	}
}
