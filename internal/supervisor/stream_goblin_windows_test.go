package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host/hosttest"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// streamGoblinHolding runs goblin task-1's Claude Code on stream-json pipes,
// waiting for a line or, with isWorking, in a turn, and has the board's
// delivery of his message m1 to it read as one Claude Code holds in its own
// queue, as a line written during a turn is held. It returns when that
// delivery was made.
func streamGoblinHolding(t *testing.T, isWorking bool) (*Store, *Service, time.Time) {
	t.Helper()
	useConversations(t)
	store, home := testStore(t)
	service := &Service{Store: store, Instance: "instance-1", Options: Options{CEO: &CEOConnection{State: home.State}}}
	meta := makeNative(t, home.State, "task-1")
	meta.Harness = "claude"
	if err := state.WriteTaskMeta(home.State, meta); err != nil {
		t.Fatal(err)
	}
	hosttest.RunStream(t, home.State, meta.ID, isWorking)
	messageTo(t, store, meta, "m1", "first words")
	since := time.Now().UTC()
	if err := store.ProcessOne(context.Background(), func(_ context.Context, a Action) (Evaluation, error) {
		return service.behindGoblinsTurn(meta.ID, since, a.Text, ""), nil
	}); err != nil {
		t.Fatal(err)
	}
	if held := actionOf(store, "m1"); !isHarnessHeld(held) {
		t.Fatalf("m1 = %+v; want it held in the goblin's own queue", held)
	}
	return store, service, since
}

// A stream goblin has no send-now key or queue for the board to press, so
// Send now, Edit and Cancel on a message Claude Code holds say so, and the
// message stays held for its next tool call (#2374).
func TestSendNowEditAndCancelRefuseAMessageAStreamGoblinHolds(t *testing.T) {
	for _, test := range []struct {
		name, kind, wantSays string
	}{
		{name: "Send now", kind: "message_now", wantSays: "runs on stream-json pipes, where Claude Code holds the message itself"},
		{name: "Edit or Cancel", kind: "message_withdraw", wantSays: "own queue on stream-json pipes, which Claude Code gives back to nothing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, _ := streamGoblinHolding(t, true)

			// Act
			press := pressOnQueued(service, test.kind, "task-1", "m1", "first words")
			after := actionOf(store, "m1")

			// Assert
			if press.Code != 409 || !strings.Contains(press.Body.String(), test.wantSays) {
				t.Errorf("%s = %d %s; want it refused saying %q", test.name, press.Code, press.Body, test.wantSays)
			}
			if !isHarnessHeld(after) {
				t.Errorf("after %s = %+v; want it still held in the goblin's own queue", test.name, after)
			}
		})
	}
}

// A message a stream goblin holds waits for as long as its host's state
// shows the turn, with no screen to read, and warns only once Claude Code
// waits for a line without having taken it (#2374).
func TestAMessageAStreamGoblinHoldsWaitsOutItsTurnAndWarnsOnlyOnceItWaitsForALine(t *testing.T) {
	for _, test := range []struct {
		name       string
		isWorking  bool
		wantStatus string
	}{
		{name: "in a turn", isWorking: true, wantStatus: "running"},
		{name: "waiting for a line", wantStatus: "uncertain"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, service, since := streamGoblinHolding(t, test.isWorking)

			// Act
			err := store.settleDeliveries(since.Add(deliveryQuiet+time.Second), service.lookAtTerminal)
			settled := actionOf(store, "m1")

			// Assert
			if err != nil || settled.Status != test.wantStatus {
				t.Errorf("settle = %v, m1 = %s %q; want %s", err, settled.Status, settled.Message, test.wantStatus)
			}
		})
	}
}

// A stream goblin's engine changes at the end of its turn as its host's
// state reads it, with no screen to read: once Claude Code waits for a line,
// and never while a turn runs, which is no failed read either (#2374).
func TestAStreamGoblinsEngineChangesOnlyOnceClaudeCodeWaitsForALine(t *testing.T) {
	for _, test := range []struct {
		name         string
		isWorking    bool
		wantSwitches int
	}{
		{name: "waiting for a line", wantSwitches: 1},
		{name: "in a turn", isWorking: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 5*gigabyte, spawner)
			s := handler.Service
			s.Options.FirstRun = newFirstRunMachine(t).run
			s.Options.Gate = &engineGate{}
			meta := state.TaskMeta{ID: "task", SpawnGen: "s1", Harness: "claude", Model: defaultModel, Effort: "xhigh", Backend: "native", Worktree: h.Root, Project: h.Root}
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			gitFixture(t, meta.Worktree)
			hosttest.RunStream(t, h.State, meta.ID, test.isWorking)
			input := map[string]string{"task": meta.ID, "generation": meta.SpawnGen, "harness": "codex", "model": "default", "effort": "high", "when": "turn-end"}
			if response := taskControlRequest(handler, "/api/tasks/engine", input); response.Code != 202 {
				t.Fatalf("deferred choice = %d: %s", response.Code, response.Body)
			}
			now := time.Now().UTC()

			// Act
			for _, elapsed := range []time.Duration{0, 2 * time.Second} {
				if err := s.applyEngineChoices(context.Background(), now.Add(elapsed)); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(time.Minute)
			for len(spawner.recorded()) < test.wantSwitches && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			waitEngineChange(t, s, meta.ID)

			// Assert
			calls := spawner.recorded()
			if len(calls) != test.wantSwitches || test.wantSwitches == 1 && calls[0][0] != "switch" {
				t.Errorf("calls = %+v; want %d switches", calls, test.wantSwitches)
			}
			if test.isWorking {
				if card := engineCard(t, s, meta.ID); card.ActionError != "" || card.PendingEngine == nil {
					t.Errorf("card error %q, pending %+v; want the change waiting with no failed read", card.ActionError, card.PendingEngine)
				}
			}
		})
	}
}
