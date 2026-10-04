package supervisor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCFOCompactStartPreservesHeldWorkAndUnacceptedDelivery(t *testing.T) {
	store, home := testStore(t)
	now := time.Now().UTC().Add(-time.Minute)
	normalize := func(name, source string, at time.Time) nativehook.Event {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"hook_event_name": name, "source": source, "session_id": "same-thread", "turn_id": "same-turn", "cwd": home.Root})
		if err != nil {
			t.Fatal(err)
		}
		event, err := nativehook.Normalize(strings.NewReader(string(payload)), nativehook.Context{Harness: "codex", Role: "cfo", HostID: "fixture-host", Now: at})
		if err != nil {
			t.Fatal(err)
		}
		return event
	}
	for _, event := range []nativehook.Event{normalize("SessionStart", "startup", now), normalize("UserPromptSubmit", "", now.Add(time.Second))} {
		if err := store.Accept(event); err != nil {
			t.Fatal(err)
		}
	}
	store.db.Tasks["task-1"] = Evaluation{Phase: "waiting", Reason: "verified work waits for the owner", WaitingOn: "owner", Generation: "g1", At: now}
	store.db.Actions = append(store.db.Actions, Action{ID: "queued-answer", Kind: "answer", Status: "running", CreatedAt: now, UpdatedAt: now, Awaiting: &Awaiting{Host: "fixture-host", Harness: "codex", Since: now.Add(2 * time.Second), QuietSince: now.Add(2 * time.Second)}})
	store.db.Questions = []Question{{ID: "unread-decision", Status: "open", Text: "A real unread choice"}, {ID: "answered-decision", Status: "answered", AnsweredBy: "overlord", Answer: "Keep the hold"}}
	if err := store.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := wake.Append(home.State, "notify", "unread-decision", "blocked: A real unread choice options: Keep the hold | Finish the proof"); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	beforeWakes, err := wake.Pending(home.State)
	if err != nil || len(beforeWakes) != 1 {
		t.Fatalf("pending decision = %v, %v", beforeWakes, err)
	}
	compact := normalize("SessionStart", "compact", now.Add(3*time.Second))
	for range 2 {
		if err := store.Accept(compact); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.settleDeliveries(now.Add(4*time.Second), func(Awaiting) terminalLook { return terminalLook{Working: true} }); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	after := reopened.Snapshot()
	if !after.Sessions["codex/same-thread"].PromptAt.Equal(before.Sessions["codex/same-thread"].PromptAt) || !reflect.DeepEqual(after.Tasks, before.Tasks) || !reflect.DeepEqual(after.Actions, before.Actions) || !reflect.DeepEqual(after.Questions, before.Questions) {
		t.Fatalf("compact manufactured acceptance or changed held work: before=%+v after=%+v", before, after)
	}
	afterWakes, err := wake.Pending(home.State)
	if err != nil || !reflect.DeepEqual(afterWakes, beforeWakes) {
		t.Fatalf("compact consumed or duplicated the unread decision: %v, %v", afterWakes, err)
	}
}
