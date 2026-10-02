package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

const (
	ticketRepository = "fpresta0607/northwind-api"
	ticketPull       = "https://github.com/fpresta0607/northwind-api/pull/412"
)

var ticketNow = time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)

func TestTicketForFollowsTheTaskLifecycle(t *testing.T) {
	pull := tickets.PullRequestLink{Number: 412, URL: ticketPull}
	worked := &tickets.Record{Labels: []string{"cfo: pr open", "goblin: codex"}}
	cases := []struct {
		name   string
		task   Task
		record *tickets.Record
		want   tickets.Ticket
		wantOK bool
	}{
		{name: "queued", task: Task{Evaluation: Evaluation{Phase: "queued"}}, want: tickets.Ticket{State: tickets.Queued}, wantOK: true},
		{name: "working", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "working"}}, want: tickets.Ticket{State: tickets.InProgress, Harness: "claude"}, wantOK: true},
		{name: "in review before any pull request", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "review"}}, want: tickets.Ticket{State: tickets.InProgress, Harness: "claude"}, wantOK: true},
		{name: "a pull request opened", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "ready", PR: ticketPull}}, want: tickets.Ticket{State: tickets.PROpen, Harness: "claude", PullRequest: pull}, wantOK: true},
		{name: "reported done, its pull request still open", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "done", PR: ticketPull}}, want: tickets.Ticket{State: tickets.PROpen, Harness: "claude", PullRequest: pull}, wantOK: true},
		{name: "paused", task: Task{Harness: "pi", Evaluation: Evaluation{Phase: "paused", PR: ticketPull}}, want: tickets.Ticket{State: tickets.Paused, Harness: "pi", PullRequest: pull}, wantOK: true},
		{name: "blocked on a question", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "blocked", Reason: "Which database should the migration target?"}}, want: tickets.Ticket{State: tickets.Blocked, Harness: "claude", Reason: tickets.WaitingOnDecision}, wantOK: true},
		{name: "failed", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "failed", Reason: "the token for the staging database expired"}}, want: tickets.Ticket{State: tickets.Blocked, Harness: "claude", Reason: tickets.StoppedOnFailure}, wantOK: true},
		{name: "merged by the gate", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "merged", PR: ticketPull}}, want: tickets.Ticket{State: tickets.Merged, Harness: "claude", PullRequest: pull}, wantOK: true},
		{name: "finished and merged, harness from its record", task: Task{Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}, record: worked, want: tickets.Ticket{State: tickets.Merged, Harness: "codex", PullRequest: pull}, wantOK: true},
		{name: "finished, its pull request still open", task: Task{Archived: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}, record: worked, want: tickets.Ticket{State: tickets.PROpen, Harness: "codex", PullRequest: pull}, wantOK: true},
		{name: "its pull request closed without merging", task: Task{Archived: true, Closed: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}, record: worked, want: tickets.Ticket{State: tickets.Closed, Harness: "codex", PullRequest: pull, Reason: tickets.FinishedUnmerged}, wantOK: true},
		{name: "stopped", task: Task{Archived: true, Evaluation: Evaluation{Phase: "stopped", Reason: "The Overlord stopped it to free memory"}}, record: worked, want: tickets.Ticket{State: tickets.Closed, Harness: "codex", Reason: tickets.StoppedByCFO}, wantOK: true},
		{name: "merged with no pull request known", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "merged"}}},
		{name: "stopping, not stopped yet", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "stopping"}}},
		{name: "working with no harness known", task: Task{Evaluation: Evaluation{Phase: "working"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			tc.task.Title = "Say why a billing sync fails"
			tc.want.TaskID, tc.want.Title = "nw-sync", tc.task.Title

			// Act
			got, ok := ticketFor("nw-sync", tc.task, tc.record)

			// Assert
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (ticket %+v)", ok, tc.wantOK, got)
			}
			if ok && got != tc.want {
				t.Fatalf("ticket = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// fakeTicketWriter stands in for GitHub: it answers who works in a
// repository and applies a ticket the way tickets.GitHub.Apply reports it.
type fakeTicketWriter struct {
	mu                 sync.Mutex
	collaboration      tickets.Collaboration
	collaborationReads int
	labelled           []string
	applied            []appliedTicket
	applyErr           error
	refuseClaim        bool
}

type appliedTicket struct {
	repository string
	ticket     tickets.Ticket
	claim      int
	hadRecord  bool
}

func (f *fakeTicketWriter) writer(checkout string) *Tickets {
	return &Tickets{
		Checkout: func(project string) (string, error) { return checkout, nil },
		Repository: func(_ context.Context, got string) (string, error) {
			if got != checkout {
				return "", errors.New("unexpected checkout " + got)
			}
			return ticketRepository, nil
		},
		Collaboration: func(context.Context, string, time.Time) (tickets.Collaboration, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.collaborationReads++
			return f.collaboration, nil
		},
		EnsureLabels: func(_ context.Context, repository string) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.labelled = append(f.labelled, repository)
			return nil
		},
		Apply: func(_ context.Context, repository string, record *tickets.Record, ticket tickets.Ticket, claim int) (*tickets.Record, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.applied = append(f.applied, appliedTicket{repository, ticket, claim, record != nil})
			if f.applyErr != nil {
				return record, f.applyErr
			}
			next := tickets.Record{TaskID: ticket.TaskID, Repository: repository, Number: 501, State: ticket.State, Status: ticket.StatusLine(), Labels: ticket.Labels(), IsDone: !ticket.IsOpen()}
			if record != nil {
				next.Number = record.Number
			}
			if f.refuseClaim && claim > 0 {
				return &next, &tickets.ClaimRefused{Number: claim, Why: "it is closed"}
			}
			return &next, nil
		},
	}
}

// ticketHome is a home with one live task, nw-sync, whose project is a
// private collaborative repository's checkout.
func ticketHome(t *testing.T) (home.Home, string, *fakeTicketWriter) {
	t.Helper()
	dir := t.TempDir()
	h := home.Home{Root: dir, State: filepath.Join(dir, "state"), Data: filepath.Join(dir, "data")}
	checkout := filepath.Join(dir, "northwind-api")
	for _, path := range []string{h.State, h.Data, checkout} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "nw-sync", Project: checkout, Worktree: checkout, Harness: "claude", SpawnGen: "s1"}); err != nil {
		t.Fatal(err)
	}
	return h, checkout, &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true, IsPrivate: true}}
}

func liveTask(phase, pull string) Task {
	return Task{ID: "nw-sync", Title: "Say why a billing sync fails", Harness: "claude", Evaluation: Evaluation{Phase: phase, PR: pull}}
}

func TestKeeperOpensATicketForATaskInACollaborativeRepository(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), []Task{liveTask("working", "")}, ticketNow)

	// Assert
	if len(github.applied) != 1 || github.applied[0].hadRecord || github.applied[0].repository != ticketRepository || github.applied[0].ticket.State != tickets.InProgress || github.applied[0].claim != 0 {
		t.Fatalf("applied = %+v, want one ticket opened in progress", github.applied)
	}
	if !slices.Equal(github.labelled, []string{ticketRepository}) {
		t.Fatalf("labels ensured in %v, want the repository once", github.labelled)
	}
	record, err := tickets.ReadRecord(h.State, "nw-sync")
	if err != nil || record.Number != 501 || record.State != tickets.InProgress {
		t.Fatalf("record = %+v, %v, want the opened issue kept", record, err)
	}
	if issues := keeper.Issues(); len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestKeeperMovesTheTicketAsTheTaskMovesAndAsksGitHubWhoWorksThereOnceAnHour(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), []Task{liveTask("working", "")}, ticketNow)
	keeper.reconcile(context.Background(), []Task{liveTask("ready", ticketPull)}, ticketNow.Add(time.Minute))
	merged := liveTask("merged", ticketPull)
	keeper.reconcile(context.Background(), []Task{merged}, ticketNow.Add(2*time.Minute))
	keeper.reconcile(context.Background(), []Task{merged}, ticketNow.Add(3*time.Minute))

	// Assert
	var states []tickets.State
	for _, applied := range github.applied {
		states = append(states, applied.ticket.State)
	}
	if !slices.Equal(states, []tickets.State{tickets.InProgress, tickets.PROpen, tickets.Merged}) {
		t.Fatalf("states applied = %v, want each move once and nothing after the ticket is done", states)
	}
	if !github.applied[1].hadRecord || !github.applied[2].hadRecord {
		t.Fatalf("applied = %+v, want every move after the first to carry the record", github.applied)
	}
	if record, _ := tickets.ReadRecord(h.State, "nw-sync"); !record.IsDone || record.State != tickets.Merged {
		t.Fatalf("record = %+v, want it done and merged", record)
	}
	if github.collaborationReads != 1 || len(github.labelled) != 1 {
		t.Fatalf("collaboration reads = %d, label passes = %d, want one each: a task with a record asks nothing more", github.collaborationReads, len(github.labelled))
	}
}

func TestKeeperReadsWhoWorksInARepositoryAgainAfterAnHour(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	github.collaboration.IsCollaborative = false
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(59*time.Minute))
	readsWithinTheHour := github.collaborationReads
	github.collaboration.IsCollaborative = true
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(61*time.Minute))

	// Assert
	if readsWithinTheHour != 1 || github.collaborationReads != 2 {
		t.Fatalf("collaboration reads = %d within the hour and %d after it, want 1 and 2", readsWithinTheHour, github.collaborationReads)
	}
	if len(github.applied) != 1 {
		t.Fatalf("applied = %+v, want no ticket while only the Overlord works there and one once a teammate does", github.applied)
	}
}

func TestKeeperHoldsTicketsInAPublicRepositoryUntilTheCFOAllowsThem(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	github.collaboration.IsPrivate = false
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	held, heldIssues := len(github.applied), keeper.Issues()
	if err := tickets.AllowPublic(h.State, ticketRepository); err != nil {
		t.Fatal(err)
	}
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(time.Minute))

	// Assert
	if held != 0 || len(heldIssues) != 1 || !strings.Contains(heldIssues[0], ticketRepository) || !strings.Contains(heldIssues[0], "--allow-public-tickets") {
		t.Fatalf("applied %d, issues %v, want nothing opened and one line naming the repository and the command", held, heldIssues)
	}
	if len(github.applied) != 1 || len(keeper.Issues()) != 0 {
		t.Fatalf("applied = %+v, issues = %v, want the ticket opened and the hold gone once allowed", github.applied, keeper.Issues())
	}
}

func TestKeeperClaimsTheIssueTheBriefNames(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	writeFile(t, filepath.Join(h.Data, "nw-sync", "brief.md"), "# Brief nw-sync\n\n## Task\n\nResolve issue #415, which a teammate filed.\n")
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), []Task{liveTask("working", "")}, ticketNow)

	// Assert
	if len(github.applied) != 1 || github.applied[0].claim != 415 {
		t.Fatalf("applied = %+v, want the brief's issue claimed", github.applied)
	}
}

func TestKeeperKeepsTheRecordOfARefusedClaimAndShowsTheRefusal(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	github.refuseClaim = true
	writeFile(t, filepath.Join(h.Data, "nw-sync", "brief.md"), "## Task\n\nResolve issue #415.\n")
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(time.Minute))

	// Assert
	if record, err := tickets.ReadRecord(h.State, "nw-sync"); err != nil || record.Number != 501 {
		t.Fatalf("record = %+v, %v, want the issue opened instead kept although Apply returned an error", record, err)
	}
	if github.applied[1].claim != 0 {
		t.Fatalf("second pass claim = %d, want no second claim once the task has a record", github.applied[1].claim)
	}
	if issues := keeper.Issues(); len(issues) != 1 || !strings.Contains(issues[0], "nw-sync") || !strings.Contains(issues[0], "#415") {
		t.Fatalf("issues = %v, want the refusal still shown after a later clean pass", issues)
	}
}

func TestKeeperShowsAFailedWriteUntilItSucceeds(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}
	github.applyErr = &tickets.APIError{Status: 502}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	failed := keeper.Issues()
	github.applyErr = nil
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(time.Minute))

	// Assert
	if len(failed) != 1 || !strings.Contains(failed[0], "nw-sync") || !strings.Contains(failed[0], "502") {
		t.Fatalf("issues = %v, want the task and GitHub's answer", failed)
	}
	if len(github.applied) != 2 || len(keeper.Issues()) != 0 {
		t.Fatalf("applied = %d, issues = %v, want a retry on the next pass and the line gone", len(github.applied), keeper.Issues())
	}
}

func TestKeeperBacksOffARepositoryGitHubRefused(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}
	github.applyErr = &tickets.APIError{Status: 403}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(30*time.Minute))
	duringBackOff := len(github.applied)
	github.applyErr = nil
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(61*time.Minute))

	// Assert
	if duringBackOff != 1 {
		t.Fatalf("writes within the hour after a 403 = %d, want 1: the repository waits", duringBackOff)
	}
	if len(github.applied) != 2 || len(keeper.Issues()) != 0 {
		t.Fatalf("applied = %d, issues = %v, want one retry after the hour", len(github.applied), keeper.Issues())
	}
}

func TestKeeperLeavesAloneWhatHasNoTicketToKeep(t *testing.T) {
	cases := []struct {
		name string
		task Task
	}{
		{name: "a finished task that never had a ticket", task: Task{ID: "finished:nw-old", Title: "Old work", Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}},
		{name: "a pull request merged outside any task", task: Task{ID: "merged:" + ticketPull, Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}},
		{name: "a task whose project nothing names", task: Task{ID: "nw-nowhere", Title: "No project", Evaluation: Evaluation{Phase: "queued"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, checkout, github := ticketHome(t)
			// The finished task's brief still names its project, so only its
			// being finished keeps GitHub from being asked about it.
			writeFile(t, filepath.Join(h.Data, "nw-old", "brief.md"), "## Project\n\n"+checkout+"\n\n## Task\n\nOld work.\n")
			keeper := newTicketKeeper(h, github.writer(checkout))

			keeper.reconcile(context.Background(), []Task{tc.task}, ticketNow)

			if len(github.applied) != 0 || github.collaborationReads != 0 {
				t.Fatalf("applied = %+v, collaboration reads = %d, want GitHub never asked", github.applied, github.collaborationReads)
			}
		})
	}
}

func TestKeeperClosesTheTicketOfAFinishedTaskFromItsRecord(t *testing.T) {
	// Arrange: the task was cleaned up, so no task record or brief names its
	// project any more; its ticket record does.
	h, checkout, github := ticketHome(t)
	if err := state.RemoveTaskMeta(h.State, "nw-sync"); err != nil {
		t.Fatal(err)
	}
	if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "nw-sync", Repository: ticketRepository, Number: 501, State: tickets.PROpen, Status: "PR open: #412", Labels: []string{"cfo: pr open", "goblin: claude"}}); err != nil {
		t.Fatal(err)
	}
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), []Task{{ID: "finished:nw-sync", Title: "Say why a billing sync fails", Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}}, ticketNow)

	// Assert
	if len(github.applied) != 1 || github.applied[0].ticket.State != tickets.Merged || github.applied[0].ticket.Harness != "claude" || github.applied[0].repository != ticketRepository {
		t.Fatalf("applied = %+v, want the ticket merged in its record's repository, still naming who worked it", github.applied)
	}
	if github.collaborationReads != 0 {
		t.Fatalf("collaboration reads = %d, want none for a task that already has a ticket", github.collaborationReads)
	}
}

func (f *fakeTicketWriter) appliedSoFar() []appliedTicket {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

func TestKeepTicketsFollowsTheBoardsOwnTasksAndWritesOncePerChange(t *testing.T) {
	// Arrange: the board's live task, task-1 on codex, in a collaborative
	// repository.
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	github := &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true, IsPrivate: true}}
	service := &Service{Store: store, tickets: newTicketKeeper(h, github.writer(filepath.Join(h.Root, "work")))}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Act
	go func() {
		defer close(done)
		service.keepTickets(ctx, time.Hour, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(20 * time.Second)
	for len(github.appliedSoFar()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	// Assert
	applied := github.appliedSoFar()
	if len(applied) != 1 {
		t.Fatalf("applied = %+v, want one write for the task and none on the passes where nothing moved", applied)
	}
	if applied[0].ticket.TaskID != "task-1" || applied[0].ticket.Harness != "codex" || !applied[0].ticket.IsOpen() {
		t.Fatalf("ticket = %+v, want the board's task-1 on codex, open", applied[0].ticket)
	}
}

func TestSnapshotShowsTheTicketsThatWait(t *testing.T) {
	// Arrange
	store, h := testStore(t)
	if err := os.MkdirAll(h.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	github := &fakeTicketWriter{collaboration: tickets.Collaboration{Repository: ticketRepository, IsCollaborative: true}}
	service := &Service{Store: store, tickets: newTicketKeeper(h, github.writer(filepath.Join(h.Root, "work")))}
	before, err := service.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	// Act
	service.tickets.reconcile(context.Background(), before.Tasks, ticketNow)
	after, err := service.Snapshot()

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Issues) != len(before.Issues)+1 || !strings.Contains(after.Issues[len(after.Issues)-1], "--allow-public-tickets") {
		t.Fatalf("issues = %v, want the board to say the public repository's tickets are held", after.Issues)
	}
}
