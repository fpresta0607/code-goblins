package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// closedCEO registers, in native terminal ceo, a CEO process that is no
// longer running, as a CEO that was closed leaves its registration, and
// returns that registration's identity.
func closedCEO(t *testing.T, stateDir string) string {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(primaryRegistration{Host: NativeCEOTerminal, Agent: "claude", Process: lock.Info{PID: 37680, OwnerPID: 37680, Start: time.Date(2026, 9, 15, 9, 0, 0, 0, time.UTC), Hostname: hostname}})
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

// reopenCEO opens native terminal ceo again with a program that registers as
// the CEO and reports each line it takes through its native prompt hook, as
// Claude Code does once its SessionStart hook has registered it.
func reopenCEO(t *testing.T, stateDir string) hostedTerminal {
	t.Helper()
	ceo := hostTerminal(t, stateDir, NativeCEOTerminal)
	ceo.typeLine(t, "register")
	if lines := ceo.waitForLines(t, 1); len(lines) != 1 || !strings.HasPrefix(lines[0], "registered ") {
		t.Fatalf("the reopened CEO recorded %q, want its registration", lines)
	}
	ceo.typeLine(t, "hooked")
	return ceo
}

// ceoBoard is a board whose CEO, registered in native terminal ceo, has been
// closed, and the identity that CEO registered with.
func ceoBoard(t *testing.T) (*Service, string, string) {
	t.Helper()
	t.Setenv("HERDR_PANE_ID", "")
	h, _ := nativeBoard(t, "direct")
	stateDir := h.Service.Store.Home.State
	h.Service.Options.CEO = &CEOConnection{State: stateDir}
	return h.Service, stateDir, closedCEO(t, stateDir)
}

// deliver keeps the board's reconciliation ticks while an untyped reply is
// queued for a reopened CEO whose composer has not settled yet.
func deliver(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for {
		s.cycle(ctx, false)
		s.process(ctx)
		d := s.Store.Snapshot()
		isPending := slices.ContainsFunc(d.Actions, func(a Action) bool { return a.Status == "queued" }) || slices.ContainsFunc(d.Runs, func(r Run) bool { return r.Untold != "" })
		if !isPending || !s.Store.ceoLive() {
			return
		}
		select {
		case <-time.After(50 * time.Millisecond): // stall-accepted: a pause between ticks, which a stall only makes longer.
		case <-ctx.Done():
			t.Fatalf("the reopened CEO still has an untyped reply queued: %+v", d.Actions)
		}
	}
}

// The Overlord answered the CEO's question while the CEO was closed. The
// answer waits, and the CEO that opens again in its terminal receives it.
func TestAnAnswerGivenWhileTheCEOWasClosedReachesTheCEOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := ceoBoard(t)
	question := Question{ID: "ceo-question-1", Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := s.Store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Queue(Action{ID: "answer-ceo-question-1", Kind: "ceo_answer", QuestionID: question.ID, Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	deliver(t, s)
	waiting := s.Store.Snapshot().Questions[0]

	// Act
	ceo := reopenCEO(t, stateDir)
	deliver(t, s)

	// Assert
	if waiting.Status != "queued" {
		t.Errorf("with the CEO closed, the answered question reads %q (%s), want it waiting as queued", waiting.Status, waiting.Message)
	}
	want := "Overlord: User answer to CEO question ceo-question-1. Question: Pick a layout Answer: Board"
	if lines := ceo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CEO received %q, want %q", lines, want)
	}
	if answered := s.Store.Snapshot().Questions[0]; answered.Status != "succeeded" {
		t.Errorf("the question reads %q (%s), want it answered", answered.Status, answered.Message)
	}
}

// The CEO's question still waiting on the Overlord when the CEO closes stays
// open for the CEO that opens again, and his answer then reaches that CEO.
func TestTheCEOsOpenQuestionFollowsItAcrossARestart(t *testing.T) {
	// Arrange
	s, stateDir, closed := ceoBoard(t)
	question := Question{ID: "ceo-question-2", Identity: closed, Text: "Ship it now?", Options: []string{"Ship it", "Wait"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := s.Store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	ceo := reopenCEO(t, stateDir)

	// Act
	deliver(t, s)
	open := s.Store.Snapshot().Questions[0]
	reads := 0
	s.Options.CEO.ReadScreen = func(record host.Record) ([]string, error) {
		reads++
		if reads == 1 {
			return []string{strings.Repeat("─", 80), "❯ hooked", strings.Repeat("─", 80), "⏵⏵ bypass permissions on (shift+tab to cycle)"}, nil
		}
		return host.ReadScreen(record)
	}
	_, err := s.Store.Queue(Action{ID: "answer-ceo-question-2", Kind: "ceo_answer", QuestionID: question.ID, Generation: open.Identity, Text: "Ship it", AnswerKind: "option"})
	deliver(t, s)

	// Assert
	if open.Status != "pending" {
		t.Fatalf("after the CEO reopened, its question reads %q (%s), want it still open", open.Status, open.Message)
	}
	if err != nil {
		t.Fatalf("answering the question after the CEO reopened: %v", err)
	}
	want := "Overlord: User answer to CEO question ceo-question-2. Question: Ship it now? Answer: Ship it"
	if lines := ceo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CEO received %q, want %q", lines, want)
	}
	if answered := s.Store.Snapshot().Questions[0]; answered.Identity != open.Identity || answered.Status != "succeeded" || answered.AnsweredBy != "overlord" || answered.AnsweredOption != "Ship it" {
		t.Errorf("the reopened question's delivery/history = %+v, want the same recipient and the Overlord's accepted choice", answered)
	}
	if lines := ceo.waitForLines(t, 2); strings.Count(strings.Join(lines, "\n"), want) != 1 {
		t.Errorf("the reopened CEO received %q, want the answer once", lines)
	}
}

// The Overlord answered the CEO's own review item while the CEO was closed.
// The answer waits, and the CEO that opens again receives it.
func TestAReviewAnswerGivenWhileTheCEOWasClosedReachesTheCEOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := ceoBoard(t)
	now := time.Now().UTC()
	review := Review{ID: "ceo-review-1", Identity: closed, Title: "Read the plan", State: "open", CreatedAt: now, UpdatedAt: now}
	if err := s.Store.acceptReview(review); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Queue(Action{ID: "answer-ceo-review-1", Kind: "review_answer", ReviewID: review.ID, Generation: closed, Text: "Looks right"}); err != nil {
		t.Fatal(err)
	}
	deliver(t, s)

	// Act
	ceo := reopenCEO(t, stateDir)
	deliver(t, s)

	// Assert
	want := "Overlord: Answer to your review item ceo-review-1 (Read the plan): Looks right"
	if lines := ceo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CEO received %q, want %q", lines, want)
	}
	if delivered := s.Store.Snapshot().Reviews[0]; !delivered.Delivered {
		t.Errorf("the review item reads %+v, want its answer delivered", delivered)
	}
}

// The Overlord ran the CEO's run item while the CEO was closed. The CEO that
// opens again is told how it ended.
func TestARunThatEndedWhileTheCEOWasClosedIsToldToTheCEOThatReopens(t *testing.T) {
	// Arrange
	s, stateDir, closed := ceoBoard(t)
	s.Options.Runs = &fakeRunLauncher{started: liveStart(t)}
	r := readyRun(t, s.Store, closed, "ceo-run-0001", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-ceo-run-0001")
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
	ceo := reopenCEO(t, stateDir)
	deliver(t, s)

	// Assert
	want := "Overlord: Run item ceo-run-0001 (Run ceo-run-0001) finished with exit code 0. The output ends: migration 42 applied"
	if lines := ceo.waitForLines(t, 2); !slices.Contains(lines, want) {
		t.Errorf("the reopened CEO received %q, want %q", lines, want)
	}
}

// reregisterCEO registers the home's running CEO again under another
// registration, as a CEO that was closed and opened again registers, and
// returns the identity of that registration.
func reregisterCEO(t *testing.T, store *Store, primary primaryRegistration) string {
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

// The CEO's run item nobody ran yet keeps its script across the CEO's
// restart: the Overlord runs it afterwards and it starts.
func TestAReadyRunStartsAfterTheCEORestarted(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, made, _, connection := primaryFixture(t, store)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CEO: connection, Runs: launcher}}
	r := readyRun(t, store, made, "ceo-run-0002", "powershell", false, time.Now().UTC())
	reopened := reregisterCEO(t, store, primary)
	s.cycle(context.Background(), false)
	followed := store.Snapshot().Runs[0]

	// Act
	pressRun(t, s, followed, "run-ceo-run-0002")

	// Assert
	if followed.Identity != reopened {
		t.Fatalf("the run item is addressed to %s, want the reopened CEO %s", followed.Identity, reopened)
	}
	script := filepath.Join(runDir(store.Home.State, r), "command.ps1")
	if len(launcher.launches) != 1 || launcher.launches[0].Script != script {
		t.Errorf("launched %+v, want the script it was published with, %s", launcher.launches, script)
	}
	if started := store.Snapshot().Runs[0]; started.State != "running" {
		t.Errorf("the run item reads %s (%s), want it running", started.State, started.Reason)
	}
}

// The CEO's run item that was running when the CEO restarted is still read
// from its own directory when its command finishes.
func TestARunningRunThatFinishesAfterTheCEORestartedIsRead(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, made, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CEO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, made, "ceo-run-0003", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-ceo-run-0003")
	reregisterCEO(t, store, primary)
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
	want := "Run item ceo-run-0003 (Run ceo-run-0003) finished with exit code 0. The output ends: migration 42 applied"
	if typed := ceo.lines(t); !slices.ContainsFunc(typed, func(line string) bool { return strings.Contains(line, want) }) {
		t.Errorf("the reopened CEO received %q, want %q", typed, want)
	}
}

func TestRunCardKeepsItsOutcomeAcrossCEORestartAndBoardReload(t *testing.T) {
	for _, test := range []struct {
		name, receipt, state string
		isStopped            bool
	}{
		{name: "completed switch", receipt: "0", state: "succeeded"},
		{name: "failed switch", receipt: "7", state: "failed"},
		{name: "command still running", state: "running"},
		{name: "previously stopped switch", state: "stopped", isStopped: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			store, h := testStore(t)
			primary, identity, _, connection := primaryFixture(t, store)
			launcher := &fakeRunLauncher{started: liveStart(t)}
			s := &Service{Store: store, Options: Options{CEO: connection, Runs: launcher}}
			r := readyRun(t, store, identity, "switch-ceo-1", "powershell", false, time.Now().UTC())
			pressRun(t, s, r, "press-switch")
			r = store.Snapshot().Runs[0]
			dir := runDir(h.State, r)
			if err := os.WriteFile(filepath.Join(dir, "output.log"), []byte("CEO restarted as Codex"), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.isStopped {
				if err := s.endRun(context.Background(), r, "stopped", nil, "the Overlord stopped it"); err != nil {
					t.Fatal(err)
				}
			}
			reopened := reregisterCEO(t, store, primary)
			if err := store.followCEO(reopened); err != nil {
				t.Fatal(err)
			}
			if test.receipt != "" {
				if err := os.WriteFile(filepath.Join(dir, "exit.txt"), []byte(test.receipt), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			reloaded, err := Open(h)
			if err != nil {
				t.Fatal(err)
			}
			s.Store = reloaded
			if err := s.finishRuns(context.Background()); err != nil {
				t.Fatal(err)
			}
			server := runBoard(t, s)
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(server.URL + "/api/runs/" + r.ID + "/output")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			var card struct{ State, Output string }
			if err := json.NewDecoder(response.Body).Decode(&card); err != nil {
				t.Fatal(err)
			}

			// Assert
			if response.StatusCode != http.StatusOK || card.State != test.state || card.Output != "CEO restarted as Codex" {
				t.Fatalf("card = %d %+v, want %s with the command output", response.StatusCode, card, test.state)
			}
			finished := reloaded.Snapshot().Runs[0]
			if test.receipt != "" {
				if finished.ExitCode == nil || strconv.Itoa(*finished.ExitCode) != test.receipt || finished.FinishedAt == nil {
					t.Fatalf("run lost its completion receipt: %+v", finished)
				}
			} else if finished.ExitCode != nil {
				t.Fatal("a command without a receipt acquired an exit code")
			}
			if test.isStopped && finished.Reason != "the Overlord stopped it" {
				t.Fatalf("stopped history changed: %+v", finished)
			}
			if len(launcher.all()) != 1 || runDir(h.State, finished) != dir {
				t.Fatal("reload relaunched the command or changed its history directory")
			}
		})
	}
}

// The CEO's delivered file follows the CEO across a restart: its item takes
// the new registration's clear, and its page and copy are removed when the
// item is pruned once the review on its page has ended.
func TestTheCEOsDeliveredFileFollowsTheCEOAndIsPrunedAfterItRestarted(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primary, _, _, connection := primaryFixture(t, store)
	servePipe(t, store, connection)
	source := filepath.Join(t.TempDir(), "plan.pdf")
	if err := os.WriteFile(source, []byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pages := &opened{}
	if err := DeliverDocument(h, "", "ceo-document-1", "The plan", source, false, pages.open); err != nil {
		t.Fatal(err)
	}
	if err := store.ingestReviews(); err != nil {
		t.Fatal(err)
	}
	dir := reviewImageDir(h.State, store.Snapshot().Reviews[0])
	s := &Service{Store: store, work: make(chan struct{}, 1), Options: Options{CEO: connection}}
	reopened := reregisterCEO(t, store, primary)
	s.cycle(ctx, false)

	// Act
	if _, err := store.Queue(Action{ID: "clear-ceo-document-1", Kind: "review_clear", Generation: reopened, ReviewID: "ceo-document-1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ProcessOne(ctx, s.execute); err != nil {
		t.Fatal(err)
	}
	_, pageErr := os.Stat(pages.pages[0])
	if err := store.settlePage(store.Snapshot().Reviews[0], Review{}); err != nil {
		t.Fatal(err)
	}
	pruneErr := store.pruneReviews(time.Now().Add(closedReviewRetention + time.Hour))

	// Assert
	if pageErr != nil || filepath.Dir(pages.pages[0]) != dir {
		t.Errorf("the page before pruning = %v in %s, want it in the item's folder %s", pageErr, pages.pages[0], dir)
	}
	if pruneErr != nil || len(store.Snapshot().Reviews) != 0 {
		t.Fatalf("pruning left %+v (%v), want the cleared item gone", store.Snapshot().Reviews, pruneErr)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the pruned item's directory still exists (%v)", err)
	}
}

// A run item the board made for the Overlord, a connection repair or a
// credential request's terminal, is not the CEO's: it does not follow the CEO,
// and the terminal still opens after the CEO restarted.
func TestARunTheBoardMadeDoesNotFollowTheCEO(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	primary, _, _, connection := primaryFixture(t, store)
	launcher := &fakeRunLauncher{started: liveStart(t)}
	s := &Service{Store: store, Instance: "test-instance", work: make(chan struct{}, 1), Options: Options{CEO: connection, Runs: launcher}}
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
	reregisterCEO(t, store, primary)

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
			t.Errorf("after the CEO restarted, run items read %+v, want %s still under the identity the board made it with", runs, made.ID)
		}
	}
	script := filepath.Join(runDir(store.Home.State, terminal), "command.ps1")
	if len(launcher.launches) != 1 || launcher.launches[0].Script != script {
		t.Errorf("launched %+v, want the credential terminal's script %s", launcher.launches, script)
	}
}

// An answer that waited for the CEO is delivered to the CEO registered now
// even when the delivery worker runs before the supervisor's next cycle.
func TestAWaitingAnswerIsDeliveredToTheReopenedCEOBeforeAnyCycle(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, closed, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CEO: connection}}
	question := Question{ID: "ceo-question-3", Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}
	if err := store.acceptQuestion(question); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queue(Action{ID: "answer-ceo-question-3", Kind: "ceo_answer", QuestionID: question.ID, Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	reregisterCEO(t, store, primary)

	// Act
	err := store.ProcessOne(context.Background(), s.execute)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := "User answer to CEO question ceo-question-3. Question: Pick a layout Answer: Board"
	if typed := ceo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], want) {
		t.Errorf("the reopened CEO received %q, want %q once", typed, want)
	}
	if answered := store.Snapshot().Questions[0]; answered.Status != "succeeded" {
		t.Errorf("the question reads %q (%s), want it answered", answered.Status, answered.Message)
	}
}

// A queued action follows the CEO only when the item it names did: a clear of
// a question that failed under the CEO before, which stays where it is, still
// clears it, while the answer to a question still open reaches the CEO now.
func TestAClearOfAQuestionThatDidNotFollowTheCEOStillClearsIt(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, closed, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CEO: connection}}
	for _, id := range []string{"ceo-question-failed", "ceo-question-open"} {
		if err := store.acceptQuestion(Question{ID: id, Identity: closed, Text: "Pick a layout", Options: []string{"Board", "Tree"}, CreatedAt: time.Now().UTC(), Status: "pending"}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.db.Questions[0].Status = "failed"
	store.mu.Unlock()
	if _, err := store.Queue(Action{ID: "answer-ceo-question-open", Kind: "ceo_answer", QuestionID: "ceo-question-open", Generation: closed, Text: "Board", AnswerKind: "option"}); err != nil {
		t.Fatal(err)
	}
	reregisterCEO(t, store, primary)
	if _, err := store.Queue(Action{ID: "clear-ceo-question-failed", Kind: "question_clear", QuestionID: "ceo-question-failed", Generation: closed}); err != nil {
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
	want := "User answer to CEO question ceo-question-open. Question: Pick a layout Answer: Board"
	if typed := ceo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], want) {
		t.Errorf("the reopened CEO received %q, want %q once", typed, want)
	}
	if questions[1].Status != "succeeded" {
		t.Errorf("the open question reads %q (%s), want it answered", questions[1].Status, questions[1].Message)
	}
}

// A run's result typed and submitted while the CEO was inside a turn is told:
// the CEO takes it at its next tool call, so it is never typed again.
func TestARunResultSubmittedBehindTheCEOsTurnIsToldOnce(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CEO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	r := readyRun(t, store, identity, "ceo-run-0004", "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-ceo-run-0004")
	if err := os.WriteFile(filepath.Join(runDir(store.Home.State, r), "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
	ceo.startTurn(t)
	ctx := context.Background()

	// Act
	finishErr := s.finishRuns(ctx)
	retellErr := errors.Join(s.retellRuns(ctx), s.retellRuns(ctx))

	// Assert
	if finishErr != nil || retellErr != nil {
		t.Fatalf("finishing = %v, telling again = %v, want neither to fail", finishErr, retellErr)
	}
	if typed := ceo.waitForLines(t, 1); len(typed) != 1 || !strings.Contains(typed[0], "Run item ceo-run-0004") {
		t.Errorf("the CEO in a turn was sent %q, want the result once", typed)
	}
	if told := store.Snapshot().Runs[0]; told.Untold != "" || told.Reason != "" {
		t.Errorf("the run item reads untold %q, reason %q, want it told with nothing noted", told.Untold, told.Reason)
	}
}

// finishedRun is a run item of the CEO registered as identity whose command
// has finished and that the supervisor has yet to end.
func finishedRun(t *testing.T, s *Service, identity, id string) {
	t.Helper()
	r := readyRun(t, s.Store, identity, id, "powershell", false, time.Now().UTC())
	pressRun(t, s, r, "run-"+id)
	if err := os.WriteFile(filepath.Join(runDir(s.Store.Home.State, r), "exit.txt"), []byte("0"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A run's result that was typed to the CEO before the send failed may have
// reached it: it is noted on the item and never typed again.
func TestARunResultThatFailedAfterItWasTypedIsNotTypedAgain(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	_, identity, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CEO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	finishedRun(t, s, identity, "ceo-run-0005")
	// The send ends between the typing and Enter, so the result is in the
	// CEO's composer and whether the CEO takes it is unknown.
	interrupted, interrupt := context.WithCancel(context.Background())
	interrupt()
	ctx := context.Background()

	// Act
	finishErr := s.finishRuns(interrupted)
	retellErr := errors.Join(s.retellRuns(ctx), s.retellRuns(ctx))
	ceo.typeLine(t, "")

	// Assert
	if finishErr != nil || retellErr != nil {
		t.Fatalf("finishing = %v, telling again = %v, want neither to fail", finishErr, retellErr)
	}
	if typed := ceo.waitForLines(t, 1); len(typed) != 1 || strings.Count(typed[0], "Run item ceo-run-0005") != 1 {
		t.Errorf("the CEO's composer held %q, want the result typed once", typed)
	}
	if noted := store.Snapshot().Runs[0]; noted.Untold != "" || !strings.Contains(noted.Reason, "the CEO could not be told") {
		t.Errorf("the run item reads untold %q, reason %q, want the uncertain delivery noted and nothing left to tell", noted.Untold, noted.Reason)
	}
}

// A run's result refused before anything was typed, here with no CEO
// registered, stays untold, and the CEO that registers next is told it once.
func TestARunResultRefusedBeforeItWasTypedIsToldOnceTheCEORuns(t *testing.T) {
	// Arrange
	store, _ := testStore(t)
	primary, identity, ceo, connection := primaryFixture(t, store)
	s := &Service{Store: store, Options: Options{CEO: connection, Runs: &fakeRunLauncher{started: liveStart(t)}}}
	finishedRun(t, s, identity, "ceo-run-0006")
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
		t.Errorf("with no CEO registered the tell = %v and the item's untold = %q, want it refused and still to tell", refused, waiting.Untold)
	}
	if retellErr != nil {
		t.Fatal(retellErr)
	}
	if typed := ceo.lines(t); len(typed) != 1 || !strings.Contains(typed[0], "Run item ceo-run-0006") {
		t.Errorf("the CEO that registered was sent %q, want the result once", typed)
	}
	if told := store.Snapshot().Runs[0]; told.Untold != "" || told.Reason != "" {
		t.Errorf("the run item reads untold %q, reason %q, want it told with nothing noted", told.Untold, told.Reason)
	}
}
