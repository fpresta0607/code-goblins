package supervisor

import (
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestBoardAnswerToPausedQuestionIsKeptForResume(t *testing.T) {
	store, h := testStore(t)
	meta, notified, runner, connection := goblinFixture(t, store)
	question := surfaced(t, store, meta, notified, connection)
	condition, err := state.NewPauseCondition("question", question.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, Operation: "pause-question", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
		t.Fatal(err)
	}
	action := Action{ID: "answer-1", Kind: "goblin_answer", Generation: question.Identity, QuestionID: question.ID, Text: "SQLite"}
	if _, err := store.Queue(action); err != nil {
		t.Fatal(err)
	}
	service := &Service{Store: store, Options: Options{CFO: connection}}

	if err := store.ProcessOne(t.Context(), service.execute); err != nil {
		t.Fatal(err)
	}

	if len(runner.prompts) != 0 {
		t.Fatalf("answer sent to paused terminal: %q", runner.prompts)
	}
	record, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil || record.ResumeNote == "" {
		t.Fatalf("answer missing from retained resume: %+v %v", record, err)
	}
	if question := store.Snapshot().Questions[0]; question.Status != "succeeded" || question.AnsweredBy != "overlord" {
		t.Fatalf("answer not recorded: %+v", question)
	}
	isReady, err := service.pauseCleared(t.Context(), condition, time.Now(), &fleetWakes{})
	if err != nil || !isReady {
		t.Fatalf("answered pause stayed blocked: %v, %v", isReady, err)
	}
}
