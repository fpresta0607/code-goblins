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
}

const (
	// collaborationRefresh is how long an answer to "who works in this
	// repository" stands; a failed read is asked again after ticketRetry.
	collaborationRefresh = time.Hour
	ticketRetry          = 10 * time.Minute
	// ticketBackOff is how long a repository waits after GitHub refused a
	// write with 403 or 429.
	ticketBackOff = time.Hour
	// ticketWatch is how often the keeper looks for a task that moved.
	ticketWatch = 15 * time.Second
)

// ticketKeeper keeps each task's ticket where its task is. It reads the
// tasks the board shows, so a ticket follows the same lifecycle the Overlord
// sees, and it writes to GitHub only when a ticket's state changed.
type ticketKeeper struct {
	home   home.Home
	writer *Tickets

	mu           sync.Mutex
	repositories map[string]repositoryAnswer
	labelled     map[string]bool
	backOff      map[string]time.Time
	problems     map[string]string
}

// repositoryAnswer is what one project's repository answered, and when.
type repositoryAnswer struct {
	tickets.Collaboration
	err error
	at  time.Time
}

func newTicketKeeper(h home.Home, writer *Tickets) *ticketKeeper {
	return &ticketKeeper{home: h, writer: writer, repositories: map[string]repositoryAnswer{}, labelled: map[string]bool{}, backOff: map[string]time.Time{}, problems: map[string]string{}}
}

// Issues are the lines the board shows for tickets that wait or failed.
func (k *ticketKeeper) Issues() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	issues := make([]string, 0, len(k.problems))
	for _, problem := range k.problems {
		issues = append(issues, problem)
	}
	slices.Sort(issues)
	return issues
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

// reconcile moves every task's ticket to where the task is now.
func (k *ticketKeeper) reconcile(ctx context.Context, tasks []Task, now time.Time) {
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		id := strings.TrimPrefix(task.ID, "finished:")
		if state.ValidTaskID(id) != nil {
			continue
		}
		k.reconcileTask(ctx, id, task, now)
	}
}

func (k *ticketKeeper) reconcileTask(ctx context.Context, id string, task Task, now time.Time) {
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
	ticket, ok := ticketFor(id, task, record)
	if !ok {
		return
	}
	repository, claim := "", 0
	if record != nil {
		repository = record.Repository
	} else {
		if !ticket.IsOpen() {
			return
		}
		if repository = k.ticketedRepository(ctx, id, now); repository == "" {
			return
		}
		claim = k.claimedIssue(id, repository)
	}
	if until, isWaiting := k.backOff[repository]; isWaiting && now.Before(until) {
		return
	}
	if !k.labelled[repository] {
		if err := k.writer.EnsureLabels(ctx, repository); err != nil {
			k.refused(id, repository, err, now)
			return
		}
		k.labelled[repository] = true
	}
	next, err := k.writer.Apply(ctx, repository, record, ticket, claim)
	if next != nil && next != record {
		if writeErr := tickets.WriteRecord(k.home.State, *next); writeErr != nil {
			k.note("task:"+id, fmt.Sprintf("The ticket record of %s cannot be saved: %v", id, writeErr))
			return
		}
	}
	var refusedClaim *tickets.ClaimRefused
	switch {
	case errors.As(err, &refusedClaim):
		k.clear("task:" + id)
		k.note("claim:"+id, fmt.Sprintf("The ticket for %s: %v", id, err))
	case err != nil:
		k.refused(id, repository, err, now)
	default:
		k.clear("task:" + id)
		k.clear("repository:" + repository)
	}
}

// refused records a write GitHub did not take: the task's line on the board,
// and an hour's wait for the whole repository when GitHub asked for one.
func (k *ticketKeeper) refused(id, repository string, err error, now time.Time) {
	if tickets.ShouldBackOff(err) {
		k.backOff[repository] = now.Add(ticketBackOff)
		k.note("repository:"+repository, fmt.Sprintf("GitHub refused a ticket write in %s, so its tickets wait until %s: %v", repository, now.Add(ticketBackOff).UTC().Format("15:04Z"), err))
		return
	}
	k.note("task:"+id, fmt.Sprintf("The ticket for %s could not be written and is tried again: %v", id, err))
}

// ticketedRepository names the repository a task without a ticket gets one
// in, or "" when it gets none: its project is unknown, only the Overlord
// works in its repository, or the repository is public and not allowed.
func (k *ticketKeeper) ticketedRepository(ctx context.Context, id string, now time.Time) string {
	project := k.project(id)
	if project == "" {
		return ""
	}
	known, ok := k.repositories[project]
	refresh := collaborationRefresh
	if known.err != nil {
		refresh = ticketRetry
	}
	if !ok || now.Sub(known.at) >= refresh {
		known = k.readRepository(ctx, project, now)
		k.repositories[project] = known
	}
	if known.err != nil {
		k.note("project:"+project, fmt.Sprintf("Tickets for %s wait: %v", filepath.Base(project), known.err))
		return ""
	}
	k.clear("project:" + project)
	if !known.IsCollaborative {
		return ""
	}
	if !known.IsPrivate {
		isAllowed, err := tickets.IsPublicAllowed(k.home.State, known.Repository)
		if err != nil {
			k.note("public:"+known.Repository, fmt.Sprintf("Tickets in %s wait: %v", known.Repository, err))
			return ""
		}
		if !isAllowed {
			k.note("public:"+known.Repository, fmt.Sprintf("Tickets in %s are held: it is public, and an issue there is public. Run cfo tickets %s --allow-public-tickets to keep them there.", known.Repository, filepath.Base(project)))
			return ""
		}
	}
	k.clear("public:" + known.Repository)
	return known.Repository
}

func (k *ticketKeeper) readRepository(ctx context.Context, project string, now time.Time) repositoryAnswer {
	checkout, err := k.writer.Checkout(project)
	if err != nil {
		return repositoryAnswer{err: err, at: now}
	}
	repository, err := k.writer.Repository(ctx, checkout)
	if err != nil {
		return repositoryAnswer{err: err, at: now}
	}
	collaboration, err := k.writer.Collaboration(ctx, repository, now)
	return repositoryAnswer{Collaboration: collaboration, err: err, at: now}
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

func (k *ticketKeeper) note(key, problem string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.problems[key] = bounded(problem, 400)
}

func (k *ticketKeeper) clear(key string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.problems, key)
}

// ticketFor is the ticket a task's place on the board calls for, and false
// when the board does not say enough to move it: a state between two others,
// a merge with no pull request named, or work with no harness known.
//
// Only the title, the state, who is on it and the pull request cross over.
// A blocked task's question and a failed or stopped task's reason stay here:
// the ticket says which of four fixed things happened, never why.
func ticketFor(id string, task Task, record *tickets.Record) (tickets.Ticket, bool) {
	ticket := tickets.Ticket{TaskID: id, Title: task.Title, Harness: task.Harness, PullRequest: pullRequestLink(task.PR)}
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
	case task.Phase == "stopping":
		return ticket, false
	case task.Phase == "queued":
		ticket.State = tickets.Queued
	case task.Phase == "paused" || task.Phase == "pausing":
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
