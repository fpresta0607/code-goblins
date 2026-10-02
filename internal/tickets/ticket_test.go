package tickets

import (
	"slices"
	"strings"
	"testing"
)

func TestTicketShowsWhereTheTaskStandsAndWhoIsOnIt(t *testing.T) {
	pull := PullRequestLink{Number: 412, URL: "https://github.com/fpresta0607/northwind-api/pull/412"}
	cases := []struct {
		name       string
		ticket     Ticket
		wantLabels []string
		wantStatus string
		wantOpen   bool
		wantReason string
	}{
		{name: "queued", ticket: Ticket{TaskID: "nw-sync", State: Queued}, wantLabels: []string{"cfo: queued"}, wantStatus: "Queued", wantOpen: true},
		{name: "in progress", ticket: Ticket{TaskID: "nw-sync", State: InProgress, Harness: "claude"}, wantLabels: []string{"cfo: in progress", "goblin: claude"}, wantStatus: "In progress: goblin nw-sync on claude", wantOpen: true},
		{name: "pull request open", ticket: Ticket{TaskID: "nw-sync", State: PROpen, Harness: "codex", PullRequest: pull}, wantLabels: []string{"cfo: pr open", "goblin: codex"}, wantStatus: "PR open: #412", wantOpen: true},
		{name: "paused", ticket: Ticket{TaskID: "nw-sync", State: Paused, Harness: "pi"}, wantLabels: []string{"cfo: paused", "goblin: pi"}, wantStatus: "Paused", wantOpen: true},
		{name: "blocked on a decision", ticket: Ticket{TaskID: "nw-sync", State: Blocked, Harness: "claude", Reason: WaitingOnDecision}, wantLabels: []string{"cfo: blocked", "goblin: claude"}, wantStatus: "Blocked: waiting on a decision", wantOpen: true},
		{name: "blocked on a failure", ticket: Ticket{TaskID: "nw-sync", State: Blocked, Harness: "claude", Reason: StoppedOnFailure}, wantLabels: []string{"cfo: blocked", "goblin: claude"}, wantStatus: "Blocked: stopped on a failure", wantOpen: true},
		{name: "merged", ticket: Ticket{TaskID: "nw-sync", State: Merged, Harness: "claude", PullRequest: pull}, wantLabels: []string{"goblin: claude"}, wantStatus: "Merged in #412", wantReason: "completed"},
		{name: "closed by a stop", ticket: Ticket{TaskID: "nw-sync", State: Closed, Harness: "claude", Reason: StoppedByCFO}, wantLabels: []string{"goblin: claude"}, wantStatus: "Closed: stopped by the CFO", wantReason: "not_planned"},
		{name: "closed without a merge", ticket: Ticket{TaskID: "nw-sync", State: Closed, Reason: FinishedUnmerged}, wantLabels: []string{}, wantStatus: "Closed: finished without a merge", wantReason: "not_planned"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			labels, status, open, reason := tc.ticket.Labels(), tc.ticket.StatusLine(), tc.ticket.IsOpen(), tc.ticket.CloseReason()

			// Assert
			if !slices.Equal(labels, tc.wantLabels) {
				t.Fatalf("labels = %q, want %q", labels, tc.wantLabels)
			}
			if status != tc.wantStatus {
				t.Fatalf("status line = %q, want %q", status, tc.wantStatus)
			}
			if open != tc.wantOpen || reason != tc.wantReason {
				t.Fatalf("open = %v with close reason %q, want %v and %q", open, reason, tc.wantOpen, tc.wantReason)
			}
		})
	}
}

func TestTicketBodyCarriesStateWhoAndLinksOnly(t *testing.T) {
	// Arrange
	ticket := Ticket{TaskID: "nw-sync", Title: "Say why a billing sync fails", State: PROpen, Harness: "claude",
		PullRequest: PullRequestLink{Number: 412, URL: "https://github.com/fpresta0607/northwind-api/pull/412"}}

	// Act
	body := ticket.Body()

	// Assert
	for _, want := range []string{
		"**State:** PR open: #412\n",
		"**On it:** goblin nw-sync on claude\n",
		"**Pull request:** https://github.com/fpresta0607/northwind-api/pull/412\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, ticket.Title) {
		t.Fatalf("body repeats the title, which is the issue's own:\n%s", body)
	}
}

func TestTicketBodyOfAQueuedTaskNamesNobody(t *testing.T) {
	body := Ticket{TaskID: "nw-sync", State: Queued}.Body()
	if !strings.Contains(body, "**State:** Queued\n") || strings.Contains(body, "On it") || strings.Contains(body, "Pull request") {
		t.Fatalf("queued body = %q, want the state alone", body)
	}
}
