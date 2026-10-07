package spawn

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A native goblin takes a steer the way it took its instruction: typed,
// submitted once its composer shows it, and delivered once it works on it. A
// key reaches its terminal as the console reads it, and a key cfo send does
// not name is refused before anything is sent.
func TestANativeGoblinTakesTextAndKeysThroughItsTerminal(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	textErr := f.service.SendNative(context.Background(), meta, "CFO: run the tests")
	keyErr := f.service.SendNativeKey(meta, "Enter")
	unknownErr := f.service.SendNativeKey(meta, "F5")

	if textErr != nil {
		t.Errorf("SendNative: %v", textErr)
	}
	if keyErr != nil {
		t.Errorf("SendNativeKey Enter: %v", keyErr)
	}
	if unknownErr == nil || !strings.Contains(unknownErr.Error(), "unsupported key") {
		t.Errorf("SendNativeKey F5 = %v, want it refused", unknownErr)
	}
	want := []string{spawnPointer(t, f.fixture), "CFO: run the tests", ""}
	var submitted []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= len(want) {
			break
		}
	}
	if !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, the steer, then an empty line from Enter", submitted)
	}
}

// A slash command reaches a native goblin's harness once, submitted only after
// the wait a completion popup needs, and is reported unconfirmed without
// waiting for a working screen: /exit ends the harness, so nothing afterwards
// can show it ran.
func TestANativeGoblinTakesASlashCommandOnceAndReportsItUnconfirmed(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	f.service.Sleep = func(ctx context.Context, duration time.Duration) error {
		slept = append(slept, duration)
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}

	began := time.Now()
	err = f.service.SendNative(context.Background(), meta, "/exit")
	took := time.Since(began)

	if err == nil || !strings.Contains(err.Error(), "submitted once") || !strings.Contains(err.Error(), "cfo peek task-7") {
		t.Errorf("SendNative /exit = %v, want it reported submitted once but unconfirmed", err)
	}
	if !slices.ContainsFunc(slept, func(duration time.Duration) bool { return duration >= 1200*time.Millisecond }) {
		t.Errorf("waits = %v, want one of at least 1.2s before Enter", slept)
	}
	if took >= nativeReadGrace {
		t.Errorf("SendNative took %s, want it back without waiting on the ended harness's screen", took)
	}
	want := []string{spawnPointer(t, f.fixture), "/exit"}
	var submitted []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= len(want) {
			break
		}
	}
	if !slices.Equal(submitted, want) {
		t.Errorf("submitted = %q, want the instruction, then /exit once", submitted)
	}
}

// A native task whose terminal has ended takes nothing, and says which task.
func TestANativeTaskWithNoTerminalTakesNothing(t *testing.T) {
	service := Service{StateDir: t.TempDir()}
	meta := state.TaskMeta{ID: "task-7", Window: "native", Harness: string(harness.Codex), Backend: "native"}

	textErr := service.SendNative(context.Background(), meta, "CFO: run the tests")
	keyErr := service.SendNativeKey(meta, "Enter")

	for name, err := range map[string]error{"text": textErr, "key": keyErr} {
		if err == nil || !strings.Contains(err.Error(), "native task task-7 has no running terminal") {
			t.Errorf("%s = %v, want it refused naming the task", name, err)
		}
	}
}

// Text typed into a harness already in a turn is queued by the harness, so
// with nothing showing the harness took it the send says it is queued, where
// it once reported it delivered because the screen already showed the harness
// working.
func TestASteerToANativeGoblinInATurnIsQueuedWithoutAHookReport(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	nativeQueuedProof = 500 * time.Millisecond
	t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the next tool call", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A harness in a turn reports a prompt through its hook when it queues the
// text, not when it takes it: Claude Code 2.1.292 ran UserPromptSubmit for a
// steer typed during a tool call ten seconds before it took the steer into
// its turn at the next tool call, live on 2026-10-06. So a hook report never
// proves a delivery to a goblin in a turn, and the steer is reported queued
// for its next tool call, typed and submitted once.
func TestAHookReportDoesNotProveASteerToANativeGoblinInATurn(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	nativeQueuedProof = 500 * time.Millisecond
	t.Cleanup(func() { nativeQueuedProof = 5 * time.Second })
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	f.service.PromptSince = func(string, string, time.Time) (bool, error) { return true, nil }

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")

	if !errors.Is(err, fleet.ErrQueuedForToolCall) {
		t.Errorf("SendNative = %v, want it queued for the goblin's next tool call despite the hook report", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A steer to a goblin in a turn is delivered once the harness's own record of
// the conversation shows it handed to the model, asked for with the task and
// the exact text typed, from before the submit; until then it is queued.
func TestASteerToANativeGoblinInATurnIsProvenByItsRecord(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	type ask struct {
		task, worktree, text string
		since                time.Time
	}
	var asked []ask
	before := time.Now()
	f.service.Took = func(_ context.Context, meta state.TaskMeta, text string, since time.Time) bool {
		asked = append(asked, ask{meta.ID, meta.Worktree, text, since})
		// The fake records a line once it takes it, as a harness's record does.
		return len(named(f.events(t), "submitted")) == 2
	}

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")
	after := time.Now()

	if err != nil {
		t.Errorf("SendNative = %v, want the record to prove it taken", err)
	}
	if len(asked) == 0 || asked[0].task != "task-7" || asked[0].worktree != meta.Worktree || asked[0].text != "CFO: run the tests" || asked[0].since.Before(before) || asked[0].since.After(after) {
		t.Errorf("the record was asked for %+v, want task-7's in %s holding the steer since the send", asked, meta.Worktree)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// submittedLines waits for at least n lines the fake harness recorded as
// submitted and returns them.
func submittedLines(t *testing.T, f *nativeFixture, n int) []string {
	t.Helper()
	var submitted []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		submitted = submitted[:0]
		for _, event := range named(f.events(t), "submitted") {
			submitted = append(submitted, event.Text)
		}
		if len(submitted) >= n {
			break
		}
	}
	return submitted
}

// awaitComposer waits until the fake harness's turn has ended and its
// composer waits for input.
func awaitComposer(t *testing.T, f *nativeFixture) {
	t.Helper()
	record, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	screens, _ := harness.NativeScreens(harness.Codex)
	if _, err := f.service.awaitScreen(context.Background(), record, 30*time.Second, screens.IsReady); err != nil {
		t.Fatalf("the fake harness never ended its turn: %v", err)
	}
}
