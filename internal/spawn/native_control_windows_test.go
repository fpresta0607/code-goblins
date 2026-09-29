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

// Text typed into a harness already in a turn waits in its composer until
// that turn ends, so with no hook report of the harness taking it the send
// says it waits behind the turn, where it once reported it delivered because
// the screen already showed the harness working.
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

	if !errors.Is(err, fleet.ErrQueuedBehindTurn) {
		t.Errorf("SendNative = %v, want it queued behind the turn", err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Equal(submitted, []string{spawnPointer(t, f.fixture), "CFO: run the tests"}) {
		t.Errorf("submitted = %q, want the steer submitted once after the instruction", submitted)
	}
}

// A harness's own hook report of taking a prompt after the submit proves the
// delivery even while the harness was in a turn, and the report is asked for
// with the task's id and generation, from before the submit.
func TestASteerToANativeGoblinInATurnIsProvenByItsHook(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "")
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	meta, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	before := time.Now()
	f.service.PromptSince = func(taskID, generation string, since time.Time) (bool, error) {
		asked = append(asked, taskID+" "+generation)
		return !since.Before(before), nil
	}

	err = f.service.SendNative(context.Background(), meta, "CFO: run the tests")

	if err != nil {
		t.Errorf("SendNative = %v, want the hook report to prove it taken", err)
	}
	if len(asked) == 0 || asked[0] != "task-7 "+meta.SpawnGen {
		t.Errorf("the hook report was asked for %q, want task-7 in generation %s", asked, meta.SpawnGen)
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
