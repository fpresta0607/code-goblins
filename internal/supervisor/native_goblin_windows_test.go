package supervisor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/routing"
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

// makeNative rewrites task id's record as a native task's, the way spawn
// records one.
func makeNative(t *testing.T, stateDir, id string) state.TaskMeta {
	t.Helper()
	meta, err := state.ReadTaskMeta(stateDir, id)
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend, meta.Window = "native", "native"
	meta.HerdrSession, meta.HerdrWorkspaceID, meta.HerdrTabID, meta.HerdrPaneID = "", "", "", ""
	if err := state.WriteTaskMeta(stateDir, meta); err != nil {
		t.Fatal(err)
	}
	return meta
}

// A native goblin asks, presents and reports from inside its own terminal:
// its program is the proof. A process outside that terminal cannot ask in its
// name.
func TestANativeGoblinAsksFromItsOwnTerminalOnly(t *testing.T) {
	stateDir := t.TempDir()
	writeNativeTask(t, stateDir, "task-9")
	goblin := hostTerminal(t, stateDir, "task-9")

	goblin.typeLine(t, "ask task-9")
	_, outside := goblinAsker(stateDir, "task-9")

	if lines := goblin.waitForLines(t, 1); len(lines) != 1 || lines[0] != "asked" {
		t.Errorf("the goblin's own terminal recorded %q, want its question proven", lines)
	}
	if outside == nil || !strings.Contains(outside.Error(), "does not run under it") {
		t.Errorf("a process outside the terminal asked with %v, want it refused", outside)
	}
}

// The Overlord's answer reaches a native goblin through its own terminal:
// typed, submitted once its composer shows it, and delivered once the
// harness works on it, on one line and stamped as a steer so the monitor never
// reads the question it repeats as the harness's own fault.
func TestTheOverlordsAnswerReachesANativeGoblinOnce(t *testing.T) {
	stateDir := t.TempDir()
	meta := writeNativeTask(t, stateDir, "task-9")
	goblin := hostTerminal(t, stateDir, "task-9")
	goblin.typeLine(t, "harness")

	result, err := (&CFOConnection{State: stateDir}).SendGoblin(context.Background(), "task-9", goblinIdentity(meta), "The Overlord answered your question on the board.\nAnswer: yes")

	if err != nil || !strings.Contains(result.Reason, "native terminal") {
		t.Errorf("SendGoblin = %+v, %v; want it accepted in the native terminal", result, err)
	}
	want := []string{routing.SteerPrefix + "The Overlord answered your question on the board. Answer: yes"}
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

// A native goblin's send to its child names it as the receipt's sender when
// the send runs from inside its own terminal. The same send from outside that
// terminal names no sender.
func TestANativeGoblinsSendToItsChildNamesItAsSender(t *testing.T) {
	store, h := testStore(t)
	makeNative(t, h.State, "task-1")
	parentMeta := writeNativeTask(t, h.State, "task-9")
	if err := store.Accept(event(t, h, "SessionStart", "worker", "", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	id := store.db.TaskSessions["task-1"]
	node := store.db.Sessions[id]
	node.Parent = "claude/parent-1"
	store.db.Sessions[id] = node
	store.db.Sessions[node.Parent] = Session{ID: node.Parent, NativeID: "parent-1", Harness: "claude", Role: "goblin", TaskID: "task-9", Generation: parentMeta.SpawnGen, Phase: "active"}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CFO_SESSION_ID", "parent-1")
	t.Setenv("CFO_SESSION_HARNESS", "claude")
	parent := hostTerminal(t, h.State, "task-9")

	parent.typeLine(t, "send gb-task-1")
	lines := parent.waitForLines(t, 1)
	outside := PrepareSendActivity(h, "task-1")()

	if len(lines) != 1 || lines[0] != "sent" {
		t.Fatalf("the parent's terminal recorded %q, want its send received", lines)
	}
	if outside != nil {
		t.Fatal(outside)
	}
	if err := store.ingestActivity(); err != nil {
		t.Fatal(err)
	}
	var sources []string
	for _, a := range store.Snapshot().Activity {
		if a.Kind == "message" {
			sources = append(sources, a.Source)
		}
	}
	slices.Sort(sources)
	if !slices.Equal(sources, []string{"", node.Parent}) {
		t.Errorf("message receipt sources = %q, want the parent for its own send and none from outside its terminal", sources)
	}
}
