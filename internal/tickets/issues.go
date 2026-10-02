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
// differ, the body of an issue it opened or the status comment of one it
// claimed, and the close. A claimed issue belongs to whoever filed it, so a
// task that stops without a merge releases it open rather than closing it.
// A done issue is never written again.
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
	status, labels := ticket.StatusLine(), ticket.Labels()
	isReleased := record.IsClaimed && ticket.State == Closed
	if isReleased {
		status, labels = "Released: "+string(ticket.Reason), []string{}
	}
	if status == record.Status && slices.Equal(labels, record.Labels) {
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
	if !record.IsClaimed {
		if _, err := g.api(ctx, "PATCH", issue, "body="+ticket.Body()); err != nil {
			return record, err
		}
	} else if record.CommentID != 0 {
		if _, err := g.api(ctx, "PATCH", fmt.Sprintf("repos/%s/issues/comments/%d", repository, record.CommentID), "body="+status); err != nil {
			return record, err
		}
	}
	if !ticket.IsOpen() && !isReleased {
		if _, err := g.api(ctx, "PATCH", issue, "state=closed", "state_reason="+ticket.CloseReason()); err != nil {
			return record, err
		}
	}
	next := *record
	if record.IsClaimed && record.CommentID == 0 {
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
	return &next, nil
}

// open opens an issue for a task that has none.
func (g GitHub) open(ctx context.Context, repository string, ticket Ticket) (*Record, error) {
	title := ticket.Title
	if title == "" {
		title = ticket.TaskID
	}
	fields := []string{"title=" + title, "body=" + ticket.Body()}
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
	return &Record{TaskID: ticket.TaskID, Repository: repository, Number: opened.Number, URL: opened.URL, State: ticket.State, Status: ticket.StatusLine(), Labels: ticket.Labels()}, nil
}

// claim takes over issue number for a task: it labels the issue and gives it
// a status comment, and leaves its title and body to whoever filed it. Once
// the issue is labelled, the claim is a record Apply finishes with that
// comment, so a failed comment still leaves the labels on record.
func (g GitHub) claim(ctx context.Context, repository string, ticket Ticket, number int) (*Record, error) {
	issue := fmt.Sprintf("repos/%s/issues/%d", repository, number)
	out, err := g.api(ctx, "GET", issue)
	if err != nil {
		return nil, err
	}
	var claimed githubIssue
	if err := json.Unmarshal(out, &claimed); err != nil {
		return nil, fmt.Errorf("read issue #%d in %s: %w", number, repository, err)
	}
	if claimed.PullRequest != nil {
		return nil, fmt.Errorf("#%d in %s is a pull request, not an issue the task can claim", number, repository)
	}
	if claimed.State != "open" {
		return nil, fmt.Errorf("issue #%d in %s is closed, so the task cannot claim it", number, repository)
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

// apiError is a gh api request GitHub refused, with its HTTP status and the
// answer it gave.
type apiError struct {
	request string
	status  int
	message string
	answer  string
}

func (e *apiError) Error() string { return "gh api " + e.request + ": " + e.message }

var httpStatus = regexp.MustCompile(`\(HTTP (\d{3})\)`)

func hasStatus(err error, status int) bool {
	var refused *apiError
	return errors.As(err, &refused) && refused.status == status
}

func isExistingLabel(err error) bool {
	var refused *apiError
	return errors.As(err, &refused) && refused.status == 422 && strings.Contains(refused.answer, "already_exists")
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
		refused := &apiError{request: method + " " + path, message: strings.TrimSpace(string(result.Stderr)), answer: string(result.Stdout)}
		if match := httpStatus.FindStringSubmatch(refused.message); match != nil {
			refused.status, _ = strconv.Atoi(match[1])
		}
		if refused.message == "" {
			refused.message = fmt.Sprintf("gh exited %d", result.ExitCode)
		}
		return nil, refused
	}
	return result.Stdout, nil
}
