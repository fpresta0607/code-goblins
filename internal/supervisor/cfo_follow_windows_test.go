package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
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

// deliver keeps the board's reconciliation ticks while an untyped reply is
// queued for a reopened CFO whose composer has not settled yet.
func deliver(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		s.cycle(ctx, false)
		s.process(ctx)
		d := s.Store.Snapshot()
		isPending := slices.ContainsFunc(d.Actions, func(a Action) bool { return a.Status == "queued" }) || slices.ContainsFunc(d.Runs, func(r Run) bool { return r.Untold != "" })
		if !isPending || !s.Store.cfoLive() {
			return
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("the reopened CFO still has an untyped reply queued: %+v", d.Actions)
		}
	}
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
	reads := 0
	s.Options.CFO.ReadScreen = func(record host.Record) ([]string, error) {
		reads++
		if reads == 1 {
			return []string{strings.Repeat("─", 80), "❯ hooked", strings.Repeat("─", 80), "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
		}
		return host.ReadScreen(record)
	}
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
	if answered := s.Store.Snapshot().Questions[0]; answered.Identity != open.Identity || answered.Status != "succeeded" || answered.AnsweredBy != "overlord" || answered.AnsweredOption != "Ship it" {
		t.Errorf("the reopened question's delivery/history = %+v, want the same recipient and the Overlord's accepted choice", answered)
	}
	if lines := cfo.waitForLines(t, 2); strings.Count(strings.Join(lines, "\n"), want) != 1 {
		t.Errorf("the reopened CFO received %q, want the answer once", lines)
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

// reregisterCFO registers the home's running CFO again under another
// registration, as a CFO that was closed and opened again registers, and
// returns the identity of that registration.
func reregisterCFO(t *testing.T, store *Store, primary primaryRegistration) string {
	t.Helper()
	primary.Process.Session = "reopened-session"
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Home.State, "primary.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, identity, err := decodePrimary(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

// The CFO's run item nobody ran yet keeps its script across the CFO's
// restart: the Overlord runs it afterwards and it starts.
func TestAReadyRunStartsAfterTheCFORestarted(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, made, _, connection := primaryFixture(t, store)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CFO: connection, Runs: launcher}}
	r := readyRun(t, store, made, "cfo-run-0002", "powershell", false, time.Now().UTC())
	reopened := reregisterCFO(t, store, primary)
	s.cycle(context.Background(), false)
	followed := store.Snapshot().Runs[0]

	// Act
	pressRun(t, s, followed, "run-cfo-run-0002")

	// Assert
	if followed.Identity != reopened {
		t.Fatalf("the run item is addressed to %s, want the reopened CFO %s", followed.Identity, reopened)
	}
	script := filepath.Join(runDir(store.Home.State, r), "command.ps1")
	if len(launcher.launches) != 1 || launcher.launches[0].Script != script {
		t.Errorf("launched %+v, want the script it was published with, %s", launcher.launches, script)
	}
	if started := store.Snapshot().Runs[0]; started.State != "running" {
		t.Errorf("the run item reads %s (%s), want it running", started.State, started.Reason)
	}
}

// The CFO's run item that was running when the CFO restarted is still read
// from its own directory when its command finishes.
func TestARunningRunThatFinishesAfterTheCFORestartedIsRead(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, made, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, made, "cfo-run-0003", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-cfo-run-0003")
	reregisterCFO(t, store, primary)
	s.cycle(context.Background(), false)
	dir := runDir(store.Home.State, r)
	if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("migration 42 applied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	s.cycle(context.Background(), false)

	// Assert
	if finished := store.Snapshot().Runs[0]; finished.State != "succeeded" || finished.Output != "migration 42 applied" {
		t.Errorf("the run item reads %s with output %q (%s), want it succeeded with what it printed", finished.State, finished.Output, finished.Reason)
	}
	want := "Run item cfo-run-0003 (Run cfo-run-0003) finished with exit code 0. The output ends: migration 42 applied"
	if typed := cfo.lines(t); !slices.ContainsFunc(typed, func(line string) bool { return strings.Contains(line, want) }) {
		t.Errorf("the reopened CFO received %q, want %q", typed, want)
	}
}

// The CFO's delivered document is still served after the CFO restarted, and
// its copy is removed when the item is pruned.
func TestTheCFOsDocumentIsServedAndPrunedAfterTheCFORestarted(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primary, _, _, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	source := filepath.Join(t.TempDir(), "plan.pdf")
	data := []byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := DeliverDocument(h, "", "cfo-document-1", "The plan", source, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	dir := reviewImageDir(h.State, store.Snapshot().Reviews[0])
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CFO: connection}}
	reopened := reregisterCFO(t, store, primary)
	s.cycle(ctx, false)

	// Act
	served := httptest.NewRecorder()
	NewHTTP(s, "board.local", nil).ServeHTTP(served, httptest.NewRequest("GET", "http://board.local/api/reviews/cfo-document-1/document", nil))
	if _, err := store.Queue(Action{ID: "open-cfo-document-1", Kind: "review_clear", Generation: reopened, ReviewID: "cfo-document-1", Text: "Opened"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(ctx, s.execute); err != nil {
		t.Fatal(err)
	}
	pruneErr := store.pruneReviews(time.Now().Add(closedReviewRetention + time.Hour))

	// Assert
	if served.Code != 200 || !bytes.Equal(served.Body.Bytes(), data) {
		t.Errorf("the document after the restart = %d %q, want the copy", served.Code, served.Body.String())
	}
	if pruneErr != nil || len(store.Snapshot().Reviews) != 0 {
		t.Fatalf("pruning left %+v (%v), want the cleared item gone", store.Snapshot().Reviews, pruneErr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the pruned item's directory still exists (%v)", err)
	}
}

// A run item the board made for the Overlord, a connection repair or a
// credential request's terminal, is not the CFO's: it does not follow the CFO,
// and the terminal still opens after the CFO restarted.
func TestARunTheBoardMadeDoesNotFollowTheCFO(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primary, _, _, connection := primaryFixture(t, store)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, Instance: "test-instance", work: make(chan struct{}, 1), Options: Options{CFO: connection, Runs: launcher}}
	t.Cleanup(func() {
		if s.connectionChecks != nil {
			s.connectionChecks.Close()
		}
	})
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	repair, err := s.connectionRun(meta, connectionRequest{Task: meta.ID, Generation: meta.SpawnGen, Connection: "credential:TEST_TOKEN", Action: "store:TEST_TOKEN"}, connections.Repair{Credential: "TEST_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	terminal, err := s.recordBoardRun(Run{ID: "credential-0123456789abcdef", Identity: strings.Repeat("d", 64), Title: "Type STRIPE_SECRET_KEY for throwaway in a terminal on this PC", Shell: "powershell", Command: "Write-Output ready\n", Cwd: store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), CredentialRequest: "cred-0123456789abcdef", CredentialNames: []string{"STRIPE_SECRET_KEY"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "run-" + terminal.ID, Kind: "run", RunID: terminal.ID, Generation: terminal.Identity}); err != nil {
		t.Fatal(err)
	}
	reregisterCFO(t, store, primary)

	// Act
	s.cycle(context.Background(), false)
	if err := store.ProcessOne(context.Background(), s.execute); err != nil {
		t.Fatal(err)
	}

	// Assert
	for _, made := range []Run{repair, terminal} {
		runs := store.Snapshot().Runs
		i := slices.IndexFunc(runs, func(r Run) bool { return r.ID == made.ID })
		if i < 0 || runs[i].Identity != made.Identity || runs[i].Made != "" {
			t.Errorf("after the CFO restarted, run items read %+v, want %s still under the identity the board made it with", runs, made.ID)
		}
	}
	script := filepath.Join(runDir(store.Home.State, terminal), "command.ps1")
	if len(launcher.launches) != 1 || launcher.launches[0].Script != script {
		t.Errorf("launched %+v, want the credential terminal's script %s", launcher.launches, script)
	}
}

// An answer that waited for the CFO is delivered to the CFO registered now
// even when the delivery worker runs before the supervisor's next cycle.
func TestAWaitingAnswerIsDeliveredToTheReopenedCFOBeforeAnyCycle(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, closed, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection}}
	question := Question{ID: "cfo-question-3", Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "answer-cfo-question-3", Kind: "cfo_answer", QuestionID: question.ID, Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	reregisterCFO(t, store, primary)

	// Act
	err := store.ProcessOne(context.Background(), s.execute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := "User answer to CFO question cfo-question-3. Question: Pick a layout Answer: Board"
	if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], want) {
		t.Errorf("the reopened CFO received %q, want %q once", typed, want)
	}
	if answered := store.Snapshot().Questions[0]; answered.Status != "succeeded" {
		t.Errorf("the question reads %q (%s), want it answered", answered.Status, answered.Message)
	}
}

// A queued action follows the CFO only when the item it names did: a clear of
// a question that failed under the CFO before, which stays where it is, still
// clears it, while the answer to a question still open reaches the CFO now.
func TestAClearOfAQuestionThatDidNotFollowTheCFOStillClearsIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, closed, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection}}
	for _, id := range []string{"cfo-question-failed", "cfo-question-open"} {
		if err := store.acceptQuestion(Question{ID: id, Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.db.Questions[0].Status = "failed"
	store.mu.Unlock()
	if _, err := store.Queue(Action{ID: "answer-cfo-question-open", Kind: "cfo_answer", QuestionID: "cfo-question-open", Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	reregisterCFO(t, store, primary)
	if _, err := store.Queue(Action{ID: "clear-cfo-question-failed", Kind: "question_clear", QuestionID: "cfo-question-failed", Generation: closed}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Act
	err := errors.Join(store.ProcessOne(ctx, s.execute), store.ProcessOne(ctx, s.execute))

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	questions := store.Snapshot().Questions
	if questions[0].Status != "cleared" {
		t.Errorf("the failed question reads %q (%s), want it cleared", questions[0].Status, questions[0].Message)
	}
	if actions := store.Snapshot().Actions; actions[1].Status != "succeeded" {
		t.Errorf("the clear reads %q (%s), want it succeeded", actions[1].Status, actions[1].Message)
	}
	want := "User answer to CFO question cfo-question-open. Question: Pick a layout Answer: Board"
	if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], want) {
		t.Errorf("the reopened CFO received %q, want %q once", typed, want)
	}
	if questions[1].Status != "succeeded" {
		t.Errorf("the open question reads %q (%s), want it answered", questions[1].Status, questions[1].Message)
	}
}

// A run's result typed and submitted while the CFO was inside a turn is told:
// the CFO takes it at its next tool call, so it is never typed again.
func TestARunResultSubmittedBehindTheCFOsTurnIsToldOnce(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, identity, "cfo-run-0004", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-cfo-run-0004")
	if err := os.WriteFile(filepath.Join(runDir(store.Home.State, r), "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfo.startTurn(t)
	ctx := context.Background()

	// Act
	finishErr := s.finishRuns(ctx)
	retellErr := errors.Join(s.retellRuns(ctx), s.retellRuns(ctx))

	// Assert
	if finishErr != nil || retellErr != nil {
		t.Fatalf("finishing = %v, telling again = %v, want neither to fail", finishErr, retellErr)
	}
	if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Run item cfo-run-0004") {
		t.Errorf("the CFO in a turn was sent %q, want the result once", typed)
	}
	if told := store.Snapshot().Runs[0]; told.Untold != "" || told.Reason != "" {
		t.Errorf("the run item reads untold %q, reason %q, want it told with nothing noted", told.Untold, told.Reason)
	}
}

// finishedRun is a run item of the CFO registered as identity whose command
// has finished and that the supervisor has yet to end.
func finishedRun(t *testing.T, s *Service, identity, id string) {
	t.Helper()
	r := readyRun(t, s.Store, identity, id, "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-"+id)
	if err := os.WriteFile(filepath.Join(runDir(s.Store.Home.State, r), "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A run's result that was typed to the CFO before the send failed may have
// reached it: it is noted on the item and never typed again.
func TestARunResultThatFailedAfterItWasTypedIsNotTypedAgain(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	finishedRun(t, s, identity, "cfo-run-0005")
	// The send ends between the typing and Enter, so the result is in the
	// CFO's composer and whether the CFO takes it is unknown.
	interrupted, interrupt := context.WithCancel(context.Background())
	interrupt()
	ctx := context.Background()

	// Act
	finishErr := s.finishRuns(interrupted)
	retellErr := errors.Join(s.retellRuns(ctx), s.retellRuns(ctx))
	cfo.typeLine(t, "")

	// Assert
	if finishErr != nil || retellErr != nil {
		t.Fatalf("finishing = %v, telling again = %v, want neither to fail", finishErr, retellErr)
	}
	if typed := cfo.waitForLines(t, 1); len(typed) != 1 || strings.Count(typed[0], "Run item cfo-run-0005") != 1 {
		t.Errorf("the CFO's composer held %q, want the result typed once", typed)
	}
	if noted := store.Snapshot().Runs[0]; noted.Untold != "" || !strings.Contains(noted.Reason, "the CFO could not be told") {
		t.Errorf("the run item reads untold %q, reason %q, want the uncertain delivery noted and nothing left to tell", noted.Untold, noted.Reason)
	}
}

// A run's result refused before anything was typed, here with no CFO
// registered, stays untold, and the CFO that registers next is told it once.
func TestARunResultRefusedBeforeItWasTypedIsToldOnceTheCFORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, identity, cfo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CFO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	finishedRun(t, s, identity, "cfo-run-0006")
	registration := filepath.Join(store.Home.State, "primary.json")
	if err := os.Remove(registration); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.finishRuns(ctx); err != nil {
		t.Fatal(err)
	}

	// Act
	refused := s.retellRuns(ctx)
	waiting := store.Snapshot().Runs[0]
	data, err := json.Marshal(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registration, data, 0o600); err != nil {
		t.Fatal(err)
	}
	retellErr := errors.Join(s.retellRuns(ctx), s.retellRuns(ctx))

	// Assert
	if !errors.Is(refused, ErrRejected) || waiting.Untold == "" {
		t.Errorf("with no CFO registered the tell = %v and the item's untold = %q, want it refused and still to tell", refused, waiting.Untold)
	}
	if retellErr != nil {
		t.Fatal(retellErr)
	}
	if typed := cfo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Run item cfo-run-0006") {
		t.Errorf("the CFO that registered was sent %q, want the result once", typed)
	}
	if told := store.Snapshot().Runs[0]; told.Untold != "" || told.Reason != "" {
		t.Errorf("the run item reads untold %q, reason %q, want it told with nothing noted", told.Untold, told.Reason)
	}
}
