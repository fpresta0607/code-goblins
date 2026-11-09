package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// A pi goblin whose ledger holds no session start is still named by its
// generation: the board draws the session its own read of the running harness
// proved and kept, so Modern Chat, the family tree and the goblin's panel all
// find the same session, and a relaunch finds it again (the Overlord's report,
// 2026-11-07: "fix modern caht functionality for pi its nto registerign").
func TestAPiGoblinIsNamedByItsKeptConversation(t *testing.T) {
	// Arrange
	handler, h := orderBoard(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Harness, meta.Backend, meta.SpawnGen = "pi", "native", "pi-generation-1"
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	kept := "01a1b754-986b-7386-8df3-12b245c9034b"
	if err := state.WriteConversation(h.State, state.Conversation{ID: meta.ID, Generation: meta.SpawnGen, Harness: "pi", Session: kept, ProvedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	// Act
	snapshot, err := handler.Service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	named := ""
	for _, task := range snapshot.Tasks {
		if task.ID == meta.ID {
			named = task.Session
		}
	}
	if named != "pi/"+kept {
		t.Fatalf("the task names session %q, want the kept conversation %q", named, "pi/"+kept)
	}
	for _, node := range snapshot.Sessions {
		if node.ID != named {
			continue
		}
		if node.Role != "goblin" || node.TaskID != meta.ID || node.Generation != meta.SpawnGen || node.Harness != "pi" || node.NativeID != kept {
			t.Fatalf("the drawn session = %+v, want the goblin's own of its generation", node)
		}
		return
	}
	t.Fatalf("the snapshot draws no session %q for the goblin", named)
}
