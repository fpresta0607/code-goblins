package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// writeNativeTask records a native task id, whose terminal a test hosts under
// the same id.
func writeNativeTask(t *testing.T, stateDir, id string) state.TaskMeta {
	t.Helper()
	worktree := t.TempDir()
	meta := state.TaskMeta{ID: id, Worktree: worktree, Project: worktree, Harness: "claude", Kind: "ship", Mode: "local-only", Yolo: "no", SpawnGen: "gen-1", Backend: "native"}
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// A native goblin asks, presents and reports from inside its own terminal:
// its program is the proof, where a Herdr goblin's pane is. A process outside
// that terminal cannot ask in its name.
func TestANativeGoblinAsksFromItsOwnTerminalOnly(t *testing.T) {
	stateDir := t.TempDir()
	writeNativeTask(t, stateDir, "task-9")
	goblin := hostTerminal(t, stateDir, "task-9")

	goblin.typeLine(t, "ask task-9")
	_, outside := goblinAsker(context.Background(), stateDir, nil, "task-9")

	if lines := goblin.waitForLines(t, 1); len(lines) != 1 || lines[0] != "asked" {
		t.Errorf("the goblin's own terminal recorded %q, want its question proven", lines)
	}
	if outside == nil || !strings.Contains(outside.Error(), "does not run under it") {
		t.Errorf("a process outside the terminal asked with %v, want it refused", outside)
	}
}

// The Overlord's answer reaches a native goblin through its own terminal:
// typed, submitted once its composer shows it, and delivered once the
// harness works on it, on one line.
func TestTheOverlordsAnswerReachesANativeGoblinOnce(t *testing.T) {
	stateDir := t.TempDir()
	meta := writeNativeTask(t, stateDir, "task-9")
	goblin := hostTerminal(t, stateDir, "task-9")
	goblin.typeLine(t, "harness")

	result, err := (&CFOConnection{State: stateDir}).SendGoblin(context.Background(), "task-9", goblinIdentity(meta), "The Overlord answered your question on the board.\nAnswer: yes")

	if err != nil || !strings.Contains(result.Reason, "native terminal") {
		t.Errorf("SendGoblin = %+v, %v; want it accepted in the native terminal", result, err)
	}
	want := []string{"The Overlord answered your question on the board. Answer: yes"}
	if typed := goblin.exit(t); !slices.Equal(typed, want) {
		t.Errorf("the goblin received %q, want the answer once on one line", typed)
	}
}

// An answer meant for a native goblin that has since restarted is refused
// with nothing typed into its successor's terminal.
func TestAnAnswerForARestartedNativeGoblinIsRefused(t *testing.T) {
	stateDir := t.TempDir()
	earlier := writeNativeTask(t, stateDir, "task-9")
	current := earlier
	current.SpawnGen = "gen-2"
	if err := state.WriteTaskMeta(stateDir, current); err != nil {
		t.Fatal(err)
	}
	goblin := hostTerminal(t, stateDir, "task-9")
	goblin.typeLine(t, "harness")

	_, err := (&CFOConnection{State: stateDir}).SendGoblin(context.Background(), "task-9", goblinIdentity(earlier), "Answer: yes")

	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("SendGoblin error = %v, want a refusal with nothing sent", err)
	}
	if typed := goblin.exit(t); len(typed) != 0 {
		t.Errorf("the restarted goblin received %q, want nothing", typed)
	}
}
