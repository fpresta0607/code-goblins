package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// Tickets is what the supervisor needs to keep a GitHub issue, a ticket, for
// each task in a repository other people work in. Without it no ticket is
// kept. The supervisor is the tickets' only writer: no command and no goblin
// touches them.
type Tickets struct {
	// Checkout turns a task's project, a path or a bare name, into its
	// checkout.
	Checkout func(project string) (string, error)
	// Repository names the GitHub repository a checkout's origin is.
	Repository func(ctx context.Context, checkout string) (string, error)
	// Collaboration reads whether other people work in a repository and
	// whether it is private.
	Collaboration func(ctx context.Context, repository string, now time.Time) (tickets.Collaboration, error)
	// EnsureLabels creates the ticket labels in a repository.
	EnsureLabels func(ctx context.Context, repository string) error
	// Apply moves one task's issue to its ticket.
	Apply func(ctx context.Context, repository string, record *tickets.Record, ticket tickets.Ticket, claim int) (*tickets.Record, error)
	// PullRequestState asks GitHub whether a pull request is OPEN, CLOSED or
	// MERGED, for a ticket whose task has left the board; without it such a
	// ticket waits.
	PullRequestState func(ctx context.Context, url string) (PullRequestInfo, error)
}

const (
	// collaborationRefresh is how long an answer about a project's
	// repository stands; a read GitHub did not answer is asked again after
	// ticketRetry.
	collaborationRefresh = time.Hour
	ticketRetry          = 10 * time.Minute
	// ticketBackOff is how long a repository waits after GitHub refused a
	// write with 403 or 429.
	ticketBackOff = time.Hour
	// ticketWatch is how often the keeper looks for a task that moved.
	ticketWatch = 15 * time.Second
	// ticketGoneGrace is how long a task must stay off the board before its
	// ticket is moved as a gone task's. The board's finished tasks come from
	// a history that is rebuilt on its own schedule and is empty when the
	// supervisor starts, so a task just cleaned up is briefly on neither of
	// the board's lists.
	ticketGoneGrace = ticketRetry
)

// ticketKeeper keeps each task's ticket where its task is. It reads the
// tasks the board shows, so a ticket follows the same lifecycle the Overlord
// sees, and it writes to GitHub only when a ticket's state changed.
type ticketKeeper struct {
	home   home.Home
	writer *Tickets

	repositories map[string]repositoryAnswer
	labelled     map[string]bool
	backOff      map[string]ticketWait
	askPullAfter map[string]ticketWait
	// offTheBoardSince is when each task with an open ticket was first seen
	// off the board, for as long as it stays off it.
	offTheBoardSince map[string]time.Time
	// rowTitles and queuedIDs are the backlog as the pass under way read it.
	rowTitles map[string]string
	queuedIDs map[string]bool
	// found are the problems the pass under way has met, by what they are
	// about. A pass starts with none, so a line lasts only while its cause
	// does.
	found map[string]string

	mu       sync.Mutex
	problems []string
}

// repositoryAnswer is what one project's repository answered, and when it is
// asked again.
type repositoryAnswer struct {
	tickets.Collaboration
	isNotGitHub bool
	err         error
	again       time.Time
}

// ticketWait is a wait the keeper holds, a repository GitHub asked it to
// leave alone or a pull request it asks about again later, and the line the
// board shows while it lasts, if any.
type ticketWait struct {
	until time.Time
	line  string
}

func newTicketKeeper(h home.Home, writer *Tickets) *ticketKeeper {
	return &ticketKeeper{home: h, writer: writer, repositories: map[string]repositoryAnswer{}, labelled: map[string]bool{}, backOff: map[string]ticketWait{}, askPullAfter: map[string]ticketWait{}, offTheBoardSince: map[string]time.Time{}}
}

// Issues are the lines the board shows for tickets that wait or failed, as
// the last pass found them.
func (k *ticketKeeper) Issues() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.problems)
}

// keepTickets moves tickets away from the loop, as keepHistory rebuilds the
// Completed column: whenever a task's place on the board changes, which it
// checks every watch, and otherwise every retry, so a write GitHub did not
// take is tried again. A pass with nothing to move asks GitHub nothing.
func (s *Service) keepTickets(ctx context.Context, retry, watch time.Duration) {
	ticker := time.NewTicker(watch)
	defer ticker.Stop()
	var mark string
	var reconciled time.Time
	for {
		if snapshot, err := s.Snapshot(); err == nil {
			if next := ticketMark(snapshot.Tasks); next != mark || time.Since(reconciled) >= retry {
				mark, reconciled = next, time.Now()
				before := s.tickets.Issues()
				s.tickets.reconcile(ctx, snapshot.Tasks, time.Now().UTC())
				if !slices.Equal(s.tickets.Issues(), before) {
					s.notify()
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ticketMark names everything about the tasks that a ticket shows, so it
// changes exactly when some ticket may have to move.
func ticketMark(tasks []Task) string {
	var mark strings.Builder
	for _, task := range tasks {
		fmt.Fprintf(&mark, "%s|%s|%s|%s|%s|%t|%t|%t;", task.ID, task.Title, task.Phase, task.PR, task.Harness, task.Merged, task.Closed, task.Archived)
	}
	return mark.String()
}

// reconcile moves every ticket to where its task is now: the tickets of the
// tasks the board shows, then the open tickets of tasks that have left it.
//
// Work that was already queued when tickets were first kept here gets its
// ticket when it starts, not now: a backlog of old queued work must not
// arrive in a teammate's repository as a burst of issues. Work queued later
// gets its ticket at once.
func (k *ticketKeeper) reconcile(ctx context.Context, tasks []Task, now time.Time) {
	k.found = map[string]string{}
	k.readBacklog()
	var queued []string
	for _, task := range tasks {
		if task.Phase == "queued" {
			queued = append(queued, task.ID)
		}
	}
	queuedBefore, err := tickets.QueuedBefore(k.home.State, queued)
	if err != nil {
		k.note("keeping", fmt.Sprintf("Tickets wait: what the supervisor remembers about them cannot be read or saved: %v", err))
		k.show()
		return
	}
	onTheBoard := map[string]bool{}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		id := strings.TrimPrefix(task.ID, "finished:")
		if state.ValidTaskID(id) != nil {
			continue
		}
		onTheBoard[id] = true
		k.reconcileTask(ctx, id, task, slices.Contains(queuedBefore, id), now)
	}
	k.reconcileGone(ctx, onTheBoard, now)
	if ctx.Err() == nil {
		k.show()
	}
}

// readBacklog reads the titles the backlog gives tasks and which tasks it
// still queues or parks. A backlog that cannot be read titles and queues
// nothing for this pass.
func (k *ticketKeeper) readBacklog() {
	k.rowTitles, k.queuedIDs = map[string]string{}, map[string]bool{}
	backlog, err := fleet.ReadBacklog(k.home)
	if err != nil {
		return
	}
	for _, row := range slices.Concat(backlog.Done, backlog.Parked, backlog.Queued) {
		if row.Structured && row.Title != "" {
			k.rowTitles[row.ID] = row.Title
		}
	}
	for _, row := range slices.Concat(backlog.Parked, backlog.Queued) {
		if row.Structured {
			k.queuedIDs[row.ID] = true
		}
	}
	for _, brief := range queuedBriefs(k.home) {
		k.queuedIDs[brief.ID] = true
	}
}

func (k *ticketKeeper) reconcileTask(ctx context.Context, id string, task Task, wasQueuedBefore bool, now time.Time) {
	var record *tickets.Record
	if kept, err := tickets.ReadRecord(k.home.State, id); err == nil {
		record = &kept
	} else if !errors.Is(err, os.ErrNotExist) {
		k.note("task:"+id, fmt.Sprintf("The ticket record of %s cannot be read: %v", id, err))
		return
	}
	if record != nil && record.IsDone {
		return
	}
	if task.Phase == "queued" && record != nil && record.State != tickets.Queued {
		k.noteOf(record)
		return
	}
	ticket, ok := ticketFor(id, k.title(id, record), task, record)
	if !ok {
		k.noteOf(record)
		return
	}
	k.move(ctx, id, record, ticket, wasQueuedBefore, now)
}

// reconcileGone moves the open tickets of tasks the board no longer shows.
// A finished task leaves the board after a week, or once twenty newer ones
// have finished, and its pull request may merge later; a queued task leaves
// it when its brief or row is removed. A task the fleet still runs or queues
// is only off the board's list, and its ticket stays as it is. Any other
// task is gone only once it has been off the board for ticketGoneGrace
// without a break.
func (k *ticketKeeper) reconcileGone(ctx context.Context, onTheBoard map[string]bool, now time.Time) {
	records, err := tickets.ListRecords(k.home.State)
	if err != nil {
		k.note("records", fmt.Sprintf("The ticket records cannot be read: %v", err))
		return
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return
		}
		if record.IsDone || onTheBoard[record.TaskID] {
			delete(k.offTheBoardSince, record.TaskID)
			continue
		}
		if k.queuedIDs[record.TaskID] || exists(filepath.Join(k.home.State, record.TaskID+".meta")) {
			delete(k.offTheBoardSince, record.TaskID)
			k.noteOf(&record)
			continue
		}
		since, wasOff := k.offTheBoardSince[record.TaskID]
		if !wasOff {
			since = now
			k.offTheBoardSince[record.TaskID] = now
		}
		if now.Sub(since) < ticketGoneGrace {
			k.noteOf(&record)
			continue
		}
		ticket := tickets.Ticket{TaskID: record.TaskID, Title: record.Title, Harness: record.Harness(), PullRequest: pullRequestLink(record.PullRequest)}
		if ticket.PullRequest.Number == 0 {
			ticket.State, ticket.Reason = tickets.Closed, tickets.LeftTheFleet
		} else {
			switch k.pullRequestState(ctx, record, now) {
			case "MERGED":
				ticket.State = tickets.Merged
			case "CLOSED":
				ticket.State, ticket.Reason = tickets.Closed, tickets.FinishedUnmerged
			default:
				k.noteOf(&record)
				continue
			}
		}
		k.move(ctx, record.TaskID, &record, ticket, false, now)
	}
}

// pullRequestState asks GitHub what became of a gone task's pull request,
// once an hour while it is open and after ticketRetry when GitHub did not
// answer; in between, and without a way to ask, it is "". The wait after a
// failed read keeps its line on the board.
func (k *ticketKeeper) pullRequestState(ctx context.Context, record tickets.Record, now time.Time) string {
	if k.writer.PullRequestState == nil {
		return ""
	}
	if wait := k.askPullAfter[record.TaskID]; now.Before(wait.until) {
		if wait.line != "" {
			k.note("task:"+record.TaskID, wait.line)
		}
		return ""
	}
	answer, err := k.writer.PullRequestState(ctx, record.PullRequest)
	if err != nil {
		wait := ticketWait{until: now.Add(ticketRetry), line: fmt.Sprintf("The ticket for %s waits: its pull request could not be read: %v", record.TaskID, err)}
		k.askPullAfter[record.TaskID] = wait
		k.note("task:"+record.TaskID, wait.line)
		return ""
	}
	k.askPullAfter[record.TaskID] = ticketWait{until: now.Add(collaborationRefresh)}
	return answer.State
}

// move writes a task's ticket through GitHub and keeps what it then holds.
func (k *ticketKeeper) move(ctx context.Context, id string, record *tickets.Record, ticket tickets.Ticket, wasQueuedBefore bool, now time.Time) {
	repository, claim := "", 0
	if record != nil {
		repository = record.Repository
	} else {
		if !ticket.IsOpen() || ticket.State == tickets.Queued && wasQueuedBefore {
			return
		}
		if repository = k.ticketedRepository(ctx, id, now); repository == "" {
			return
		}
		claim = k.claimedIssue(id, repository)
	}
	if wait, isWaiting := k.backOff[repository]; isWaiting && now.Before(wait.until) {
		k.note("repository:"+repository, wait.line)
		k.noteOf(record)
		return
	}
	if !k.labelled[repository] {
		if err := k.writer.EnsureLabels(ctx, repository); err != nil {
			k.refused(id, repository, err, now)
			k.noteOf(record)
			return
		}
		k.labelled[repository] = true
	}
	next, err := k.writer.Apply(ctx, repository, record, ticket, claim)
	var refusedClaim *tickets.ClaimRefused
	if errors.As(err, &refusedClaim) && next != nil {
		noted := *next
		noted.Note = fmt.Sprintf("The ticket for %s: %v", id, err)
		next, err = &noted, nil
	}
	if next != nil && next != record {
		if writeErr := tickets.WriteRecord(k.home.State, *next); writeErr != nil {
			k.note("task:"+id, fmt.Sprintf("The ticket record of %s cannot be saved: %v", id, writeErr))
			return
		}
	}
	if err != nil {
		k.refused(id, repository, err, now)
	}
	k.noteOf(next)
}

// refused records a write GitHub did not take: the task's line on the board,
// and an hour's wait for the whole repository when GitHub asked for one.
func (k *ticketKeeper) refused(id, repository string, err error, now time.Time) {
	if tickets.ShouldBackOff(err) {
		wait := ticketWait{until: now.Add(ticketBackOff)}
		wait.line = fmt.Sprintf("GitHub refused a ticket write in %s, so its tickets wait until %s: %v", repository, wait.until.UTC().Format("15:04Z"), err)
		k.backOff[repository] = wait
		k.note("repository:"+repository, wait.line)
		return
	}
	k.note("task:"+id, fmt.Sprintf("The ticket for %s could not be written and is tried again: %v", id, err))
}

// ticketedRepository names the repository a task without a ticket gets one
// in, or "" when it gets none: its project is unknown or has no GitHub
// repository, only the Overlord works in its repository, or the repository
// is public and not allowed.
func (k *ticketKeeper) ticketedRepository(ctx context.Context, id string, now time.Time) string {
	project := k.project(id)
	if project == "" {
		return ""
	}
	known, ok := k.repositories[project]
	if !ok || !now.Before(known.again) {
		known = k.readRepository(ctx, project, now)
		k.repositories[project] = known
	}
	switch {
	case known.isNotGitHub:
		return ""
	case known.err != nil:
		k.note("project:"+project, fmt.Sprintf("Tickets for %s wait: %v", project, known.err))
		return ""
	case !known.IsCollaborative:
		return ""
	}
	if !known.IsPrivate {
		isAllowed, err := tickets.IsPublicAllowed(k.home.State, known.Repository)
		if err != nil {
			k.note("public:"+known.Repository, fmt.Sprintf("Tickets in %s wait: %v", known.Repository, err))
			return ""
		}
		if !isAllowed {
			k.note("public:"+known.Repository, fmt.Sprintf("Tickets in %s are held: it is public, and an issue there is public. Run cfo tickets \"%s\" --allow-public-tickets to keep them there.", known.Repository, project))
			return ""
		}
	}
	return known.Repository
}

// readRepository asks what a project's repository is and who works in it.
// A project with no GitHub repository never has tickets, which is not a
// problem to show. An answer stands an hour; only a read GitHub itself did
// not answer is asked again sooner.
func (k *ticketKeeper) readRepository(ctx context.Context, project string, now time.Time) repositoryAnswer {
	answer := repositoryAnswer{again: now.Add(collaborationRefresh)}
	checkout, err := k.writer.Checkout(project)
	if err != nil {
		answer.err = err
		return answer
	}
	repository, err := k.writer.Repository(ctx, checkout)
	if errors.Is(err, tickets.ErrNotGitHub) {
		answer.isNotGitHub = true
		return answer
	}
	if err != nil {
		answer.err = err
		return answer
	}
	if answer.Collaboration, answer.err = k.writer.Collaboration(ctx, repository, now); answer.err != nil {
		answer.again = now.Add(ticketRetry)
	}
	return answer
}

// project is the project a task names: its task record's once it runs, else
// its brief's, else its backlog row's.
func (k *ticketKeeper) project(id string) string {
	if meta, err := state.ReadTaskMeta(k.home.State, id); err == nil && meta.Project != "" {
		return meta.Project
	}
	if project := briefProject(filepath.Join(k.home.Data, id, "brief.md")); project != "" {
		return project
	}
	if queued, err := fleet.ReadQueuedTask(k.home, id); err == nil {
		return queued.Row.Repo
	}
	return ""
}

// title is the title a task's ticket carries: the one it was dispatched
// under, else its backlog row's, else the one its ticket already has, else
// its id. The board's own title is never used: it falls back to the first
// line of the brief, and a brief's text must not reach GitHub.
func (k *ticketKeeper) title(id string, record *tickets.Record) string {
	if meta, err := state.ReadTaskMeta(k.home.State, id); err == nil && meta.Title != "" {
		return meta.Title
	}
	if title := k.rowTitles[id]; title != "" {
		return title
	}
	if record != nil && record.Title != "" {
		return record.Title
	}
	return id
}

// claimedIssue is the issue a task's brief, or its backlog row, names as
// the one it is for, or 0.
func (k *ticketKeeper) claimedIssue(id, repository string) int {
	text := ""
	if brief, err := fsx.ReadFile(filepath.Join(k.home.Data, id, "brief.md")); err == nil {
		text = string(brief)
	} else if queued, err := fleet.ReadQueuedTask(k.home, id); err == nil {
		text = queued.Detail
	}
	number, _ := tickets.ClaimedIssue(text, repository)
	return number
}

// note records a problem the pass under way met.
func (k *ticketKeeper) note(key, problem string) {
	k.found[key] = bounded(problem, 400)
}

// noteOf shows what an open ticket's record says about it, such as a claim
// that was refused, for as long as the ticket is open.
func (k *ticketKeeper) noteOf(record *tickets.Record) {
	if record != nil && !record.IsDone && record.Note != "" {
		k.note("note:"+record.TaskID, record.Note)
	}
}

// show puts the finished pass's problems on the board.
func (k *ticketKeeper) show() {
	problems := make([]string, 0, len(k.found))
	for _, problem := range k.found {
		problems = append(problems, problem)
	}
	slices.Sort(problems)
	k.mu.Lock()
	defer k.mu.Unlock()
	k.problems = problems
}

// ticketFor is the ticket a task's place on the board calls for, under the
// title the keeper chose, and false when the board does not say enough to
// move it: a state between two others, a merge with no pull request named,
// or work with no harness known.
//
// Only the title, the state, who is on it and the pull request cross over.
// A blocked task's question and a failed or stopped task's reason stay here:
// the ticket says which fixed thing happened, never why.
func ticketFor(id, title string, task Task, record *tickets.Record) (tickets.Ticket, bool) {
	ticket := tickets.Ticket{TaskID: id, Title: title, Harness: task.Harness, PullRequest: pullRequestLink(task.PR)}
	if ticket.Harness == "" && record != nil {
		ticket.Harness = record.Harness()
	}
	hasPullRequest := ticket.PullRequest.Number > 0
	switch {
	case task.Merged || task.Phase == "merged":
		if !hasPullRequest {
			return ticket, false
		}
		ticket.State = tickets.Merged
	case task.Closed:
		ticket.State, ticket.Reason = tickets.Closed, tickets.FinishedUnmerged
	case task.Phase == "stopped":
		ticket.State, ticket.Reason = tickets.Closed, tickets.StoppedByCFO
	case task.Phase == "stopping" || task.Phase == "pausing":
		return ticket, false
	case task.Phase == "queued":
		ticket.State = tickets.Queued
	case task.Phase == "paused":
		ticket.State = tickets.Paused
	case task.Phase == "blocked":
		ticket.State, ticket.Reason = tickets.Blocked, tickets.WaitingOnDecision
	case task.Phase == "failed":
		ticket.State, ticket.Reason = tickets.Blocked, tickets.StoppedOnFailure
	case hasPullRequest:
		ticket.State = tickets.PROpen
	case task.Archived:
		ticket.State, ticket.Reason = tickets.Closed, tickets.FinishedUnmerged
	default:
		ticket.State = tickets.InProgress
	}
	if ticket.State != tickets.Queued && ticket.IsOpen() && ticket.Harness == "" {
		return ticket, false
	}
	return ticket, true
}

// pullRequestLink reads a pull request's number from its GitHub URL; anything
// else links nothing.
func pullRequestLink(url string) tickets.PullRequestLink {
	if !githubPullRequest.MatchString(url) {
		return tickets.PullRequestLink{}
	}
	number, err := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	if err != nil {
		return tickets.PullRequestLink{}
	}
	return tickets.PullRequestLink{Number: number, URL: url}
}
