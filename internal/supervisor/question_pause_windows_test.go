package supervisor

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

func TestCFOAnswerToMemoryPausedGoblinSurvivesItsHostAndResumesOnce(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, notified, goblin, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	question := surfaced(t, store, meta, notified, connection)
	condition, err := state.NewPauseCondition("memory", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "pause-memory", Action: "pause", Phase: "paused", Pause: &condition}); err != nil {
		t.Fatal(err)
	}
	keptForResume(t, store, meta, "use the staging database")
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if typed := goblin.exit(t); len(typed) != 0 {
		t.Fatalf("paused host received %q, want nothing", typed)
	}

	// Act
	chosen, isQueued, answerErr := connection.AnswerGoblin(t.Context(), question.ID, "sqlite", "keep it local")

	// Assert
	if answerErr != nil || chosen != "SQLite" || !isQueued {
		t.Fatalf("answer = %q, queued %v, %v; want SQLite retained without a native host", chosen, isQueued, answerErr)
	}
	wantAnswer := "SQLite. keep it local"
	wantNote := fmt.Sprintf("decision %d: %s\nMessage from the Overlord, typed on the board: use the staging database", notified.Seq, wantAnswer)
	retained, err := state.ReadLifecycle(h.State, meta.ID)
	if err != nil || retained.ResumeNote != wantNote || retained.Generation != meta.SpawnGen {
		t.Fatalf("retained = %+v, %v; want the answer and earlier message for its original generation", retained, err)
	}
	if pending, err := wake.Pending(h.State); err != nil || len(pending) != 1 || pending[0].Seq != notified.Seq || pending[0].Answered != wantAnswer || pending[0].AnsweredBy != wake.AnsweredByCFO {
		t.Fatalf("notify = %+v, %v; want the exact answer attributed to the CFO", pending, err)
	}
	if recorded := store.Snapshot().Questions[0]; recorded.ID != question.ID || recorded.Identity != question.Identity || recorded.Generation != meta.SpawnGen || recorded.Answer != wantAnswer || recorded.AnsweredOption != "SQLite" || recorded.AnsweredBy != "cfo" || !recorded.AnsweredAway || recorded.AnsweredAt == nil {
		t.Fatalf("recorded = %+v; want the original question's CFO answer and authority", recorded)
	}
	for _, ref := range []string{question.ID, fmt.Sprint(notified.Seq)} {
		if _, _, err := connection.AnswerGoblin(t.Context(), ref, "Postgres", "another answer"); err == nil {
			t.Fatal("a duplicate answer was accepted")
		}
	}
	if entries := afkEntries(t, h.State); len(entries) != 2 || entries[1].Kind != afk.KindAnswer || entries[1].What != question.ID || entries[1].Task != meta.ID || !strings.Contains(entries[1].Evidence, wantAnswer) {
		t.Fatalf("AFK entries = %+v, want the switch and one audit of the retained answer", entries)
	}
	if actions := store.Snapshot().Actions; len(actions) != 1 || actions[0].Kind != "message" {
		t.Fatalf("actions = %+v, want the existing resume-note owner without a second delivery queue", actions)
	}

	// Act
	var resumed hostedTerminal
	resumeCount := 0
	controller := lifecycle.Service{StateDir: h.State, Operations: lifecycle.Operations{
		Memory: func() (uint64, uint64, error) { return 8 << 30, 8 << 30, nil },
		Resume: func(ctx context.Context, current state.TaskMeta, prior state.Lifecycle) error {
			resumeCount++
			current.SpawnGen = "resumed-generation"
			if err := state.WriteTaskMeta(h.State, current); err != nil {
				return err
			}
			resumed = goblinTerminal(t, h.State, current.ID)
			_, err := connection.SendGoblin(ctx, current.ID, goblinIdentity(current), prior.ResumeNote)
			return err
		},
		Notify: func(state.Lifecycle) error { return nil },
	}}
	request := lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: "resume-memory", Action: "resume", Reason: "overlord"}
	for range 2 {
		if result, err := controller.Run(t.Context(), request); err != nil || result.Phase != "running" || result.Generation != "resumed-generation" {
			t.Fatalf("resume = %+v, %v; want the supported resume running", result, err)
		}
	}
	if err := store.ingestAnswers(); err != nil {
		t.Fatal(err)
	}
	if err := store.supersedeQuestions(); err != nil {
		t.Fatal(err)
	}

	// Assert
	if typed := resumed.lines(t); resumeCount != 1 || len(typed) != 1 || typed[0] != fmt.Sprintf("CFO: decision %d: %s Message from the Overlord, typed on the board: use the staging database", notified.Seq, wantAnswer) {
		t.Fatalf("resume count = %d, received %q; want one retained answer delivered to the replacement host", resumeCount, typed)
	}
	if _, _, err := connection.AnswerGoblin(t.Context(), question.ID, "SQLite", "keep it local"); err == nil {
		t.Fatal("a reconnect accepted the old answer again")
	}
	if entries := afkEntries(t, h.State); len(entries) != 2 {
		t.Fatalf("AFK entries = %+v, want still one answer audit after resume", entries)
	}
}

func TestCFOAnswerDoesNotQueueForUnexpectedlyMissingNativeHosts(t *testing.T) {
	for _, test := range []struct {
		name, phase, generation, backend string
	}{
		{name: "no pause", backend: "native"},
		{name: "running task", phase: "running", backend: "native"},
		{name: "pause belongs to an earlier generation", phase: "paused", generation: "earlier-generation", backend: "native"},
		{name: "unsupported legacy backend", phase: "paused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			primaryFixture(t, store)
			meta, notified, goblin, connection := goblinFixture(t, store)
			servePipe(t, store, connection)
			question := surfaced(t, store, meta, notified, connection)
			if test.phase != "" {
				generation := meta.SpawnGen
				if test.generation != "" {
					generation = test.generation
				}
				if err := state.WriteLifecycle(h.State, state.Lifecycle{ID: meta.ID, Generation: generation, Operation: "pause-1", Action: "pause", Phase: test.phase}); err != nil {
					t.Fatal(err)
				}
			}
			meta.Backend = test.backend
			if err := state.WriteTaskMeta(h.State, meta); err != nil {
				t.Fatal(err)
			}
			goblin.exit(t)

			// Act
			_, isQueued, err := connection.AnswerGoblin(t.Context(), question.ID, "SQLite", "")

			// Assert
			if err == nil || isQueued {
				t.Fatalf("answer queued = %v, %v; want an actionable refusal", isQueued, err)
			}
			if pending, err := wake.Pending(h.State); err != nil || len(pending) != 1 || pending[0].Answered != "" {
				t.Fatalf("notify = %+v, %v; want it still unanswered", pending, err)
			}
			if recorded := store.Snapshot().Questions[0]; recorded.Answer != "" || recorded.AnsweredBy != "" || recorded.Status != "pending" {
				t.Fatalf("question = %+v, want it untouched", recorded)
			}
			if retained, err := state.ReadLifecycle(h.State, meta.ID); err == nil && retained.ResumeNote != "" {
				t.Fatalf("retained = %+v, want no answer kept for an invalid recipient", retained)
			}
		})
	}
}

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
