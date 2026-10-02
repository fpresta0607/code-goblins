package supervisor

import (
	"context"
	"errors"
	"fmt"
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
		{name: "pausing, not paused yet", task: Task{Harness: "claude", Evaluation: Evaluation{Phase: "pausing"}}},
		{name: "working with no harness known", task: Task{Evaluation: Evaluation{Phase: "working"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			tc.task.Title = "The board's own title, which may come from the brief"
			tc.want.TaskID, tc.want.Title = "nw-sync", "Say why a billing sync fails"

			// Act
			got, ok := ticketFor("nw-sync", "Say why a billing sync fails", tc.task, tc.record)

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
	collaborationErr   error
	collaborationReads int
	checkoutErr        error
	checkoutReads      int
	repositoryErr      error
	repositoryReads    int
	pulls              map[string]string
	pullAsks           int
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
		Checkout: func(project string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.checkoutReads++
			return checkout, f.checkoutErr
		},
		Repository: func(_ context.Context, got string) (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.repositoryReads++
			if got != checkout {
				return "", errors.New("unexpected checkout " + got)
			}
			return ticketRepository, f.repositoryErr
		},
		PullRequestState: func(_ context.Context, url string) (PullRequestInfo, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.pullAsks++
			state, ok := f.pulls[url]
			if !ok {
				return PullRequestInfo{}, errors.New("no such pull request " + url)
			}
			return PullRequestInfo{State: state, Title: "a pull request"}, nil
		},
		Collaboration: func(context.Context, string, time.Time) (tickets.Collaboration, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.collaborationReads++
			return f.collaboration, f.collaborationErr
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
			next := tickets.Record{TaskID: ticket.TaskID, Repository: repository, Number: 501}
			if record != nil {
				next = *record
			}
			next.State, next.Status, next.Labels, next.IsDone = ticket.State, ticket.StatusLine(), ticket.Labels(), !ticket.IsOpen()
			if ticket.Title != "" {
				next.Title = ticket.Title
			}
			if ticket.PullRequest.URL != "" {
				next.PullRequest = ticket.PullRequest.URL
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
	if held != 0 || len(heldIssues) != 1 || !strings.Contains(heldIssues[0], ticketRepository) || !strings.Contains(heldIssues[0], "cfo tickets \""+checkout+"\" --allow-public-tickets") {
		t.Fatalf("applied %d, issues %v, want nothing opened and one line naming the repository and the command for the project as the task names it", held, heldIssues)
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
	duringBackOff, shownDuringBackOff := len(github.applied), keeper.Issues()
	github.applyErr = nil
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(61*time.Minute))

	// Assert
	if duringBackOff != 1 {
		t.Fatalf("writes within the hour after a 403 = %d, want 1: the repository waits", duringBackOff)
	}
	if len(shownDuringBackOff) != 1 || !strings.Contains(shownDuringBackOff[0], ticketRepository) || !strings.Contains(shownDuringBackOff[0], "wait until") {
		t.Fatalf("issues during the wait = %v, want the board to keep saying the repository waits and until when", shownDuringBackOff)
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

func TestKeeperGivesWorkQueuedBeforeItFirstRanItsTicketWhenItStarts(t *testing.T) {
	// Arrange: nw-old is queued when the keeper first runs; nw-new is queued
	// later. Both briefs name the collaborative repository's checkout.
	h, checkout, github := ticketHome(t)
	for _, id := range []string{"nw-old", "nw-new"} {
		writeFile(t, filepath.Join(h.Data, id, "brief.md"), "## Project\n\n"+checkout+"\n\n## Task\n\nQueued work.\n")
	}
	keeper := newTicketKeeper(h, github.writer(checkout))
	queued := func(id string) Task {
		return Task{ID: id, Title: "Queued work " + id, Evaluation: Evaluation{Phase: "queued"}}
	}

	// Act
	keeper.reconcile(context.Background(), []Task{queued("nw-old")}, ticketNow)
	atFirstRun := len(github.applied)
	keeper.reconcile(context.Background(), []Task{queued("nw-old"), queued("nw-new")}, ticketNow.Add(time.Minute))
	afterNewWork := slices.Clone(github.applied)
	started := queued("nw-old")
	started.Harness, started.Phase = "claude", "working"
	keeper.reconcile(context.Background(), []Task{queued("nw-new"), started}, ticketNow.Add(2*time.Minute))

	// Assert
	if atFirstRun != 0 {
		t.Fatalf("applied at the first run = %+v, want no ticket for work already queued then", github.applied)
	}
	if len(afterNewWork) != 1 || afterNewWork[0].ticket.TaskID != "nw-new" || afterNewWork[0].ticket.State != tickets.Queued {
		t.Fatalf("applied = %+v, want a queued ticket for the work queued afterwards and still none for the old", afterNewWork)
	}
	last := github.applied[len(github.applied)-1]
	if last.ticket.TaskID != "nw-old" || last.ticket.State != tickets.InProgress || last.hadRecord {
		t.Fatalf("last applied = %+v, want the old work's ticket opened in progress once it starts", last)
	}
}

func TestKeeperRemembersTheWorkQueuedBeforeItAcrossARestart(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	writeFile(t, filepath.Join(h.Data, "nw-old", "brief.md"), "## Project\n\n"+checkout+"\n\n## Task\n\nQueued work.\n")
	tasks := []Task{{ID: "nw-old", Title: "Queued work", Evaluation: Evaluation{Phase: "queued"}}}
	newTicketKeeper(h, github.writer(checkout)).reconcile(context.Background(), tasks, ticketNow)

	// Act: a new supervisor starts with the same work still queued.
	newTicketKeeper(h, github.writer(checkout)).reconcile(context.Background(), tasks, ticketNow.Add(time.Hour))

	// Assert
	if len(github.applied) != 0 {
		t.Fatalf("applied = %+v, want the old queued work still without a ticket after a restart", github.applied)
	}
}

func TestKeeperTitlesATicketFromTheBacklogOrTheTaskRecordNeverFromTheBoard(t *testing.T) {
	// The board titles a task with no row from the first line of its brief,
	// and a brief's text must never reach a teammate's repository.
	fromTheBrief := "Diagnose the outage in the billing database of the paying customer"
	cases := []struct {
		name      string
		arrange   func(t *testing.T, h home.Home, checkout string)
		task      Task
		wantTitle string
	}{
		{name: "the title the task was dispatched under", task: Task{ID: "nw-sync", Title: fromTheBrief, Harness: "claude", Evaluation: Evaluation{Phase: "working"}},
			arrange: func(t *testing.T, h home.Home, checkout string) {
				if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "nw-sync", Project: checkout, Worktree: checkout, Harness: "claude", SpawnGen: "s1", Title: "Say why a billing sync fails"}); err != nil {
					t.Fatal(err)
				}
			}, wantTitle: "Say why a billing sync fails"},
		{name: "its backlog row's title", task: Task{ID: "nw-queued", Title: fromTheBrief, Evaluation: Evaluation{Phase: "queued"}},
			arrange: func(t *testing.T, h home.Home, checkout string) {
				writeFile(t, filepath.Join(h.Data, "nw-queued", "brief.md"), "## Project\n\n"+checkout+"\n\n## Task\n\n"+fromTheBrief+".\n")
			}, wantTitle: "Refund totals in the export"},
		{name: "the task's id when nothing titles it", task: Task{ID: "nw-sync", Title: fromTheBrief, Harness: "claude", Evaluation: Evaluation{Phase: "working"}}, wantTitle: "nw-sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: the keeper has already run once, with nothing queued.
			h, checkout, github := ticketHome(t)
			keeper := newTicketKeeper(h, github.writer(checkout))
			keeper.reconcile(context.Background(), nil, ticketNow)
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] nw-queued - Refund totals in the export (repo: northwind-api)\n")
			if tc.arrange != nil {
				tc.arrange(t, h, checkout)
			}

			// Act
			keeper.reconcile(context.Background(), []Task{tc.task}, ticketNow.Add(time.Minute))

			// Assert
			if len(github.applied) != 1 || github.applied[0].ticket.Title != tc.wantTitle {
				t.Fatalf("applied = %+v, want one ticket titled %q", github.applied, tc.wantTitle)
			}
		})
	}
}

func TestKeeperKeepsATicketsTitleWhenNothingTitlesTheTaskAnyMore(t *testing.T) {
	// Arrange: the task was cleaned up; its ticket was opened under its
	// dispatch title, which no record holds any more.
	h, checkout, github := ticketHome(t)
	if err := state.RemoveTaskMeta(h.State, "nw-sync"); err != nil {
		t.Fatal(err)
	}
	if err := tickets.WriteRecord(h.State, tickets.Record{TaskID: "nw-sync", Repository: ticketRepository, Number: 501, State: tickets.PROpen, Status: "PR open: #412", Labels: []string{"cfo: pr open", "goblin: claude"}, Title: "Say why a billing sync fails"}); err != nil {
		t.Fatal(err)
	}
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), []Task{{ID: "finished:nw-sync", Title: "fix(sync): say why", Archived: true, Merged: true, Evaluation: Evaluation{Phase: "done", PR: ticketPull}}}, ticketNow)

	// Assert
	if len(github.applied) != 1 || github.applied[0].ticket.Title != "Say why a billing sync fails" {
		t.Fatalf("applied = %+v, want the ticket to keep the title it has rather than fall back to the task's id", github.applied)
	}
}

func orphanRecord(pull string) tickets.Record {
	record := tickets.Record{TaskID: "nw-gone", Repository: ticketRepository, Number: 501, State: tickets.InProgress, Status: "In progress: goblin nw-gone on claude", Labels: []string{"cfo: in progress", "goblin: claude"}, Title: "Old work", PullRequest: pull}
	if pull != "" {
		record.State, record.Status, record.Labels = tickets.PROpen, "PR open: #412", []string{"cfo: pr open", "goblin: claude"}
	}
	return record
}

func TestKeeperMovesATicketWhoseTaskHasLeftTheBoard(t *testing.T) {
	cases := []struct {
		name       string
		pull       string
		answer     string
		wantState  tickets.State
		wantReason tickets.Reason
		wantAsks   int
	}{
		{name: "its pull request merged", pull: ticketPull, answer: "MERGED", wantState: tickets.Merged, wantAsks: 1},
		{name: "its pull request was closed unmerged", pull: ticketPull, answer: "CLOSED", wantState: tickets.Closed, wantReason: tickets.FinishedUnmerged, wantAsks: 1},
		{name: "it never had a pull request", wantState: tickets.Closed, wantReason: tickets.LeftTheFleet},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: a ticket record for a task nothing in the fleet names.
			h, checkout, github := ticketHome(t)
			github.pulls = map[string]string{ticketPull: tc.answer}
			if err := tickets.WriteRecord(h.State, orphanRecord(tc.pull)); err != nil {
				t.Fatal(err)
			}
			keeper := newTicketKeeper(h, github.writer(checkout))

			// Act
			keeper.reconcile(context.Background(), nil, ticketNow)
			keeper.reconcile(context.Background(), nil, ticketNow.Add(ticketGoneGrace))

			// Assert
			if len(github.applied) != 1 || github.applied[0].ticket.TaskID != "nw-gone" || github.applied[0].ticket.State != tc.wantState || github.applied[0].ticket.Reason != tc.wantReason {
				t.Fatalf("applied = %+v, want the ticket moved to %s (%s)", github.applied, tc.wantState, tc.wantReason)
			}
			if applied := github.applied[0].ticket; applied.Harness != "claude" || applied.Title != "Old work" || (tc.pull != "" && applied.PullRequest.Number != 412) {
				t.Fatalf("ticket = %+v, want who worked it, its title and its pull request from the record", applied)
			}
			if github.pullAsks != tc.wantAsks {
				t.Fatalf("pull request asks = %d, want %d", github.pullAsks, tc.wantAsks)
			}
			if record, _ := tickets.ReadRecord(h.State, "nw-gone"); !record.IsDone {
				t.Fatalf("record = %+v, want it done", record)
			}
		})
	}
}

func TestKeeperAsksAboutAnOpenPullRequestOfAGoneTaskOnceAnHour(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	github.pulls = map[string]string{ticketPull: "OPEN"}
	if err := tickets.WriteRecord(h.State, orphanRecord(ticketPull)); err != nil {
		t.Fatal(err)
	}
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), nil, ticketNow)
	gone := ticketNow.Add(ticketGoneGrace)
	keeper.reconcile(context.Background(), nil, gone)
	keeper.reconcile(context.Background(), nil, gone.Add(30*time.Minute))
	withinTheHour := github.pullAsks
	keeper.reconcile(context.Background(), nil, gone.Add(61*time.Minute))

	// Assert
	if withinTheHour != 1 || github.pullAsks != 2 || len(github.applied) != 0 {
		t.Fatalf("asks = %d within the hour and %d after it, applied = %+v, want 1, 2 and nothing written while the pull request is open", withinTheHour, github.pullAsks, github.applied)
	}
}

func TestKeeperLeavesATicketAloneUntilItsTaskHasBeenOffTheBoardForTheGracePeriod(t *testing.T) {
	back := func(pull string) []Task {
		phase := "working"
		if pull != "" {
			phase = "ready"
		}
		return []Task{{ID: "nw-gone", Title: "Old work", Harness: "claude", Evaluation: Evaluation{Phase: phase, PR: pull}}}
	}
	type pass struct {
		after        time.Duration
		isOnTheBoard bool
	}
	short := []pass{{}, {after: ticketGoneGrace - time.Minute}}
	returned := []pass{{}, {after: ticketGoneGrace / 2, isOnTheBoard: true}, {after: ticketGoneGrace + time.Minute}}
	cases := []struct {
		name   string
		pull   string
		passes []pass
	}{
		{name: "off the board for less than the period, no pull request", passes: short},
		{name: "off the board for less than the period, a pull request merged", pull: ticketPull, passes: short},
		{name: "back on the board within the period, no pull request", passes: returned},
		{name: "back on the board within the period, a pull request merged", pull: ticketPull, passes: returned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: a task just cleaned up, on neither of the board's lists
			// until its history is rebuilt.
			h, checkout, github := ticketHome(t)
			github.pulls = map[string]string{ticketPull: "MERGED"}
			want := orphanRecord(tc.pull)
			if err := tickets.WriteRecord(h.State, want); err != nil {
				t.Fatal(err)
			}
			keeper := newTicketKeeper(h, github.writer(checkout))

			// Act
			for _, pass := range tc.passes {
				var tasks []Task
				if pass.isOnTheBoard {
					tasks = back(tc.pull)
				}
				keeper.reconcile(context.Background(), tasks, ticketNow.Add(pass.after))
			}

			// Assert
			for _, applied := range github.applied {
				if !applied.ticket.IsOpen() || applied.ticket.State != want.State {
					t.Fatalf("applied = %+v, want the ticket left %s", github.applied, want.State)
				}
			}
			if github.pullAsks != 0 {
				t.Fatalf("pull request asks = %d, want GitHub not asked", github.pullAsks)
			}
			if record, err := tickets.ReadRecord(h.State, "nw-gone"); err != nil || record.IsDone || record.State != want.State || record.Status != want.Status {
				t.Fatalf("record = %+v, %v, want it untouched", record, err)
			}
		})
	}
}

func TestKeeperLeavesATicketAloneWhileTheFleetStillNamesItsTask(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, h home.Home, checkout string)
	}{
		{name: "it still runs", arrange: func(t *testing.T, h home.Home, checkout string) {
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "nw-gone", Project: checkout, Worktree: checkout, Harness: "claude", SpawnGen: "s1"}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "it still has its brief", arrange: func(t *testing.T, h home.Home, checkout string) {
			writeFile(t, filepath.Join(h.Data, "nw-gone", "brief.md"), "## Task\n\nOld work.\n")
		}},
		{name: "it is still in the backlog", arrange: func(t *testing.T, h home.Home, checkout string) {
			writeFile(t, filepath.Join(h.Data, "backlog.md"), "## Queued\n- [ ] nw-gone - Old work (repo: northwind-api)\n")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange: the board's list misses the task, as it does past its
			// size limit, but the fleet still has it.
			h, checkout, github := ticketHome(t)
			tc.arrange(t, h, checkout)
			if err := tickets.WriteRecord(h.State, orphanRecord("")); err != nil {
				t.Fatal(err)
			}
			keeper := newTicketKeeper(h, github.writer(checkout))

			// Act
			keeper.reconcile(context.Background(), nil, ticketNow)

			// Assert
			if len(github.applied) != 0 || github.pullAsks != 0 {
				t.Fatalf("applied = %+v, asks = %d, want the ticket untouched", github.applied, github.pullAsks)
			}
		})
	}
}

func TestKeeperSaysNothingAboutAProjectWithNoGitHubRepository(t *testing.T) {
	// Arrange
	h, checkout, github := ticketHome(t)
	github.repositoryErr = fmt.Errorf("read the origin remote of %s: %w", checkout, tickets.ErrNotGitHub)
	keeper := newTicketKeeper(h, github.writer(checkout))
	tasks := []Task{liveTask("working", "")}

	// Act
	keeper.reconcile(context.Background(), tasks, ticketNow)
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(30*time.Minute))
	withinTheHour := github.repositoryReads
	keeper.reconcile(context.Background(), tasks, ticketNow.Add(61*time.Minute))

	// Assert
	if issues := keeper.Issues(); len(issues) != 0 {
		t.Fatalf("issues = %v, want none: a project with no GitHub repository never has tickets", issues)
	}
	if withinTheHour != 1 || github.repositoryReads != 2 || len(github.applied) != 0 {
		t.Fatalf("origin reads = %d within the hour and %d after it, applied = %+v, want 1, 2 and no ticket", withinTheHour, github.repositoryReads, github.applied)
	}
}

func TestKeeperShowsAProjectItCannotResolveAndAGitHubReadThatFailed(t *testing.T) {
	cases := []struct {
		name      string
		change    func(*fakeTicketWriter)
		want      string
		wantRetry time.Duration
	}{
		{name: "the project's checkout cannot be found", change: func(f *fakeTicketWriter) {
			f.checkoutErr = errors.New("project \"northwind-api\" is not under the projects root")
		}, want: "not under the projects root", wantRetry: time.Hour},
		{name: "GitHub did not answer who works there", change: func(f *fakeTicketWriter) { f.collaborationErr = errors.New("gh api graphql exited 1: HTTP 502") }, want: "HTTP 502", wantRetry: 10 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h, checkout, github := ticketHome(t)
			tc.change(github)
			keeper := newTicketKeeper(h, github.writer(checkout))
			tasks := []Task{liveTask("working", "")}

			// Act
			keeper.reconcile(context.Background(), tasks, ticketNow)
			failed := keeper.Issues()
			keeper.reconcile(context.Background(), tasks, ticketNow.Add(tc.wantRetry-time.Minute))
			readsBeforeRetry := github.checkoutReads
			github.checkoutErr, github.collaborationErr = nil, nil
			keeper.reconcile(context.Background(), tasks, ticketNow.Add(tc.wantRetry+time.Minute))

			// Assert
			if len(failed) != 1 || !strings.Contains(failed[0], tc.want) {
				t.Fatalf("issues = %v, want one line naming %q", failed, tc.want)
			}
			if readsBeforeRetry != 1 || github.checkoutReads != 2 {
				t.Fatalf("project reads = %d before %s and %d after, want 1 and 2", readsBeforeRetry, tc.wantRetry, github.checkoutReads)
			}
			if len(github.applied) != 1 || len(keeper.Issues()) != 0 {
				t.Fatalf("applied = %+v, issues = %v, want the ticket opened and the line gone once it works", github.applied, keeper.Issues())
			}
		})
	}
}

func TestKeeperDropsALineOnceItsCauseIsGone(t *testing.T) {
	cases := []struct {
		name  string
		first func(*fakeTicketWriter)
		then  func(t *testing.T, h home.Home, f *fakeTicketWriter) ([]Task, time.Duration)
	}{
		{name: "a failed open whose task then left the board", first: func(f *fakeTicketWriter) { f.applyErr = &tickets.APIError{Status: 502} },
			then: func(t *testing.T, h home.Home, f *fakeTicketWriter) ([]Task, time.Duration) { return nil, time.Minute }},
		{name: "a held public repository only the Overlord works in now", first: func(f *fakeTicketWriter) { f.collaboration.IsPrivate = false },
			then: func(t *testing.T, h home.Home, f *fakeTicketWriter) ([]Task, time.Duration) {
				f.collaboration.IsCollaborative = false
				return []Task{liveTask("working", "")}, 61 * time.Minute
			}},
		{name: "a refused claim whose ticket is done", first: func(f *fakeTicketWriter) { f.refuseClaim = true },
			then: func(t *testing.T, h home.Home, f *fakeTicketWriter) ([]Task, time.Duration) {
				return []Task{liveTask("merged", ticketPull)}, time.Minute
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			h, checkout, github := ticketHome(t)
			writeFile(t, filepath.Join(h.Data, "nw-sync", "brief.md"), "## Task\n\nResolve issue #415.\n")
			tc.first(github)
			keeper := newTicketKeeper(h, github.writer(checkout))

			// Act
			keeper.reconcile(context.Background(), []Task{liveTask("working", "")}, ticketNow)
			shown := keeper.Issues()
			tasks, later := tc.then(t, h, github)
			keeper.reconcile(context.Background(), tasks, ticketNow.Add(later))

			// Assert
			if len(shown) != 1 {
				t.Fatalf("issues after the first pass = %v, want one line", shown)
			}
			if issues := keeper.Issues(); len(issues) != 0 {
				t.Fatalf("issues = %v, want the line gone with its cause", issues)
			}
		})
	}
}

func TestKeeperClosesTheTicketOfAFinishedTaskThatAgedOffTheBoardWithItsBriefStillOnDisk(t *testing.T) {
	// Arrange: cleanup leaves a finished task's brief and status log behind
	// until they are filed, so neither makes it a task the fleet still has.
	h, checkout, github := ticketHome(t)
	writeFile(t, filepath.Join(h.Data, "nw-gone", "brief.md"), "## Task\n\nOld work.\n")
	if err := state.AppendStatus(h.State, "nw-gone", "done: PR "+ticketPull); err != nil {
		t.Fatal(err)
	}
	github.pulls = map[string]string{ticketPull: "MERGED"}
	if err := tickets.WriteRecord(h.State, orphanRecord(ticketPull)); err != nil {
		t.Fatal(err)
	}
	keeper := newTicketKeeper(h, github.writer(checkout))

	// Act
	keeper.reconcile(context.Background(), nil, ticketNow)
	keeper.reconcile(context.Background(), nil, ticketNow.Add(ticketGoneGrace))

	// Assert
	if len(github.applied) != 1 || github.applied[0].ticket.State != tickets.Merged {
		t.Fatalf("applied = %+v, want the ticket closed by its merge", github.applied)
	}
}
