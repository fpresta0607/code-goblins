package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

const northwind = "fpresta0607/northwind-api"

// apiCall is one gh api request as GitHub would see it.
type apiCall struct {
	Method string
	Path   string
	Fields map[string][]string
}

func (c apiCall) String() string { return c.Method + " " + c.Path }

// issueGitHub answers gh api writes the way GitHub would and records each
// request and each issue it closed. A status in failures makes the request it
// names fail with it, every time or only the first failTimes times. A failure
// names a request as "METHOD path", and may add one of its fields, as in
// "PATCH path state=closed", to pick out one of the requests to that path.
type issueGitHub struct {
	issues    map[int]string
	failures  map[string]int
	failTimes map[string]int
	calls     []apiCall
	closed    []string
}

func (f *issueGitHub) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	if req.Name != "gh" || len(req.Args) == 0 || req.Args[0] != "api" {
		return execx.Result{}, fmt.Errorf("unexpected command %s %v", req.Name, req.Args)
	}
	call := apiCall{Fields: map[string][]string{}}
	for i := 1; i < len(req.Args); i++ {
		switch arg := req.Args[i]; arg {
		case "--method":
			i++
			call.Method = req.Args[i]
		case "-f":
			i++
			key, value, _ := strings.Cut(req.Args[i], "=")
			call.Fields[key] = append(call.Fields[key], value)
		default:
			if strings.HasPrefix(arg, "-") {
				return execx.Result{}, fmt.Errorf("unexpected flag %s", arg)
			}
			call.Path = arg
		}
	}
	if call.Method == "" {
		call.Method = "GET"
		if len(call.Fields) > 0 {
			call.Method = "POST"
		}
	}
	f.calls = append(f.calls, call)
	if status, ok := f.failure(call); ok {
		answer := `{"message":"failed"}`
		if status == 422 {
			answer = `{"message":"Validation Failed","errors":[{"resource":"Label","code":"already_exists","field":"name"}]}`
		}
		return execx.Result{ExitCode: 1, Stdout: []byte(answer), Stderr: []byte(fmt.Sprintf("gh: failed (HTTP %d)", status))}, nil
	}
	if call.Method == "PATCH" && slices.Contains(call.Fields["state"], "closed") {
		f.closed = append(f.closed, call.Path)
	}
	switch {
	case call.Method == "POST" && call.Path == "repos/"+northwind+"/issues":
		return execx.Result{Stdout: []byte(`{"number":501,"html_url":"https://github.com/` + northwind + `/issues/501"}`)}, nil
	case call.Method == "POST" && strings.HasSuffix(call.Path, "/comments"):
		return execx.Result{Stdout: []byte(`{"id":9001}`)}, nil
	case call.Method == "GET" && call.Path == "repos/"+northwind:
		return execx.Result{Stdout: []byte(`{"full_name":"` + northwind + `"}`)}, nil
	case call.Method == "GET":
		var number int
		if _, err := fmt.Sscanf(call.Path, "repos/"+northwind+"/issues/%d", &number); err == nil {
			if body, ok := f.issues[number]; ok {
				return execx.Result{Stdout: []byte(body)}, nil
			}
		}
		return execx.Result{ExitCode: 1, Stderr: []byte("gh: Not Found (HTTP 404)")}, nil
	}
	return execx.Result{Stdout: []byte(`{}`)}, nil
}

// failure is the status a request fails with, if a failure names it and has
// times left.
func (f *issueGitHub) failure(call apiCall) (int, bool) {
	for name, status := range f.failures {
		rest, isRequest := strings.CutPrefix(name, call.String())
		field, hasField := strings.CutPrefix(rest, " ")
		if !isRequest || (rest != "" && !hasField) {
			continue
		}
		if key, value, _ := strings.Cut(field, "="); hasField && !slices.Contains(call.Fields[key], value) {
			continue
		}
		if times, isLimited := f.failTimes[name]; isLimited {
			if times == 0 {
				continue
			}
			f.failTimes[name] = times - 1
		}
		return status, true
	}
	return 0, false
}

func (f *issueGitHub) requests() []string {
	var out []string
	for _, call := range f.calls {
		out = append(out, call.String())
	}
	return out
}

func (f *issueGitHub) call(t *testing.T, request string) apiCall {
	t.Helper()
	for _, call := range f.calls {
		if call.String() == request {
			return call
		}
	}
	t.Fatalf("no %s among %v", request, f.requests())
	return apiCall{}
}

var mergedPull = PullRequestLink{Number: 412, URL: "https://github.com/" + northwind + "/pull/412"}

func openedRecord(state State, labels ...string) *Record {
	ticket := Ticket{TaskID: "nw-sync", State: state, Harness: "claude", PullRequest: mergedPull, Reason: WaitingOnDecision}
	return &Record{TaskID: "nw-sync", Repository: northwind, Number: 501, URL: "https://github.com/" + northwind + "/issues/501", State: state, Status: ticket.StatusLine(), Labels: labels}
}

func TestApplyOpensAnIssueForANewTask(t *testing.T) {
	// Arrange
	gh := &issueGitHub{}
	ticket := Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: Queued}

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, ticket, 0)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := gh.requests(); !slices.Equal(got, []string{"POST repos/" + northwind + "/issues"}) {
		t.Fatalf("requests = %v, want one issue opened", got)
	}
	opened := gh.call(t, "POST repos/"+northwind+"/issues")
	if opened.Fields["title"][0] != ticket.Title || opened.Fields["body"][0] != ticket.Body() || !slices.Equal(opened.Fields["labels[]"], []string{"cfo: queued"}) {
		t.Fatalf("opened with %v", opened.Fields)
	}
	if record == nil || record.Number != 501 || record.URL != "https://github.com/"+northwind+"/issues/501" || record.IsClaimed || record.State != Queued || record.Status != "Queued" || !slices.Equal(record.Labels, []string{"cfo: queued"}) {
		t.Fatalf("record = %+v", record)
	}
}

func TestApplyOpensNothingForATaskThatFinishedBeforeItsTicket(t *testing.T) {
	gh := &issueGitHub{}
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, Ticket{TaskID: "nw-sync", State: Merged, PullRequest: mergedPull}, 0)
	if err != nil || record != nil || len(gh.calls) != 0 {
		t.Fatalf("record = %+v, err = %v, requests = %v, want nothing opened", record, err, gh.requests())
	}
}

func TestApplyMovesAnOpenedIssueThroughEachState(t *testing.T) {
	issue := "repos/" + northwind + "/issues/501"
	cases := []struct {
		name         string
		from         *Record
		to           Ticket
		wantRequests []string
		wantRemoved  string
		wantAdded    []string
		wantClosed   string
	}{
		{name: "queued to in progress", from: openedRecord(Queued, "cfo: queued"), to: Ticket{State: InProgress, Harness: "claude"},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20queued", "POST " + issue + "/labels", "PATCH " + issue}, wantRemoved: "cfo: queued", wantAdded: []string{"cfo: in progress", "goblin: claude"}},
		{name: "in progress to pull request open", from: openedRecord(InProgress, "cfo: in progress", "goblin: claude"), to: Ticket{State: PROpen, Harness: "claude", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "POST " + issue + "/labels", "PATCH " + issue}, wantAdded: []string{"cfo: pr open"}},
		{name: "pull request open to paused", from: openedRecord(PROpen, "cfo: pr open", "goblin: claude"), to: Ticket{State: Paused, Harness: "claude", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20pr%20open", "POST " + issue + "/labels", "PATCH " + issue}, wantAdded: []string{"cfo: paused"}},
		{name: "in progress to blocked", from: openedRecord(InProgress, "cfo: in progress", "goblin: claude"), to: Ticket{State: Blocked, Harness: "claude", Reason: WaitingOnDecision},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "POST " + issue + "/labels", "PATCH " + issue}, wantAdded: []string{"cfo: blocked"}},
		{name: "pull request open to merged", from: openedRecord(PROpen, "cfo: pr open", "goblin: claude"), to: Ticket{State: Merged, Harness: "claude", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20pr%20open", "PATCH " + issue, "PATCH " + issue, "POST " + issue + "/comments"}, wantClosed: "completed"},
		{name: "in progress to stopped", from: openedRecord(InProgress, "cfo: in progress", "goblin: claude"), to: Ticket{State: Closed, Harness: "claude", Reason: StoppedByCFO},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "PATCH " + issue, "PATCH " + issue, "POST " + issue + "/comments"}, wantClosed: "not_planned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{}
			tc.to.TaskID = "nw-sync"

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, tc.from, tc.to, 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := gh.requests(); !slices.Equal(got, tc.wantRequests) {
				t.Fatalf("requests = %v, want %v", got, tc.wantRequests)
			}
			if tc.wantAdded != nil && !slices.Equal(gh.call(t, "POST "+issue+"/labels").Fields["labels[]"], tc.wantAdded) {
				t.Fatalf("labels added = %v, want %v", gh.call(t, "POST "+issue+"/labels").Fields["labels[]"], tc.wantAdded)
			}
			edits := slices.DeleteFunc(slices.Clone(gh.calls), func(c apiCall) bool { return c.String() != "PATCH "+issue })
			if body := edits[0].Fields["body"]; len(body) != 1 || body[0] != tc.to.Body() {
				t.Fatalf("body written = %q, want %q", body, tc.to.Body())
			}
			if tc.wantClosed != "" {
				if comment := gh.call(t, "POST "+issue+"/comments").Fields["body"]; len(comment) != 1 || comment[0] != tc.to.StatusLine() {
					t.Fatalf("closing comment = %q, want %q", comment, tc.to.StatusLine())
				}
				if closing := edits[1].Fields; closing["state"][0] != "closed" || closing["state_reason"][0] != tc.wantClosed {
					t.Fatalf("closed with %v, want state_reason %s", closing, tc.wantClosed)
				}
			}
			if record.State != tc.to.State || record.Status != tc.to.StatusLine() || !slices.Equal(record.Labels, tc.to.Labels()) || record.IsDone != (tc.wantClosed != "") {
				t.Fatalf("record = %+v, want it to follow %+v", record, tc.to)
			}
		})
	}
}

func TestApplyWritesNothingWhileTheTicketIsUnchanged(t *testing.T) {
	// Arrange
	gh := &issueGitHub{}
	from := openedRecord(InProgress, "cfo: in progress", "goblin: claude")

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude"}, 0)

	// Assert
	if err != nil || len(gh.calls) != 0 || record != from {
		t.Fatalf("record = %+v, err = %v, requests = %v, want the same record and no request", record, err, gh.requests())
	}
}

func TestApplyRecordsAPullRequestThatAppearsWhileTheStateStays(t *testing.T) {
	claimed := &Record{TaskID: "nw-sync", Repository: northwind, Number: 415, IsClaimed: true, CommentID: 9001, State: Paused, Status: "Paused", Labels: []string{"cfo: paused", "goblin: claude"}}
	cases := []struct {
		name      string
		from      *Record
		wantWrite string
		wantBody  func(Ticket) string
	}{
		{name: "an opened issue's body gets its link", from: openedRecord(Paused, "cfo: paused", "goblin: claude"), wantWrite: "PATCH repos/" + northwind + "/issues/501", wantBody: Ticket.Body},
		{name: "a claimed issue's status comment is written again", from: claimed, wantWrite: "PATCH repos/" + northwind + "/issues/comments/9001", wantBody: Ticket.StatusLine},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{}
			ticket := Ticket{TaskID: "nw-sync", State: Paused, Harness: "claude", PullRequest: mergedPull}

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, tc.from, ticket, 0)
			if err != nil {
				t.Fatal(err)
			}
			firstPass := gh.requests()
			again, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, record, ticket, 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(firstPass, []string{tc.wantWrite}) {
				t.Fatalf("requests = %v, want only %s", firstPass, tc.wantWrite)
			}
			if body := gh.call(t, tc.wantWrite).Fields["body"]; len(body) != 1 || body[0] != tc.wantBody(ticket) {
				t.Fatalf("written = %q, want %q", body, tc.wantBody(ticket))
			}
			if record.PullRequest != mergedPull.URL || record.Status != "Paused" || record.IsDone {
				t.Fatalf("record = %+v, want the pull request kept on a ticket still paused", record)
			}
			if len(gh.calls) != 1 || again != record {
				t.Fatalf("requests = %v, want the second pass to write nothing", gh.requests())
			}
		})
	}
}

func TestApplyWritesNothingOnceTheIssueIsDone(t *testing.T) {
	gh := &issueGitHub{}
	from := openedRecord(Merged, "goblin: claude")
	from.IsDone = true
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, Ticket{TaskID: "nw-sync", State: Closed, Reason: FinishedUnmerged}, 0)
	if err != nil || len(gh.calls) != 0 || record != from {
		t.Fatalf("record = %+v, err = %v, requests = %v, want a done issue left alone", record, err, gh.requests())
	}
}

func TestApplyClaimsTheIssueTheTaskNames(t *testing.T) {
	// Arrange
	gh := &issueGitHub{issues: map[int]string{415: `{"number":415,"html_url":"https://github.com/` + northwind + `/issues/415","state":"open"}`}}
	issue := "repos/" + northwind + "/issues/415"

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, Ticket{TaskID: "nw-sync", Title: "Ignored for a claimed issue", State: InProgress, Harness: "pi"}, 415)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := gh.requests(); !slices.Equal(got, []string{"GET " + issue, "POST " + issue + "/labels", "POST " + issue + "/comments"}) {
		t.Fatalf("requests = %v, want the issue read, labelled and given a status comment, never a new issue or an edited body", got)
	}
	if comment := gh.call(t, "POST "+issue+"/comments").Fields["body"]; len(comment) != 1 || comment[0] != "In progress: goblin nw-sync on pi" {
		t.Fatalf("status comment = %q", comment)
	}
	if record == nil || !record.IsClaimed || record.Number != 415 || record.CommentID != 9001 || record.URL != "https://github.com/"+northwind+"/issues/415" {
		t.Fatalf("record = %+v", record)
	}
}

func TestApplyFinishesAClaimWhoseStatusCommentFailed(t *testing.T) {
	issue := "repos/" + northwind + "/issues/415"
	cases := []struct {
		name         string
		next         Ticket
		wantRequests []string
		wantStatus   string
		wantLabels   []string
		wantClosed   []string
	}{
		{name: "still in progress", next: Ticket{TaskID: "nw-sync", State: InProgress, Harness: "pi"},
			wantRequests: []string{"POST " + issue + "/comments"}, wantStatus: "In progress: goblin nw-sync on pi", wantLabels: []string{"cfo: in progress", "goblin: pi"}},
		{name: "stopped meanwhile", next: Ticket{TaskID: "nw-sync", State: Closed, Harness: "pi", Reason: StoppedByCFO},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "DELETE " + issue + "/labels/goblin:%20pi", "POST " + issue + "/comments"}, wantStatus: "Released: stopped by the CFO", wantLabels: []string{}},
		{name: "merged meanwhile", next: Ticket{TaskID: "nw-sync", State: Merged, Harness: "pi", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "PATCH " + issue, "POST " + issue + "/comments"}, wantStatus: "Merged in #412", wantLabels: []string{"goblin: pi"}, wantClosed: []string{issue}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			comment := "POST " + issue + "/comments"
			gh := &issueGitHub{
				issues:    map[int]string{415: `{"number":415,"html_url":"https://github.com/` + northwind + `/issues/415","state":"open"}`},
				failures:  map[string]int{comment: 502},
				failTimes: map[string]int{comment: 1},
			}
			github := GitHub{Commands: gh}

			// Act
			partial, claimErr := github.Apply(context.Background(), northwind, nil, Ticket{TaskID: "nw-sync", State: InProgress, Harness: "pi"}, 415)
			firstPass := len(gh.calls)
			record, err := github.Apply(context.Background(), northwind, partial, tc.next, 0)

			// Assert
			if claimErr == nil || partial == nil || !partial.IsClaimed || partial.Number != 415 || partial.CommentID != 0 || partial.Status != "" || !slices.Equal(partial.Labels, []string{"cfo: in progress", "goblin: pi"}) {
				t.Fatalf("record = %+v, err = %v, want the labelled claim kept with the failure", partial, claimErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := gh.requests()[firstPass:]; !slices.Equal(got, tc.wantRequests) {
				t.Fatalf("requests = %v, want %v", got, tc.wantRequests)
			}
			if body := gh.calls[len(gh.calls)-1].Fields["body"]; len(body) != 1 || body[0] != tc.wantStatus {
				t.Fatalf("status comment = %q, want %q", body, tc.wantStatus)
			}
			if record.CommentID != 9001 || record.Status != tc.wantStatus || !slices.Equal(record.Labels, tc.wantLabels) || !slices.Equal(gh.closed, tc.wantClosed) {
				t.Fatalf("record = %+v, closed = %v", record, gh.closed)
			}
		})
	}
}

func TestApplyOpensANewIssueWhenTheNamedOneCannotBeClaimed(t *testing.T) {
	named := "repos/" + northwind + "/issues/415"
	cases := []struct {
		name         string
		issues       map[int]string
		wantWhy      string
		wantRequests []string
	}{
		{name: "a pull request", issues: map[int]string{415: `{"number":415,"state":"open","pull_request":{"url":"x"}}`}, wantWhy: "a pull request",
			wantRequests: []string{"GET " + named, "POST repos/" + northwind + "/issues"}},
		{name: "a closed issue", issues: map[int]string{415: `{"number":415,"state":"closed"}`}, wantWhy: "closed",
			wantRequests: []string{"GET " + named, "POST repos/" + northwind + "/issues"}},
		{name: "an issue that does not exist", wantWhy: "does not exist",
			wantRequests: []string{"GET " + named, "GET repos/" + northwind, "POST repos/" + northwind + "/issues"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{issues: tc.issues}

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: InProgress, Harness: "pi"}, 415)

			// Assert
			var refused *ClaimRefused
			if !errors.As(err, &refused) || refused.Number != 415 || !strings.Contains(err.Error(), tc.wantWhy) {
				t.Fatalf("err = %v, want a claim refusal for #415 naming %q", err, tc.wantWhy)
			}
			if got := gh.requests(); !slices.Equal(got, tc.wantRequests) {
				t.Fatalf("requests = %v, want %v: the named issue is never written to", got, tc.wantRequests)
			}
			if record == nil || record.Number != 501 || record.IsClaimed || record.State != InProgress {
				t.Fatalf("record = %+v, want the issue opened instead, so no later pass tries the claim again", record)
			}
		})
	}
}

func TestApplyClaimsAndOpensNothingWhileTheRepositoryIsNotVisible(t *testing.T) {
	// Arrange: a 404 for the issue and for the repository itself is what gh
	// gets when it has lost access, not what a deleted issue looks like.
	gh := &issueGitHub{failures: map[string]int{"GET repos/" + northwind: 404}}

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, Ticket{TaskID: "nw-sync", State: InProgress, Harness: "pi"}, 415)

	// Assert
	var refused *ClaimRefused
	if err == nil || errors.As(err, &refused) || !strings.Contains(err.Error(), "not visible") || record != nil {
		t.Fatalf("record = %+v, err = %v, want an error naming the repository as not visible and nothing kept", record, err)
	}
	if got := gh.requests(); !slices.Equal(got, []string{"GET repos/" + northwind + "/issues/415", "GET repos/" + northwind}) {
		t.Fatalf("requests = %v, want no issue opened", got)
	}
}

func TestApplyMovesAClaimedIssueByItsStatusComment(t *testing.T) {
	issue := "repos/" + northwind + "/issues/415"
	claimed := func(state State, labels ...string) *Record {
		return &Record{TaskID: "nw-sync", Repository: northwind, Number: 415, IsClaimed: true, CommentID: 9001, State: state, Labels: labels}
	}
	cases := []struct {
		name          string
		from          *Record
		to            Ticket
		wantRequests  []string
		wantStatus    string
		wantClosed    bool
		wantLabelsNow []string
	}{
		{name: "pull request opened", from: claimed(InProgress, "cfo: in progress", "goblin: pi"), to: Ticket{State: PROpen, Harness: "pi", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "POST " + issue + "/labels", "PATCH repos/" + northwind + "/issues/comments/9001"}, wantStatus: "PR open: #412", wantLabelsNow: []string{"cfo: pr open", "goblin: pi"}},
		{name: "merged closes it", from: claimed(PROpen, "cfo: pr open", "goblin: pi"), to: Ticket{State: Merged, Harness: "pi", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20pr%20open", "PATCH repos/" + northwind + "/issues/comments/9001", "PATCH " + issue}, wantStatus: "Merged in #412", wantClosed: true, wantLabelsNow: []string{"goblin: pi"}},
		{name: "stopped releases it open", from: claimed(InProgress, "cfo: in progress", "goblin: pi"), to: Ticket{State: Closed, Harness: "pi", Reason: StoppedByCFO},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20in%20progress", "DELETE " + issue + "/labels/goblin:%20pi", "PATCH repos/" + northwind + "/issues/comments/9001"}, wantStatus: "Released: stopped by the CFO", wantLabelsNow: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{}
			tc.to.TaskID = "nw-sync"

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, tc.from, tc.to, 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := gh.requests(); !slices.Equal(got, tc.wantRequests) {
				t.Fatalf("requests = %v, want %v", got, tc.wantRequests)
			}
			if status := gh.call(t, "PATCH repos/"+northwind+"/issues/comments/9001").Fields["body"]; len(status) != 1 || status[0] != tc.wantStatus {
				t.Fatalf("status comment = %q, want %q", status, tc.wantStatus)
			}
			if tc.wantClosed && gh.call(t, "PATCH "+issue).Fields["state_reason"][0] != "completed" {
				t.Fatalf("closed with %v", gh.call(t, "PATCH "+issue).Fields)
			}
			if !slices.Equal(record.Labels, tc.wantLabelsNow) || record.Status != tc.wantStatus || record.IsDone != (tc.to.State == Merged || tc.to.State == Closed) {
				t.Fatalf("record = %+v", record)
			}
		})
	}
}

func TestApplyKeepsTheRecordWhenAWriteFails(t *testing.T) {
	// Arrange
	issue := "repos/" + northwind + "/issues/501"
	gh := &issueGitHub{failures: map[string]int{"PATCH " + issue: 502}}
	from := openedRecord(InProgress, "cfo: in progress", "goblin: claude")

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, Ticket{TaskID: "nw-sync", State: PROpen, Harness: "claude", PullRequest: mergedPull}, 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err = %v, want GitHub's failure", err)
	}
	if record != from {
		t.Fatalf("record = %+v, want the last written one kept so the next pass redoes the move", record)
	}
}

func TestApplyRetriesAFailedCloseWithOneClosingComment(t *testing.T) {
	issue := "repos/" + northwind + "/issues/501"
	cases := []struct {
		name string
		to   Ticket
	}{
		{name: "merged", to: Ticket{TaskID: "nw-sync", State: Merged, Harness: "claude", PullRequest: mergedPull}},
		{name: "stopped", to: Ticket{TaskID: "nw-sync", State: Closed, Harness: "claude", Reason: StoppedByCFO}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			closeIssue := "PATCH " + issue + " state=closed"
			gh := &issueGitHub{failures: map[string]int{closeIssue: 502}, failTimes: map[string]int{closeIssue: 1}}
			from := openedRecord(PROpen, "cfo: pr open", "goblin: claude")
			comments := func() int {
				return len(slices.DeleteFunc(slices.Clone(gh.calls), func(c apiCall) bool { return c.String() != "POST "+issue+"/comments" }))
			}

			// Act
			failed, failErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, tc.to, 0)
			commentsAfterFailure := comments()
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, failed, tc.to, 0)

			// Assert
			if failErr == nil || failed != from {
				t.Fatalf("record = %+v, err = %v, want the last written record and the failed close", failed, failErr)
			}
			if commentsAfterFailure != 0 {
				t.Fatalf("%d closing comments posted before the issue closed, want none", commentsAfterFailure)
			}
			if err != nil || !record.IsDone {
				t.Fatalf("record = %+v, err = %v, want the retry to finish the move", record, err)
			}
			if got := comments(); got != 1 {
				t.Fatalf("%d closing comments posted across both passes, want exactly one; requests = %v", got, gh.requests())
			}
			if !slices.Equal(gh.closed, []string{issue}) {
				t.Fatalf("issues closed = %v, want %s", gh.closed, issue)
			}
		})
	}
}

func TestApplyPostsANewStatusCommentWhenSomeoneDeletedIt(t *testing.T) {
	issue := "repos/" + northwind + "/issues/415"
	deleted := "PATCH repos/" + northwind + "/issues/comments/8800"
	cases := []struct {
		name         string
		to           Ticket
		wantRequests []string
		wantStatus   string
		wantClosed   []string
	}{
		{name: "merged closes it", to: Ticket{TaskID: "nw-sync", State: Merged, Harness: "pi", PullRequest: mergedPull},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20pr%20open", deleted, "PATCH " + issue, "POST " + issue + "/comments"}, wantStatus: "Merged in #412", wantClosed: []string{issue}},
		{name: "stopped releases it", to: Ticket{TaskID: "nw-sync", State: Closed, Harness: "pi", Reason: StoppedByCFO},
			wantRequests: []string{"DELETE " + issue + "/labels/cfo:%20pr%20open", "DELETE " + issue + "/labels/goblin:%20pi", deleted, "POST " + issue + "/comments"}, wantStatus: "Released: stopped by the CFO"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{failures: map[string]int{deleted: 404}}
			from := &Record{TaskID: "nw-sync", Repository: northwind, Number: 415, IsClaimed: true, CommentID: 8800, State: PROpen, Status: "PR open: #412", Labels: []string{"cfo: pr open", "goblin: pi"}}

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, tc.to, 0)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := gh.requests(); !slices.Equal(got, tc.wantRequests) {
				t.Fatalf("requests = %v, want %v", got, tc.wantRequests)
			}
			if body := gh.call(t, "POST "+issue+"/comments").Fields["body"]; len(body) != 1 || body[0] != tc.wantStatus {
				t.Fatalf("status comment = %q, want %q", body, tc.wantStatus)
			}
			if record.CommentID != 9001 || record.Status != tc.wantStatus || !record.IsDone || !slices.Equal(gh.closed, tc.wantClosed) {
				t.Fatalf("record = %+v, closed = %v", record, gh.closed)
			}
		})
	}
}

func TestApplyEndsATicketWhoseIssueIsGone(t *testing.T) {
	for _, status := range []int{404, 410} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			// Arrange
			issue := "repos/" + northwind + "/issues/501"
			gh := &issueGitHub{failures: map[string]int{"PATCH " + issue: status}}
			from := openedRecord(InProgress, "cfo: in progress", "goblin: claude")

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, Ticket{TaskID: "nw-sync", State: PROpen, Harness: "claude", PullRequest: mergedPull}, 0)
			firstPass := len(gh.calls)
			again, againErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, record, Ticket{TaskID: "nw-sync", State: Merged, Harness: "claude", PullRequest: mergedPull}, 0)

			// Assert
			if err != nil || record == nil || !record.IsDone {
				t.Fatalf("record = %+v, err = %v, want the ticket done without an error", record, err)
			}
			if askedRepository := slices.Contains(gh.requests()[:firstPass], "GET repos/"+northwind); askedRepository != (status == 404) {
				t.Fatalf("requests = %v, want the repository read only for a 404, which lost access also answers", gh.requests()[:firstPass])
			}
			if againErr != nil || again != record || len(gh.calls) != firstPass {
				t.Fatalf("record = %+v, err = %v, requests = %v, want a gone issue left alone", again, againErr, gh.requests()[firstPass:])
			}
		})
	}
}

func TestApplyKeepsATicketLiveWhileTheRepositoryIsNotVisible(t *testing.T) {
	// Arrange
	issue := "repos/" + northwind + "/issues/501"
	gh := &issueGitHub{failures: map[string]int{"PATCH " + issue: 404, "GET repos/" + northwind: 404}}
	from := openedRecord(InProgress, "cfo: in progress", "goblin: claude")

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, Ticket{TaskID: "nw-sync", State: PROpen, Harness: "claude", PullRequest: mergedPull}, 0)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "not visible") {
		t.Fatalf("err = %v, want the repository named as not visible", err)
	}
	if record != from || record.IsDone {
		t.Fatalf("record = %+v, want the ticket kept live so it moves again once gh can see the repository", record)
	}
}

func TestApplyTakesALabelAlreadyGoneAsRemoved(t *testing.T) {
	issue := "repos/" + northwind + "/issues/501"
	gh := &issueGitHub{failures: map[string]int{"DELETE " + issue + "/labels/cfo:%20queued": 404}}
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, openedRecord(Queued, "cfo: queued"), Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude"}, 0)
	if err != nil || record.State != InProgress {
		t.Fatalf("record = %+v, err = %v, want a label someone removed by hand to count as removed", record, err)
	}
}

func TestEnsureLabelsCreatesEachLabelOnceAndAcceptsExistingOnes(t *testing.T) {
	// Arrange
	gh := &issueGitHub{failures: map[string]int{}}
	labels := "repos/" + northwind + "/labels"
	exists := &issueGitHub{failures: map[string]int{"POST " + labels: 422}}

	// Act
	err := GitHub{Commands: gh}.EnsureLabels(context.Background(), northwind)
	existsErr := GitHub{Commands: exists}.EnsureLabels(context.Background(), northwind)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, call := range gh.calls {
		if call.String() != "POST "+labels || call.Fields["color"] == nil || call.Fields["description"] == nil {
			t.Fatalf("unexpected request %v with %v", call, call.Fields)
		}
		names = append(names, call.Fields["name"][0])
	}
	want := []string{"cfo: queued", "cfo: in progress", "cfo: pr open", "cfo: paused", "cfo: blocked", "goblin: claude", "goblin: codex", "goblin: pi", "goblin: kimi"}
	if !slices.Equal(names, want) {
		t.Fatalf("labels created = %v, want %v", names, want)
	}
	if existsErr != nil {
		t.Fatalf("existing labels failed the call: %v", existsErr)
	}
}

func TestEnsureLabelsNamesAnyOtherFailure(t *testing.T) {
	gh := &issueGitHub{failures: map[string]int{"POST repos/" + northwind + "/labels": 403}}
	if err := (GitHub{Commands: gh}).EnsureLabels(context.Background(), northwind); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("err = %v, want the 403 named", err)
	}
}

func TestRecordRoundTripsThroughItsFile(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	record := Record{TaskID: "nw-sync", Repository: northwind, Number: 415, URL: "https://github.com/" + northwind + "/issues/415", IsClaimed: true, CommentID: 9001, State: PROpen, Status: "PR open: #412", Labels: []string{"cfo: pr open"}}

	// Act
	writeErr := WriteRecord(dir, record)
	got, readErr := ReadRecord(dir, "nw-sync")

	// Assert
	if writeErr != nil || readErr != nil {
		t.Fatalf("write %v, read %v", writeErr, readErr)
	}
	encoded, _ := json.Marshal(got)
	want, _ := json.Marshal(record)
	if string(encoded) != string(want) {
		t.Fatalf("read %s, want %s", encoded, want)
	}
}

func TestRecordRefusesAnInvalidTaskID(t *testing.T) {
	if err := WriteRecord(t.TempDir(), Record{TaskID: "../escape"}); err == nil {
		t.Fatal("a record with a path in its task id was written")
	}
	if _, err := ReadRecord(t.TempDir(), "../escape"); err == nil {
		t.Fatal("a record with a path in its task id was read")
	}
}
