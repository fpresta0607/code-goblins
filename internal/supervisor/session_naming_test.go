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

// A kept conversation names a goblin only for the generation and harness it
// was proved for, so a record left from an earlier generation or harness
// attaches no session, and naming one fabricates no hook lifecycle.
func TestAKeptConversationNamesOnlyItsOwnGenerationAndHarness(t *testing.T) {
	for _, test := range []struct {
		name, generation, harness, want string
	}{
		{"current conversation", "g1", "codex", "codex/proved"},
		{"prior generation", "g0", "codex", ""},
		{"prior harness", "g1", "claude", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, home := testStore(t)
			meta, err := state.ReadTaskMeta(home.State, "task-1")
			if err != nil {
				t.Fatal(err)
			}
			meta.Backend = "native"
			if err := state.WriteTaskMeta(home.State, meta); err != nil {
				t.Fatal(err)
			}
			if err := state.WriteConversation(home.State, state.Conversation{ID: meta.ID, Generation: test.generation, Harness: test.harness, Session: "proved", ProvedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store}

			// Act
			snapshot, err := service.Snapshot()

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Tasks) != 1 || snapshot.Tasks[0].Session != test.want {
				t.Fatalf("task association = %+v, want %q", snapshot.Tasks, test.want)
			}
			if test.want != "" && (len(snapshot.Sessions) != 1 || snapshot.Sessions[0].ID != test.want || snapshot.Sessions[0].Generation != meta.SpawnGen || snapshot.Sessions[0].Runtime.State == "busy" || snapshot.Sessions[0].Runtime.State == "idle") {
				t.Fatalf("the named session claims runtime it has no evidence for: %+v", snapshot.Sessions)
			}
			if len(store.Snapshot().TaskSessions) != 0 {
				t.Fatal("naming a kept conversation fabricated hook lifecycle evidence")
			}
		})
	}
}

// A restart keeps the kept conversation's name, a relaunch into a new
// generation drops it until that generation's own read proves one again, and
// a late session start in the ledger takes over from it.
func TestAKeptConversationSurvivesRestartAndYieldsToLateHooks(t *testing.T) {
	// Arrange
	store, home := testStore(t)
	meta, err := state.ReadTaskMeta(home.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Backend = "native"
	if err := state.WriteTaskMeta(home.State, meta); err != nil {
		t.Fatal(err)
	}
	if err := keepConversation(home.State, meta, "proved"); err != nil {
		t.Fatal(err)
	}
	store, err = Open(home)
	if err != nil {
		t.Fatal(err)
	}

	// Act and Assert
	before, err := (&Service{Store: store}).Snapshot()
	if err != nil || len(before.Sessions) != 1 || before.Tasks[0].Session != "codex/proved" {
		t.Fatalf("restart lost the kept identity: %+v, %v", before.Sessions, err)
	}
	meta.SpawnGen = "g2"
	if err := state.WriteTaskMeta(home.State, meta); err != nil {
		t.Fatal(err)
	}
	replaced, err := (&Service{Store: store}).Snapshot()
	if err != nil || len(replaced.Sessions) != 0 || replaced.Tasks[0].Session != "" {
		t.Fatalf("a relaunch attached the prior generation's session: %+v, %v", replaced.Sessions, err)
	}
	if err := keepConversation(home.State, meta, "proved"); err != nil {
		t.Fatal(err)
	}
	resumed, err := (&Service{Store: store}).Snapshot()
	if err != nil || len(resumed.Sessions) != 1 || resumed.Sessions[0].Generation != "g2" {
		t.Fatalf("resume lost the re-proved identity: %+v, %v", resumed.Sessions, err)
	}
	heard := event(t, home, "SessionStart", "late", "", time.Now().UTC())
	heard.Harness, heard.Generation = meta.Harness, meta.SpawnGen
	if err := store.Accept(heard); err != nil {
		t.Fatal(err)
	}
	after, err := (&Service{Store: store}).Snapshot()
	if err != nil || len(after.Sessions) != 1 || after.Tasks[0].Session != "codex/late" || after.Sessions[0].Phase != "started" {
		t.Fatalf("a late hook did not replace the kept identity: %+v, %v", after.Sessions, err)
	}
}
