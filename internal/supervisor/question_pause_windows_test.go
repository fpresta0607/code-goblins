package supervisor

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lifecycle"
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

	if typed := runner.exit(t); len(typed) != 0 {
		t.Fatalf("answer sent to paused terminal: %q", typed)
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

// An answer saved for a paused goblin joins what its resume note held before
// the board's messages kept in it, so each message kept before the answer
// stays, in order, for the board's card to list and the resume to carry.
// Before, the answer replaced the whole note, so those messages were lost and
// nobody was told (issue #1294).
func TestAnAnswerSavedWhilePausedLeavesTheMessagesKeptBeforeIt(t *testing.T) {
	answer := "The Overlord answered your question on the board. Question: Which store? Answer: SQLite"
	for _, tc := range []struct {
		name     string
		answered string
		kept     []string
	}{
		{"no message kept", "", nil},
		{"one message kept", "", []string{"use the staging database"}},
		{"two messages kept", "", []string{"use the staging database", "then rebase\nonto main"}},
		{"a message kept after an earlier answer", "The Overlord answered your question on the board. Question: Which branch? Answer: main", []string{"use the staging database"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			meta, notified, runner, connection := goblinFixture(t, store)
			question := surfaced(t, store, meta, notified, connection)
			condition, err := state.NewPauseCondition("question", question.ID, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-question", Action: "pause", Phase: "paused", Pause: &condition, ResumeNote: tc.answered}); err != nil {
				t.Fatal(err)
			}
			keptForResume(t, store, meta, tc.kept...)
			if _, err := store.Queue(Action{ID: "answer-1", Kind: "goblin_answer", Generation: question.Identity, QuestionID: question.ID, Text: "SQLite"}); err != nil {
				t.Fatal(err)
			}
			service := &Service{Store: store, Options: Options{CFO: connection}}
			var carried string
			controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
				Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil },
				Resume: func(_ context.Context, _ state.TaskMeta, prior state.Lifecycle) error {
					carried = prior.ResumeNote
					return nil
				},
				Notify: func(state.Lifecycle) error { return nil },
			}}

			// Act
			answered := store.ProcessOne(t.Context(), service.execute)
			saved, savedErr := state.ReadLifecycle(h.State, meta.ID)
			onCard := keptOnCard(t, store, meta.ID)
			resumed, resumeErr := controller.Run(t.Context(), lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-1", Action: "resume", Reason: "overlord"})

			// Assert
			if typed := runner.exit(t); answered != nil || len(typed) != 0 {
				t.Fatalf("answer = %v, typed %q, want it saved for the resume with nothing typed", answered, typed)
			}
			want := []string{answer}
			if tc.answered != "" {
				want = []string{tc.answered, answer}
			}
			for _, text := range tc.kept {
				want = append(want, fromOverlord(text))
			}
			if savedErr != nil || saved.ResumeNote != strings.Join(want, "\n") {
				t.Fatalf("resume note = %q, %v, want %q", saved.ResumeNote, savedErr, strings.Join(want, "\n"))
			}
			if !slices.Equal(onCard, tc.kept) {
				t.Fatalf("kept on the card %q, want %q", onCard, tc.kept)
			}
			if resumeErr != nil || resumed.Phase != "running" || carried != strings.Join(want, "\n") {
				t.Fatalf("resume = %+v, %v, carried %q; want it running with %q carried", resumed, resumeErr, carried, strings.Join(want, "\n"))
			}
		})
	}
}
