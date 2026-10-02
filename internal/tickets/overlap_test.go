package tickets

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestTeammateOverlapsLeavesOutTheOverlordsOwnWork(t *testing.T) {
	// Arrange
	activity := overlordActivity()
	activity.PullRequests = []PullRequest{
		{Number: 412, HeadRef: "fix/order-session-defects", Author: Actor{Login: "ana-teammate"}, Files: []string{"api/routes_orders.py"}},
		{Number: 420, HeadRef: "fix/sync-says-why", Author: Actor{Login: "FPresta0607"}, Files: []string{"tasks/billing_sync.py"}},
	}
	activity.Branches = append(activity.Branches, Branch{Name: "fix/no-pr", Head: "f2", Author: Actor{Name: "Ben Teammate"}, CommittedAt: daysAgo(1), Files: []string{"tasks/billing_sync.py"}})
	activity.Issues = []Issue{
		{Number: 414, Title: "Support email quotes an old total", Body: "See tasks/billing_sync.py line 40.", Author: Actor{Login: "ana-teammate"}},
		{Number: 430, Title: "Tidy tasks/billing_sync.py", Author: Actor{Login: "fpresta0607"}},
	}
	area := BriefArea(syncBrief, syncCheckout)
	report := Build(activity, testNow, &area)

	// Act
	teammates := report.TeammateOverlaps()

	// Assert
	if len(report.Overlaps.Files) != 3 || len(report.Overlaps.Issues) != 2 {
		t.Fatalf("report overlaps = %+v, want the Overlord's own pull request and issue in the full report", report.Overlaps)
	}
	var files []string
	for _, overlap := range teammates.Files {
		files = append(files, overlap.Branch)
	}
	if !slices.Equal(files, []string{"fix/order-session-defects", "fix/no-pr"}) {
		t.Fatalf("teammate file overlaps = %v, want the teammate's pull request and the unlinked author's branch", files)
	}
	if len(teammates.Issues) != 1 || teammates.Issues[0].Number != 414 {
		t.Fatalf("teammate issue overlaps = %+v, want the teammate's issue alone", teammates.Issues)
	}
	if got := teammates.Lines(); !slices.Equal(got, []string{
		"PR #412 by ana-teammate changes api/routes_orders.py",
		"branch fix/no-pr by Ben Teammate (no GitHub account) changes tasks/billing_sync.py",
		"issue #414 by ana-teammate names tasks/billing_sync.py: Support email quotes an old total",
	}) {
		t.Fatalf("lines = %q", got)
	}
}

func TestTeammateOverlapsOfAReportWithNoAreaIsEmpty(t *testing.T) {
	teammates := Build(overlordActivity(), testNow, nil).TeammateOverlaps()
	if len(teammates.Files) != 0 || len(teammates.Issues) != 0 || len(teammates.Lines()) != 0 {
		t.Fatalf("teammate overlaps = %+v, want none without an area", teammates)
	}
}

func TestTicketShowsTheOverlapItWasStartedBeside(t *testing.T) {
	// Arrange
	ticket := Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude", Overlap: "PR #412 touches the same route; this only adds a log line"}

	// Act
	body, comment := ticket.Body(), ticket.StatusComment()

	// Assert
	want := "**Started beside other work:** PR #412 touches the same route; this only adds a log line\n"
	if !strings.Contains(body, want) {
		t.Fatalf("body lacks %q:\n%s", want, body)
	}
	if comment != "In progress: goblin nw-sync on claude\nStarted beside other work: PR #412 touches the same route; this only adds a log line" {
		t.Fatalf("status comment = %q", comment)
	}
	plain := Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude"}
	if strings.Contains(plain.Body(), "Started beside") || plain.StatusComment() != plain.StatusLine() {
		t.Fatalf("a ticket with no overlap mentions one: %q / %q", plain.Body(), plain.StatusComment())
	}
}

func TestApplyWritesAnOverlapAcceptedAfterTheTicketWasOpened(t *testing.T) {
	issue := "repos/" + northwind + "/issues/501"
	cases := []struct {
		name        string
		from        *Record
		wantRequest string
		wantBody    string
	}{
		{name: "an issue cfo opened", from: openedRecord(InProgress, "cfo: in progress", "goblin: claude"), wantRequest: "PATCH " + issue},
		{name: "a claimed issue", from: &Record{TaskID: "nw-sync", Repository: northwind, Number: 501, IsClaimed: true, CommentID: 9001, State: InProgress, Status: "In progress: goblin nw-sync on claude", Labels: []string{"cfo: in progress", "goblin: claude"}},
			wantRequest: "PATCH repos/" + northwind + "/issues/comments/9001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &issueGitHub{}
			ticket := Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude", Overlap: "accepted: PR #412 is nearly merged"}

			// Act
			record, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, tc.from, ticket, 0)
			again, againErr := GitHub{Commands: gh}.Apply(context.Background(), northwind, record, ticket, 0)

			// Assert
			if err != nil || againErr != nil {
				t.Fatal(err, againErr)
			}
			if got := gh.requests(); !slices.Equal(got, []string{tc.wantRequest}) {
				t.Fatalf("requests = %v, want the one write that carries the overlap, and none on the second pass", got)
			}
			if written := gh.call(t, tc.wantRequest).Fields["body"]; len(written) != 1 || !strings.Contains(written[0], "accepted: PR #412 is nearly merged") {
				t.Fatalf("written = %q, want the overlap in it", written)
			}
			if record.Overlap != ticket.Overlap || again != record {
				t.Fatalf("record = %+v, want the overlap kept so it is written once", record)
			}
		})
	}
}

func TestOverlapNoteRoundTripsAndIsAbsentUntilWritten(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	before, beforeErr := ReadOverlapNote(dir, "nw-sync")
	writeErr := WriteOverlapNote(dir, "nw-sync", "PR #412 touches the same route")
	after, afterErr := ReadOverlapNote(dir, "nw-sync")
	_, invalidErr := ReadOverlapNote(dir, "../escape")

	// Assert
	if beforeErr != nil || writeErr != nil || afterErr != nil {
		t.Fatal(beforeErr, writeErr, afterErr)
	}
	if before != "" || after != "PR #412 touches the same route" {
		t.Fatalf("note = %q before and %q after, want none, then the note", before, after)
	}
	if invalidErr == nil || WriteOverlapNote(dir, "../escape", "x") == nil {
		t.Fatal("a task id with a path in it was accepted")
	}
}
