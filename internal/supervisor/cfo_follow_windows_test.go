package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/lock"
)

// closedCFO registers, in native terminal cfo, a CFO process that is no
// longer running, as a CFO that was closed leaves its registration, and
// returns that registration's identity.
func closedCFO(t *testing.T, stateDir string) string {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(primaryRegistration{Host: NativeCFOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, identity, err := decodePrimary(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// reopenCFO opens native terminal cfo again with a program that registers as
// the CFO and reports each line it takes through its native prompt hook, as
// Claude Code does once its SessionStart hook has registered it.
func reopenCFO(t *testing.T, stateDir string) hostedTerminal {
	t.Helper()
	cfo := hostTerminal(t, stateDir, NativeCFOTerminal)
	cfo.typeLine(t, "register")
	if lines := cfo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the reopened CFO recorded %q, want its registration", lines)
	}
	cfo.typeLine(t, "hooked")
	return cfo
}

// cfoBoard is a board whose CFO, registered in native terminal cfo, has been
// closed, and the identity that CFO registered with.
func cfoBoard(t *testing.T) (*Service, string, string) {
	t.Helper()
	t.Setenv("HERDR_PANE_ID", "")
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CFO = &CFOConnection{State: stateDir}
	return h.Service, stateDir, closedCFO(t, stateDir)
}

// deliver runs the supervisor's cycle and its delivery worker once, as the
// board does after a change.
func deliver(t *testing.T, s *Service) {
	t.Helper()
	s.cycle(context.Background(), false)
	s.process(context.Background())
}

// The Overlord answered the CFO's question while the CFO was closed. The
// answer waits, and the CFO that opens again in its terminal receives it.
func TestAnAnswerGivenWhileTheCFOWasClosedReachesTheCFOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := cfoBoard(t)
	question := Question{ID: "cfo-question-1", Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := s.Store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Queue(Action{ID: "answer-cfo-question-1", Kind: "cfo_answer", QuestionID: question.ID, Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	deliver(t, s)
	waiting := s.Store.Snapshot().Questions[0]

	// Act
	cfo := reopenCFO(t, stateDir)
	deliver(t, s)

	// Assert
	if waiting.Status != "queued" {
		t.Errorf("with the CFO closed, the answered question reads %q (%s), want it waiting as queued", waiting.Status, waiting.Message)
	}
	want := "Overlord: User answer to CFO question cfo-question-1. Question: Pick a layout Answer: Board"
	if lines := cfo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CFO received %q, want %q", lines, want)
	}
	if answered := s.Store.Snapshot().Questions[0]; answered.Status != "succeeded" {
		t.Errorf("the question reads %q (%s), want it answered", answered.Status, answered.Message)
	}
}

// The CFO's question still waiting on the Overlord when the CFO closes stays
// open for the CFO that opens again, and his answer then reaches that CFO.
func TestTheCFOsOpenQuestionFollowsItAcrossARestart(t *testing.T) {
	// Arrange
	s, stateDir, closed := cfoBoard(t)
	question := Question{ID: "cfo-question-2", Identity: closed, Text: "Ship it now?", Options: []string{"Ship it", "Wait"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := s.Store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	cfo := reopenCFO(t, stateDir)

	// Act
	deliver(t, s)
	open := s.Store.Snapshot().Questions[0]
	_, err := s.Store.Queue(Action{ID: "answer-cfo-question-2", Kind: "cfo_answer", QuestionID: question.ID, Generation: open.Identity, Text: "Ship it", AnswerKind: "option"})
	deliver(t, s)

	// Assert
	if open.Status != "pending" {
		t.Fatalf("after the CFO reopened, its question reads %q (%s), want it still open", open.Status, open.Message)
	}
	if err != nil {
		t.Fatalf("answering the question after the CFO reopened: %v", err)
	}
	want := "Overlord: User answer to CFO question cfo-question-2. Question: Ship it now? Answer: Ship it"
	if lines := cfo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CFO received %q, want %q", lines, want)
	}
}

// The Overlord answered the CFO's own review item while the CFO was closed.
// The answer waits, and the CFO that opens again receives it.
func TestAReviewAnswerGivenWhileTheCFOWasClosedReachesTheCFOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := cfoBoard(t)
	now := time.Now().UTC()
	review := Review{ID: "cfo-review-1", Identity: closed, Title: "Read the plan", State: "open", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.acceptReview(review); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Queue(Action{ID: "answer-cfo-review-1", Kind: "review_answer", ReviewID: review.ID, Generation: closed, Text: "Looks right"}); err != nil {
		t.Fatal(err)
	}
	deliver(t, s)

	// Act
	cfo := reopenCFO(t, stateDir)
	deliver(t, s)

	// Assert
	want := "Overlord: Answer to your review item cfo-review-1 (Read the plan): Looks right"
	if lines := cfo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CFO received %q, want %q", lines, want)
	}
	if delivered := s.Store.Snapshot().Reviews[0]; !delivered.Delivered {
		t.Errorf("the review item reads %+v, want its answer delivered", delivered)
	}
}

// The Overlord ran the CFO's run item while the CFO was closed. The CFO that
// opens again is told how it ended.
func TestARunThatEndedWhileTheCFOWasClosedIsToldToTheCFOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := cfoBoard(t)
	s.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
	r := readyRun(t, s.Store, closed, "cfo-run-0001", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-cfo-run-0001")
	dir := runDir(stateDir, r)
	if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("migration 42 applied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.finishRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Act
	cfo := reopenCFO(t, stateDir)
	deliver(t, s)

	// Assert
	want := "Overlord: Run item cfo-run-0001 (Run cfo-run-0001) finished with exit code 0. The output ends: migration 42 applied"
	if lines := cfo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CFO received %q, want %q", lines, want)
	}
}
