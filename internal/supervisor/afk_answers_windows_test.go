package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/afk"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// The CFO's own question is the Overlord's alone to answer, and the one way
// the CFO can close it is by recording an answer he gave somewhere else. While
// AFK mode is on he is away, so nothing is recorded as his, through cfo answer
// or straight over the pipe, and the question stays held for him.
func TestWhileAFKModeIsOnNoAnswerIsRecordedAsTheOverlords(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, _, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	if err := connection.PublishQuestion(context.Background(), "drop-legacy-invoices", "Migration 0042 drops legacy_invoices. Apply it?", []string{"Apply it", "Keep it held"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	_, recordErr := connection.RecordAnswer(context.Background(), "drop-legacy-invoices", "Apply it", "", "chat")
	pipeErr := sendPipeRequest(h.State, runPipeRequest{Kind: "answer", Answer: &cfoAnswer{QuestionID: "drop-legacy-invoices", Option: "Apply it", Answer: "Apply it", In: "chat", At: time.Now().UTC()}})

	// Assert
	for name, err := range map[string]error{"cfo answer --record-only --in chat": recordErr, "an answer sent straight to the pipe": pipeErr} {
		if err == nil || !strings.Contains(err.Error(), "AFK mode is on") || !strings.Contains(err.Error(), "held for him") {
			t.Errorf("%s = %v, want it refused because he is away", name, err)
		}
	}
	if got := store.Snapshot().Questions; len(got) != 1 || got[0].Status != "pending" || got[0].AnsweredBy != "" {
		t.Errorf("the question = %+v, want it still pending for him", got)
	}
	if entries := afkEntries(t, h.State); len(entries) != 1 {
		t.Errorf("the AFK log = %+v, want only the switch: nothing was decided", entries)
	}
}

// Once he is back and AFK mode is off, his answer elsewhere is recorded as
// before.
func TestAnAnswerHeGaveElsewhereIsRecordedOnceAFKModeIsOff(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	_, _, _, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	if err := connection.PublishQuestion(context.Background(), "drop-legacy-invoices", "Migration 0042 drops legacy_invoices. Apply it?", []string{"Apply it", "Keep it held"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := afk.TurnOff(h.State, "the board", time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, err := connection.RecordAnswer(context.Background(), "drop-legacy-invoices", "Keep it held", "", "chat")

	// Assert
	if err != nil || chosen != "Keep it held" {
		t.Fatalf("RecordAnswer = %q, %v, want his answer recorded", chosen, err)
	}
	if got := store.Snapshot().Questions[0]; got.Status != "succeeded" || got.AnsweredBy != "overlord" || got.AnsweredIn != "chat" {
		t.Errorf("the question = %+v, want it closed as his answer in chat", got)
	}
}

// An answer the CFO gives a goblin while AFK mode is on is a decision made
// under its authority, so the log holds it with its evidence: the question,
// whose it was and what the CFO answered.
func TestTheCFOsAnswerToAGoblinIsLoggedWhileAFKModeIsOn(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, runner, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "sqlite", "keep it local")

	// Assert
	if err != nil || chosen != "SQLite" || len(runner.prompts) != 1 {
		t.Fatalf("AnswerGoblin = %q, %v with %d prompts, want SQLite delivered once", chosen, err, len(runner.prompts))
	}
	entries := afkEntries(t, h.State)
	if len(entries) != 2 {
		t.Fatalf("the AFK log = %+v, want the switch and the answer", entries)
	}
	logged := entries[1]
	if logged.Kind != afk.KindAnswer || logged.What != q.ID || logged.Task != meta.ID || !strings.Contains(logged.Evidence, "Which store?") || !strings.Contains(logged.Evidence, "SQLite. keep it local") {
		t.Errorf("logged = %+v, want the answer with its question, its goblin and the choice", logged)
	}
}

// A choice the CFO gave a goblin another way and records afterwards is its
// own decision too.
func TestAChoiceTheCFORecordsAfterwardsIsLoggedWhileAFKModeIsOn(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, _, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	q := surfaced(t, store, meta, record, connection)
	if err := wake.AckThrough(h.State, record.Seq); err != nil {
		t.Fatal(err)
	}
	if _, _, err := afk.TurnOn(h.State, "the board", nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Act
	chosen, err := connection.RecordAnswer(context.Background(), q.ID, "sqlite", "decided out of band", "")

	// Assert
	if err != nil || chosen != "SQLite" {
		t.Fatalf("RecordAnswer = %q, %v, want SQLite recorded", chosen, err)
	}
	entries := afkEntries(t, h.State)
	if len(entries) != 2 || entries[1].Kind != afk.KindAnswer || entries[1].What != q.ID || entries[1].Task != meta.ID || !strings.Contains(entries[1].Evidence, "SQLite. decided out of band") {
		t.Errorf("the AFK log = %+v, want the switch and the recorded choice", entries)
	}
}

// While AFK mode is off the CFO answers as before and nothing is logged as
// decided under it.
func TestAnAnswerIsNotLoggedWhileAFKModeIsOff(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primaryFixture(t, store)
	meta, record, _, connection := goblinFixture(t, store)
	servePipe(t, store, connection)
	surfaced(t, store, meta, record, connection)

	// Act
	_, _, err := connection.AnswerGoblin(context.Background(), fmt.Sprint(record.Seq), "sqlite", "")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if entries := afkEntries(t, h.State); len(entries) != 0 {
		t.Errorf("the AFK log = %+v, want nothing", entries)
	}
}
