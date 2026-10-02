package tickets

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestApplyRetitlesAnIssueItOpenedWhenTheTasksTitleChanges(t *testing.T) {
	// Arrange
	gh := &issueGitHub{}
	issue := "repos/" + northwind + "/issues/501"
	from := openedRecord(InProgress, "cfo: in progress", "goblin: claude")
	from.Title = "nw-sync"
	ticket := Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: InProgress, Harness: "claude"}

	// Act
	record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, from, ticket, 0)
	again, againErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, record, ticket, 0)

	// Assert
	if err != nil || againErr != nil {
		t.Fatal(err, againErr)
	}
	if got := gh.requests(); !slices.Equal(got, []string{"PATCH " + issue}) {
		t.Fatalf("requests = %v, want one edit carrying the new title and none on the second pass", got)
	}
	if title := gh.call(t, "PATCH "+issue).Fields["title"]; !slices.Equal(title, []string{"Say why a billing sync fails"}) {
		t.Fatalf("title written = %q", title)
	}
	if record.Title != ticket.Title || again != record {
		t.Fatalf("record = %+v, want the title kept so it is written once", record)
	}
}

func TestApplyNeverRetitlesAClaimedIssueOrWritesAnEmptyTitle(t *testing.T) {
	cases := []struct {
		name   string
		from   *Record
		ticket Ticket
	}{
		{name: "a claimed issue keeps its author's title", from: &Record{TaskID: "nw-sync", Repository: northwind, Number: 415, IsClaimed: true, CommentID: 9001, State: InProgress, Status: "In progress: goblin nw-sync on claude", Labels: []string{"cfo: in progress", "goblin: claude"}},
			ticket: Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: InProgress, Harness: "claude"}},
		{name: "a task with no title leaves the issue's title alone", from: openedRecord(InProgress, "cfo: in progress", "goblin: claude"),
			ticket: Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &issueGitHub{}
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, tc.from, tc.ticket, 0)
			if err != nil || len(gh.calls) != 0 || record != tc.from {
				t.Fatalf("record = %+v, err = %v, requests = %v, want nothing written", record, err, gh.requests())
			}
		})
	}
}

func TestApplyKeepsTheTitleAndThePullRequestInTheRecord(t *testing.T) {
	// Arrange
	gh := &issueGitHub{}
	ticket := Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: Queued}

	// Act
	opened, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, nil, ticket, 0)
	ticket.State, ticket.Harness, ticket.PullRequest = PROpen, "claude", mergedPull
	withPull, pullErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, opened, ticket, 0)
	ticket.State, ticket.PullRequest = Paused, PullRequestLink{}
	paused, pausedErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, withPull, ticket, 0)

	// Assert
	if err != nil || pullErr != nil || pausedErr != nil {
		t.Fatal(err, pullErr, pausedErr)
	}
	if opened.Title != ticket.Title || opened.PullRequest != "" {
		t.Fatalf("opened record = %+v, want the title and no pull request yet", opened)
	}
	if withPull.PullRequest != mergedPull.URL {
		t.Fatalf("record = %+v, want the pull request kept once the ticket names it", withPull)
	}
	if paused.PullRequest != mergedPull.URL {
		t.Fatalf("record = %+v, want the pull request remembered when a later ticket does not name it", paused)
	}
}

func TestListRecordsReadsEveryTicketRecord(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	none, noneErr := ListRecords(dir)
	for _, record := range []Record{{TaskID: "nw-sync", Number: 501}, {TaskID: "nw-refunds", Number: 502, IsDone: true}} {
		if err := WriteRecord(dir, record); err != nil {
			t.Fatal(err)
		}
	}

	// Act
	records, err := ListRecords(dir)

	// Assert
	if noneErr != nil || len(none) != 0 {
		t.Fatalf("records before any ticket = %+v, %v, want none and no error", none, noneErr)
	}
	if err != nil || len(records) != 2 || records[0].TaskID != "nw-refunds" || records[1].TaskID != "nw-sync" || !records[0].IsDone {
		t.Fatalf("records = %+v, %v, want both, in task order", records, err)
	}
}

func TestRepositoryOfSaysWhenACheckoutHasNoGitHubRepository(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		want   bool
	}{
		{name: "no origin remote", remote: "", want: true},
		{name: "an origin that is not GitHub", remote: "https://gitlab.com/someone/project.git", want: true},
		{name: "a GitHub origin", remote: "https://github.com/fpresta0607/northwind-api.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GitHub{Commands: &fakeGitHub{remote: tc.remote}}.RepositoryOf(context.Background(), "checkout")
			if got := errors.Is(err, ErrNotGitHub); got != tc.want {
				t.Fatalf("err = %v, is ErrNotGitHub = %v, want %v", err, got, tc.want)
			}
		})
	}
}
