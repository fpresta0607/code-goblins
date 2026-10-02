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
// Without a record it opens an issue for the task, or claims issue number
// claim when the task names one, and opens nothing for a ticket that is
// already closed. With one it writes only what changed: the labels that
// differ, the body of an issue it opened (and its title, when the task's
// changed) or the status comment of one it claimed, and the close. A pull
// request the record does not hold yet is a change too, so the record keeps
// it. A claimed issue belongs to whoever filed it, so a
// task that stops without a merge releases it open rather than closing it.
// A done issue is never written again. A status comment someone deleted is
// posted again, and an issue that is deleted (410, or 404 while the
// repository still answers) ends the ticket: Apply returns its record done,
// without an error. An issue the task named that cannot be claimed (missing,
// closed, or a pull request) is left alone and a new one is opened, returned
// with a ClaimRefused.
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
// creating a comment, so that is the last write of its move.
func (g GitHub) Apply(ctx context.Context, repository string, record *Record, ticket Ticket, claim int) (*Record, error) {
	if record == nil {
		switch {
		case !ticket.IsOpen():
			return nil, nil
		case claim > 0:
			return g.claim(ctx, repository, ticket, claim)
		default:
			return g.open(ctx, repository, ticket)
		}
	}
	if record.IsDone {
		return record, nil
	}
	next, err := g.move(ctx, repository, record, ticket)
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
		return &gone, nil
	}
	return next, err
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
	status, labels := ticket.StatusLine(), ticket.Labels()
	isReleased := record.IsClaimed && ticket.State == Closed
	if isReleased {
		status, labels = "Released: "+string(ticket.Reason), []string{}
	}
	isRetitled := !record.IsClaimed && ticket.Title != "" && ticket.Title != record.Title
	isLinked := ticket.PullRequest.URL != "" && ticket.PullRequest.URL != record.PullRequest
	if status == record.Status && slices.Equal(labels, record.Labels) && !isRetitled && !isLinked {
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
		_, err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repository, commentID), "body="+status)
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
		out, err := g.api(ctx, "POST", issue+"/comments", "body="+status)
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
	next.State, next.Status, next.Labels, next.IsDone = ticket.State, status, labels, !ticket.IsOpen()
	if isRetitled {
		next.Title = ticket.Title
	}
	if ticket.PullRequest.URL != "" {
		next.PullRequest = ticket.PullRequest.URL
	}
	return &next, nil
}

// open opens an issue for a task that has none.
func (g GitHub) open(ctx context.Context, repository string, ticket Ticket) (*Record, error) {
	fields := []string{"title=" + ticket.Title, "body=" + ticket.Body()}
	for _, label := range ticket.Labels() {
		fields = append(fields, "labels[]="+label)
	}
	out, err := g.api(ctx, "POST", "repos/"+repository+"/issues", fields...)
	if err != nil {
		return nil, err
	}
	var opened githubIssue
	if err := json.Unmarshal(out, &opened); err != nil || opened.Number == 0 {
		return nil, fmt.Errorf("read the issue GitHub opened in %s: %v", repository, err)
	}
	return &Record{TaskID: ticket.TaskID, Repository: repository, Number: opened.Number, URL: opened.URL, State: ticket.State, Status: ticket.StatusLine(), Labels: ticket.Labels(), Title: ticket.Title, PullRequest: ticket.PullRequest.URL}, nil
}

// claim takes over issue number for a task: it labels the issue and gives it
// a status comment, and leaves its title and body to whoever filed it. Once
// the issue is labelled, the claim is a record Apply finishes with that
// comment, so a failed comment still leaves the labels on record.
func (g GitHub) claim(ctx context.Context, repository string, ticket Ticket, number int) (*Record, error) {
	issue := fmt.Sprintf("repos/%s/issues/%d", repository, number)
	out, err := g.api(ctx, "GET", issue)
	if err != nil {
		isGone, goneErr := g.isGone(ctx, repository, err)
		if goneErr != nil {
			return nil, goneErr
		}
		if !isGone {
			return nil, err
		}
		return g.openInstead(ctx, repository, ticket, number, "it does not exist")
	}
	var claimed githubIssue
	if err := json.Unmarshal(out, &claimed); err != nil {
		return nil, fmt.Errorf("read issue #%d in %s: %w", number, repository, err)
	}
	if claimed.PullRequest != nil {
		return g.openInstead(ctx, repository, ticket, number, "it is a pull request")
	}
	if claimed.State != "open" {
		return g.openInstead(ctx, repository, ticket, number, "it is closed")
	}
	labels := ticket.Labels()
	if len(labels) > 0 {
		var fields []string
		for _, label := range labels {
			fields = append(fields, "labels[]="+label)
		}
		if _, err := g.api(ctx, "POST", issue+"/labels", fields...); err != nil {
			return nil, err
		}
	}
	labelled := &Record{TaskID: ticket.TaskID, Repository: repository, Number: number, URL: claimed.URL, IsClaimed: true, State: ticket.State, Labels: labels}
	return g.Apply(ctx, repository, labelled, ticket, 0)
}

// openInstead opens an issue for a task whose named issue cannot be claimed,
// and returns it with the refusal.
func (g GitHub) openInstead(ctx context.Context, repository string, ticket Ticket, number int, why string) (*Record, error) {
	opened, err := g.open(ctx, repository, ticket)
	if err != nil {
		return nil, err
	}
	return opened, &ClaimRefused{Number: number, Why: why}
}

// ClaimRefused says why the issue a task named could not be claimed. Apply
// returns it together with the record of the issue it opened instead, so the
// refusal is reported once and no later pass tries the claim again.
type ClaimRefused struct {
	Number int
	Why    string
}

func (e *ClaimRefused) Error() string {
	return fmt.Sprintf("issue #%d was not claimed because %s; a new issue was opened for the task", e.Number, e.Why)
}

type githubIssue struct {
	Number      int             `json:"number"`
	URL         string          `json:"html_url"`
	State       string          `json:"state"`
	PullRequest json.RawMessage `json:"pull_request"`
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
