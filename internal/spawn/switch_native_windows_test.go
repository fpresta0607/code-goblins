package spawn

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// A native goblin switches in place, where switch once refused every task
// that did not run in Herdr: its harness exits on its own command, a new one
// starts in a new terminal under the same id with the new model and a
// handoff, and the task's generation moves on.
func TestANativeGoblinSwitchesInPlace(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if after.Model != "gpt-9" || after.SpawnGen == before.SpawnGen || after.Backend != "native" {
		t.Errorf("after the switch the task is model %q generation %q backend %q; want gpt-9, a new generation and still native", after.Model, after.SpawnGen, after.Backend)
	}
	second, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || second.HostPID == first.HostPID || !host.Running(second) {
		t.Errorf("the terminal after the switch is %+v, %v; want a new running host under the same id", second, err)
	}
	if !ended(first.HostPID) {
		t.Errorf("the first terminal's host pid %d still runs beside its replacement", first.HostPID)
	}
	submitted := submittedLines(t, f, 3)
	if len(submitted) < 3 || submitted[1] != "/exit" || !strings.Contains(submitted[2], result.Handoff) || result.Handoff == "" {
		t.Errorf("submitted = %q with handoff %q; want the instruction, /exit, then the new harness pointed at the handoff", submitted, result.Handoff)
	}
	if launches := len(named(f.events(t), "env")); launches != 2 {
		t.Errorf("the harness started %d times, want twice", launches)
	}
}

// A native goblin whose harness ignores its own exit command has its
// terminal closed, which ends it and everything it started, before the new
// harness starts: two never run side by side.
func TestANativeSwitchClosesAHarnessThatWillNotExit(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/stay"}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Model: "gpt-9"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !ended(first.HostPID) {
		t.Errorf("the first terminal's host pid %d still runs", first.HostPID)
	}
	if second, err := host.ReadRecord(f.stateDir, "task-7"); err != nil || second.HostPID == first.HostPID {
		t.Errorf("the terminal after the switch is %+v, %v; want a new host", second, err)
	}
	if submitted := submittedLines(t, f, 2); !slices.Contains(submitted, "/stay") {
		t.Errorf("submitted = %q, want the ignored exit command tried first", submitted)
	}
}

// A switch of a native goblin to a harness no native terminal can start is
// refused before anything is stopped or recorded: the goblin keeps its
// terminal, its metadata and its generation.
func TestANativeSwitchToAHarnessWithNoNativeScreensLeavesTheGoblinRunning(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{
		harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit"}},
		harness.Kimi:  nativeAdapter{kind: harness.Kimi, control: harness.Control{StopCommand: "/quit"}},
	}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	before, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	awaitComposer(t, f)

	_, err = f.service.Switch(context.Background(), SwitchRequest{ID: "task-7", Harness: harness.Kimi})

	if err == nil || !strings.Contains(err.Error(), "kimi cannot run in a native terminal") || !strings.Contains(err.Error(), "left running") {
		t.Fatalf("Switch err = %v, want a refusal naming kimi and that the goblin was left running", err)
	}
	after, err := state.ReadTaskMeta(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if after.Harness != before.Harness || after.Model != before.Model || after.Effort != before.Effort || after.SpawnGen != before.SpawnGen {
		t.Errorf("after the refusal the task is %s/%s/%s generation %q; want %s/%s/%s generation %q", after.Harness, after.Model, after.Effort, after.SpawnGen, before.Harness, before.Model, before.Effort, before.SpawnGen)
	}
	current, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil || current.HostPID != first.HostPID || !host.Running(current) {
		t.Errorf("the terminal after the refusal is %+v, %v; want the first host %d still running", current, err, first.HostPID)
	}
	if submitted := submittedLines(t, f, 1); slices.Contains(submitted, "/exit") {
		t.Errorf("submitted = %q, want no exit command sent", submitted)
	}
}

// A reboot ends every native terminal. Switching a native goblin whose
// terminal has ended, to what it already ran, starts its harness again under
// the same id with the harness's own resume and the resume instruction.
func TestANativeGoblinWhoseTerminalEndedResumesInPlace(t *testing.T) {
	f := newNativeFixture(t, harness.Codex, "turns")
	f.service.Commands = cleanWorktree{f.service.Worktrees.Commands}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: nativeAdapter{kind: harness.Codex, control: harness.Control{StopCommand: "/exit", ResumeArgs: []string{"resume", "--last"}}}}}
	if _, err := f.service.Spawn(context.Background(), f.request); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	closeCurrentTerminal(t, f)
	first, err := host.ReadRecord(f.stateDir, "task-7")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Close(f.stateDir, first, nativeCloseWait); err != nil {
		t.Fatal(err)
	}

	result, err := f.service.Switch(context.Background(), SwitchRequest{ID: "task-7"})

	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if !result.Resumed || result.Handoff != "" {
		t.Errorf("result = %+v, want the harness resumed with no handoff", result)
	}
	launches := named(f.events(t), "env")
	if len(launches) != 2 || !strings.HasPrefix(launches[1].Text, "resume --last ") {
		t.Fatalf("launches = %+v, want the second with the harness's resume arguments first", launches)
	}
	if submitted := submittedLines(t, f, 2); !strings.Contains(submitted[len(submitted)-1], "Your session was restarted") {
		t.Errorf("submitted = %q, want the resumed harness told to continue", submitted)
	}
}

// cleanWorktree answers a switch's git reads as a worktree with nothing
// uncommitted and no history to hand off, and hands every other command to
// the fixture's runner.
type cleanWorktree struct {
	next execx.Runner
}

func (c cleanWorktree) Run(ctx context.Context, req execx.Request) (execx.Result, error) {
	if req.Name == "git" {
		return execx.Result{}, nil
	}
	return c.next.Run(ctx, req)
}

// closeCurrentTerminal ends whichever terminal task-7 has when the test ends,
// the one a switch started included, which the fixture does not know about.
func closeCurrentTerminal(t *testing.T, f *nativeFixture) {
	t.Helper()
	t.Cleanup(func() {
		if record, err := host.ReadRecord(f.stateDir, "task-7"); err == nil {
			if err := host.Close(f.stateDir, record, nativeCloseWait); err != nil {
				t.Errorf("close the switched terminal: %v", err)
			}
		}
	})
}
