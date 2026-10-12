package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// Apply moves a task's issue to its ticket and returns what it then holds.
//
// Without a record it opens an issue for the task, or claims the issues named
// when the task names any, and opens nothing for a ticket that is already
// closed. With one it writes only what changed: the labels that
// differ, the body of an issue it opened (and its title, when the task's
// changed), which says what the task changes and where, or the status
// comment of each issue it claimed, and the close. A pull
// request the record does not hold yet is a change too, so the record keeps
// every pull request of the task. A claimed issue belongs to whoever filed it, so a
// task that stops without a merge releases it open rather than closing it.
// A done issue is never written again but for a new title, the one cfo title
// can still give it. A status comment someone deleted is
// posted again, and an issue that is deleted (410, or 404 while the
// repository still answers) ends the ticket, or that one of the issues it
// claimed: Apply returns it done, without an error. An issue the task named
// that cannot be claimed (missing, closed, a pull request, or someone else's:
// assigned to another account or held by another fleet) is left alone, and a
// new one is opened when no named issue could be: Apply returns the refusal
// as a ClaimRefused. A number the brief only lists that GitHub says is no
// open issue is passed over without one. An issue the task opens or claims
// is assigned to ticket.Assignee, the account the fleet writes as, and a
// claimed issue it releases is unassigned again.
//
// GitHub answers 404 both for an issue that is gone and for a repository gh
// can no longer see, so a 404 ends or refuses nothing until the repository
// itself still answers; while it does not, Apply returns an error and the
// ticket waits.
//
// An issue transferred to another repository answers with a redirect that gh
// follows as a read, so writes to it change nothing and the ticket simply
// stops being reflected there; nothing wrong is written.
//
// The caller keeps any record Apply returns, error or not: it is what the
// issue then holds. When a write fails, that is the record Apply was given,
// so the next pass redoes the whole move, or a claim whose status comment
// failed, which the next pass finishes. Every write is safe to repeat but
// creating a comment, so that is the last write of its move. Each issue a
// task claimed keeps a record of its own, so a move that fails on one of
// them is redone on that one alone.
func (g GitHub) Apply(ctx context.Context, repository string, record *Record, ticket Ticket, named []NamedIssue) (*Record, error) {
	switch {
	case record == nil && !ticket.IsOpen():
		return nil, nil
	case record == nil && len(named) > 0:
		return g.claim(ctx, repository, ticket, named)
	case record == nil:
		return g.open(ctx, repository, ticket)
	case record.IsClaimed:
		return g.moveClaims(ctx, repository, record, ticket)
	default:
		return g.applyOne(ctx, repository, record, ticket)
	}
}

// moveClaims moves every issue a task claimed to its ticket, each by its own
// record. The ticket it returns is its first issue's, holding them all, and
// done once each of them is.
func (g GitHub) moveClaims(ctx context.Context, repository string, record *Record, ticket Ticket) (*Record, error) {
	issues := record.Issues
	if len(issues) == 0 {
		issues = []Record{*record}
	}
	var moved []Record
	var errs error
	isMoved := false
	for _, issue := range issues {
		next, err := g.applyOne(ctx, repository, &issue, ticket)
		moved = append(moved, *next)
		isMoved = isMoved || next != &issue
		errs = errors.Join(errs, err)
	}
	if !isMoved {
		return record, errs
	}
	result := moved[0]
	result.Issues, result.Note = moved, record.Note
	for _, issue := range moved {
		result.IsDone = result.IsDone && issue.IsDone
	}
	return &result, errs
}

// applyOne moves one issue, the one a record names, to its ticket.
func (g GitHub) applyOne(ctx context.Context, repository string, record *Record, ticket Ticket) (*Record, error) {
	var next *Record
	var err error
	if record.IsDone {
		next, err = g.retitle(ctx, repository, record, ticket.Title)
	} else {
		next, err = g.move(ctx, repository, record, ticket)
	}
	if err == nil {
		return next, nil
	}
	isGone, goneErr := g.isGone(ctx, repository, err)
	if goneErr != nil {
		return record, goneErr
	}
	if isGone {
		gone := *record
		gone.IsDone = true
		// A retitle of an issue that is gone is not asked again.
		if record.IsDone {
			gone.Title = ticket.Title
		}
		return &gone, nil
	}
	return next, err
}

// retitle gives a done issue the task opened a new title, and writes nothing
// else to it.
func (g GitHub) retitle(ctx context.Context, repository string, record *Record, title string) (*Record, error) {
	if record.IsClaimed || title == "" || title == record.Title {
		return record, nil
	}
	if _, err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/%d", repository, record.Number), "title="+title); err != nil {
		return record, err
	}
	next := *record
	next.Title = title
	return &next, nil
}

// isGone reports whether a refused request means the issue it wrote to is
// gone: GitHub answered 410, or 404 while the repository itself still
// answers. A 404 from a repository gh cannot see is an error instead.
func (g GitHub) isGone(ctx context.Context, repository string, refused error) (bool, error) {
	if hasStatus(refused, 410) {
		return true, nil
	}
	if !hasStatus(refused, 404) {
		return false, nil
	}
	if _, err := g.api(ctx, "GET", "repos/"+repository); err != nil {
		return false, fmt.Errorf("%s is not visible to gh, so its tickets wait: %w", repository, err)
	}
	return true, nil
}

// move writes what changed between an issue's record and its ticket, and
// returns the record it was given when a write fails.
func (g GitHub) move(ctx context.Context, repository string, record *Record, ticket Ticket) (*Record, error) {
	status, comment, labels := ticket.StatusLine(), ticket.StatusComment(), ticket.Labels()
	isReleased := record.IsClaimed && ticket.State == Closed
	if isReleased {
		status, labels = "Released: "+string(ticket.Reason), []string{}
		comment = status
	}
	isRetitled := !record.IsClaimed && ticket.Title != "" && ticket.Title != record.Title
	pulls := record.PullRequestsOnRecord()
	for _, url := range ticket.pullRequestURLs() {
		if !slices.Contains(pulls, url) {
			pulls = append(pulls, url)
		}
	}
	isLinked := (ticket.PullRequest.URL != "" && ticket.PullRequest.URL != record.PullRequest) || !slices.Equal(pulls, record.PullRequests)
	isDescribed := !record.IsClaimed && (ticket.What != record.What || !slices.Equal(ticket.Area, record.Area))
	if status == record.Status && slices.Equal(labels, record.Labels) && ticket.Overlap == record.Overlap && !isRetitled && !isLinked && !isDescribed {
		return record, nil
	}
	issue := fmt.Sprintf("repos/%s/issues/%d", repository, record.Number)
	for _, label := range record.Labels {
		if slices.Contains(labels, label) {
			continue
		}
		if _, err := g.api(ctx, "DELETE", issue+"/labels/"+url.PathEscape(label)); err != nil && !hasStatus(err, 404) {
			return record, err
		}
	}
	var added []string
	for _, label := range labels {
		if !slices.Contains(record.Labels, label) {
			added = append(added, "labels[]="+label)
		}
	}
	if len(added) > 0 {
		if _, err := g.api(ctx, "POST", issue+"/labels", added...); err != nil {
			return record, err
		}
	}
	if isReleased && record.Assignee != "" {
		if _, err := g.api(ctx, "DELETE", issue+"/assignees", "assignees[]="+record.Assignee); err != nil {
			return record, err
		}
	}
	commentID := record.CommentID
	if !record.IsClaimed {
		fields := []string{"body=" + ticket.Body()}
		if isRetitled {
			fields = append(fields, "title="+ticket.Title)
		}
		if _, err := g.api(ctx, "PATCH", issue, fields...); err != nil {
			return record, err
		}
	} else if commentID != 0 {
		_, err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repository, commentID), "body="+comment)
		if hasStatus(err, 404) {
			commentID = 0
		} else if err != nil {
			return record, err
		}
	}
	if !ticket.IsOpen() && !isReleased {
		if _, err := g.api(ctx, "PATCH", issue, "state=closed", "state_reason="+ticket.CloseReason()); err != nil {
			return record, err
		}
	}
	next := *record
	if record.IsClaimed && commentID == 0 {
		out, err := g.api(ctx, "POST", issue+"/comments", "body="+comment)
		if err != nil {
			return record, err
		}
		var comment struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(out, &comment); err != nil || comment.ID == 0 {
			return record, fmt.Errorf("read the status comment GitHub added to issue #%d in %s: %v", record.Number, repository, err)
		}
		next.CommentID = comment.ID
	} else if !record.IsClaimed && !ticket.IsOpen() {
		if _, err := g.api(ctx, "POST", issue+"/comments", "body="+status); err != nil {
			return record, err
		}
	}
	next.State, next.Status, next.Labels, next.IsDone, next.Overlap = ticket.State, status, labels, !ticket.IsOpen(), ticket.Overlap
	if isRetitled {
		next.Title = ticket.Title
	}
	if !record.IsClaimed {
		next.What, next.Area = ticket.What, ticket.Area
	}
	if ticket.PullRequest.URL != "" {
		next.PullRequest = ticket.PullRequest.URL
	}
	next.PullRequests = pulls
	if isReleased {
		next.Assignee = ""
	}
	return &next, nil
}

// open opens an issue for a task that has none, assigned to the fleet's
// account.
func (g GitHub) open(ctx context.Context, repository string, ticket Ticket) (*Record, error) {
	fields := []string{"title=" + ticket.Title, "body=" + ticket.Body()}
	for _, label := range ticket.Labels() {
		fields = append(fields, "labels[]="+label)
	}
	if ticket.Assignee != "" {
		fields = append(fields, "assignees[]="+ticket.Assignee)
	}
	out, err := g.api(ctx, "POST", "repos/"+repository+"/issues", fields...)
	if err != nil {
		return nil, err
	}
	var opened githubIssue
	if err := json.Unmarshal(out, &opened); err != nil || opened.Number == 0 {
		return nil, fmt.Errorf("read the issue GitHub opened in %s: %v", repository, err)
	}
	return &Record{TaskID: ticket.TaskID, Repository: repository, Number: opened.Number, URL: opened.URL, State: ticket.State, Status: ticket.StatusLine(), Labels: ticket.Labels(), Title: ticket.Title, PullRequest: ticket.PullRequest.URL, PullRequests: ticket.pullRequestURLs(), Overlap: ticket.Overlap, What: ticket.What, Area: ticket.Area, Assignee: ticket.Assignee}, nil
}

// claim takes over the issues a task names: it reads each first, then
// assigns the fleet's account to every one it can take, labels it and gives
// it a status comment, and leaves their titles and bodies to whoever filed
// them. It never takes an issue someone else has: one assigned to another
// account, or one another fleet holds, is refused even when the brief only
// lists it. Once the issues are labelled, the
// claim is a record Apply finishes with those comments, so a failed comment
// still leaves the labels on record. When it can take none, it opens an
// issue for the task instead.
func (g GitHub) claim(ctx context.Context, repository string, ticket Ticket, named []NamedIssue) (*Record, error) {
	refused := &ClaimRefused{}
	var taken []githubIssue
	for _, issue := range named {
		read, why, isHeld, err := g.readClaim(ctx, repository, issue.Number, ticket.Assignee)
		switch {
		case err != nil:
			return nil, err
		case why == "":
			taken = append(taken, read)
		case !issue.IsListed || isHeld:
			refused.Refused = append(refused.Refused, RefusedIssue{Number: issue.Number, Why: why})
		}
	}
	if len(taken) == 0 {
		opened, err := g.open(ctx, repository, ticket)
		if err != nil || len(refused.Refused) == 0 {
			return opened, err
		}
		refused.IsOpened = true
		return opened, refused
	}
	labels := ticket.Labels()
	var issues []Record
	for _, issue := range taken {
		if ticket.Assignee != "" && !slices.ContainsFunc(issue.Assignees, func(assignee githubAccount) bool { return strings.EqualFold(assignee.Login, ticket.Assignee) }) {
			if _, err := g.api(ctx, "POST", fmt.Sprintf("repos/%s/issues/%d/assignees", repository, issue.Number), "assignees[]="+ticket.Assignee); err != nil {
				return nil, err
			}
		}
		if len(labels) > 0 {
			var fields []string
			for _, label := range labels {
				fields = append(fields, "labels[]="+label)
			}
			if _, err := g.api(ctx, "POST", fmt.Sprintf("repos/%s/issues/%d/labels", repository, issue.Number), fields...); err != nil {
				return nil, err
			}
		}
		issues = append(issues, Record{TaskID: ticket.TaskID, Repository: repository, Number: issue.Number, URL: issue.URL, IsClaimed: true, State: ticket.State, Labels: labels, Assignee: ticket.Assignee})
	}
	labelled := issues[0]
	labelled.Issues = issues
	next, err := g.Apply(ctx, repository, &labelled, ticket, nil)
	if len(refused.Refused) == 0 {
		return next, err
	}
	refused.Err = err
	return next, refused
}

// readClaim reads issue number before a task claims it for account, and says
// why it cannot be claimed: it does not exist, is a pull request or is
// closed, or, with isHeld, someone else has it. A read GitHub did not answer
// is an error, and so is a repository gh cannot see.
func (g GitHub) readClaim(ctx context.Context, repository string, number int, account string) (issue githubIssue, why string, isHeld bool, err error) {
	out, err := g.api(ctx, "GET", fmt.Sprintf("repos/%s/issues/%d", repository, number))
	if err != nil {
		isGone, goneErr := g.isGone(ctx, repository, err)
		if goneErr != nil {
			return githubIssue{}, "", false, goneErr
		}
		if !isGone {
			return githubIssue{}, "", false, err
		}
		return githubIssue{}, "it does not exist", false, nil
	}
	if err := json.Unmarshal(out, &issue); err != nil {
		return githubIssue{}, "", false, fmt.Errorf("read issue #%d in %s: %w", number, repository, err)
	}
	switch {
	case issue.PullRequest != nil:
		return issue, "it is a pull request", false, nil
	case issue.State != "open":
		return issue, "it is closed", false, nil
	}
	var assignees, labels []string
	for _, assignee := range issue.Assignees {
		assignees = append(assignees, assignee.Login)
	}
	for _, label := range issue.Labels {
		labels = append(labels, label.Name)
	}
	if held := HeldElsewhere(assignees, labels, account); held != "" {
		return issue, held, true, nil
	}
	return issue, "", false, nil
}

// ClaimRefused says why issues a task named could not be claimed, and
// whether a new issue was opened for the task because none could. Apply
// returns it together with the record of the issues it claimed or opened, so
// the refusal is reported once and no later pass tries the claim again. Err
// is a write of the claim that failed besides, which the next pass redoes.
type ClaimRefused struct {
	Refused  []RefusedIssue
	IsOpened bool
	Err      error
}

// RefusedIssue is one issue a task named that it did not claim, and why.
type RefusedIssue struct {
	Number int
	Why    string
}

func (e *ClaimRefused) Error() string {
	var refusals []string
	for _, refused := range e.Refused {
		refusals = append(refusals, fmt.Sprintf("issue #%d was not claimed because %s", refused.Number, refused.Why))
	}
	if e.IsOpened {
		refusals = append(refusals, "a new issue was opened for the task")
	}
	return strings.Join(refusals, "; ")
}

func (e *ClaimRefused) Unwrap() error { return e.Err }

type githubIssue struct {
	Number      int             `json:"number"`
	URL         string          `json:"html_url"`
	State       string          `json:"state"`
	PullRequest json.RawMessage `json:"pull_request"`
	Assignees   []githubAccount `json:"assignees"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

type githubAccount struct {
	Login string `json:"login"`
}

// EnsureLabels creates the labels tickets use in a repository: one per open
// state and one per harness. A label that already exists is left as it is.
func (g GitHub) EnsureLabels(ctx context.Context, repository string) error {
	type label struct{ name, color, description string }
	var labels []label
	for _, state := range openStates {
		labels = append(labels, label{stateLabel(state), "5319e7", "A Code Goblins task's ticket: " + string(state)})
	}
	for _, kind := range harnesses {
		labels = append(labels, label{harnessLabel(string(kind)), "0e8a16", "A Code Goblins goblin on " + string(kind) + " works this ticket"})
	}
	for _, l := range labels {
		_, err := g.api(ctx, "POST", "repos/"+repository+"/labels", "name="+l.name, "color="+l.color, "description="+l.description)
		if err != nil && !isExistingLabel(err) {
			return err
		}
	}
	return nil
}

// APIError is a gh api request GitHub refused, with its HTTP status and the
// answer it gave.
type APIError struct {
	Status  int
	request string
	message string
	answer  string
}

func (e *APIError) Error() string {
	return "gh api " + e.request + ": " + e.message
}

var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

func hasStatus(err error, status int) bool {
	var refused *APIError
	return errors.As(err, &refused) && refused.Status == status
}

// ShouldBackOff reports whether GitHub refused a request in a way that asks
// the caller to stop for a while: 403, which is how it answers both a rate
// limit and a missing permission, or 429.
func ShouldBackOff(err error) bool {
	return hasStatus(err, 403) || hasStatus(err, 429)
}

func isExistingLabel(err error) bool {
	var refused *APIError
	return errors.As(err, &refused) && refused.Status == 422 && strings.Contains(refused.answer, "already_exists")
}

// api sends one REST request through gh, each field as a string, and returns
// GitHub's answer.
func (g GitHub) api(ctx context.Context, method, path string, fields ...string) ([]byte, error) {
	args := []string{"api", "--method", method, path}
	for _, field := range fields {
		args = append(args, "-f", field)
	}
	result, err := g.Commands.Run(ctx, execx.Request{Name: "gh", Args: args})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		refused := &APIError{request: method + " " + path, message: strings.TrimSpace(string(result.Stderr)), answer: string(result.Stdout)}
		if match := httpStatus.FindStringSubmatch(refused.message); match != nil {
			refused.Status, _ = strconv.Atoi(match[1])
		}
		if refused.message == "" {
			refused.message = fmt.Sprintf("gh exited %d", result.ExitCode)
		}
		return nil, refused
	}
	return result.Stdout, nil
}
