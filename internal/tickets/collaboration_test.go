package tickets

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func collaborationPage(isPrivate bool, commitAuthor, branchAuthor string) string {
	visibility := "false"
	if isPrivate {
		visibility = "true"
	}
	return `{"data":{
 "viewer":{"login":"fpresta0607","name":"Franco Presta"},
 "repository":{
  "nameWithOwner":"fpresta0607/northwind-api","isPrivate":` + visibility + `,
  "defaultBranchRef":{"name":"main","target":{"oid":"main0001","history":{"nodes":[
   {"committedDate":"2026-10-01T19:00:00Z","author":{"name":"x","user":{"login":"` + commitAuthor + `","avatarUrl":"https://a/x"}}}]}}},
  "recentIssues":{"nodes":[]},
  "recentPullRequests":{"nodes":[
   {"number":411,"createdAt":"2026-09-29T13:30:00Z","author":{"__typename":"Bot","login":"dependabot","avatarUrl":"https://a/db"}}]},
  "refs":{"pageInfo":{"hasNextPage":true,"endCursor":"R1"},"nodes":[
   {"name":"fix/refunds","target":{"oid":"rf0001","committedDate":"2026-10-01T13:25:42Z","author":{"name":"x","user":{"login":"` + branchAuthor + `","avatarUrl":"https://a/y"}}}}]}
 }}}`
}

func TestCollaborationReadsWhoWorksThereInOneRound(t *testing.T) {
	cases := []struct {
		name              string
		page              string
		wantCollaborative bool
		wantPrivate       bool
	}{
		{name: "a teammate's commit in a private repository", page: collaborationPage(true, "ana-teammate", "fpresta0607"), wantCollaborative: true, wantPrivate: true},
		{name: "a teammate's branch in a public repository", page: collaborationPage(false, "fpresta0607", "ben-teammate"), wantCollaborative: true},
		{name: "only the Overlord and a bot", page: collaborationPage(true, "fpresta0607", "fpresta0607"), wantPrivate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			gh := &fakeGitHub{graphql: []string{tc.page}}

			// Act
			got, err := GitHub{Commands: gh}.Collaboration(context.Background(), "fpresta0607/northwind-api", testNow)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got.Repository != "fpresta0607/northwind-api" || got.IsCollaborative != tc.wantCollaborative || got.IsPrivate != tc.wantPrivate {
				t.Fatalf("collaboration = %+v, want collaborative %v, private %v", got, tc.wantCollaborative, tc.wantPrivate)
			}
			if len(gh.calls) != 1 {
				t.Fatalf("calls = %v, want one GraphQL round and no page or file read after it", gh.calls)
			}
			want := map[string]string{"first": "true", "issues": "false", "pulls": "false", "refs": "true"}
			for name, value := range want {
				if got, _ := argValue(gh.calls[0], name); got != value {
					t.Fatalf("%s = %q, want %q: the read asks only for who worked here", name, got, value)
				}
			}
		})
	}
}

func TestCollaborationNamesWhatItCouldNotRead(t *testing.T) {
	gh := &fakeGitHub{failGh: "HTTP 401: Bad credentials"}
	_, err := GitHub{Commands: gh}.Collaboration(context.Background(), "fpresta0607/northwind-api", testNow)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v, want GitHub's failure", err)
	}
}

func TestPublicTicketsAreAllowedOnlyForARepositoryTheCFOAllowed(t *testing.T) {
	// Arrange
	dir := t.TempDir()

	// Act
	before, beforeErr := IsPublicAllowed(dir, "fpresta0607/northwind-api")
	allowErr := AllowPublic(dir, "FPresta0607/Northwind-API")
	after, afterErr := IsPublicAllowed(dir, "fpresta0607/northwind-api")
	other, otherErr := IsPublicAllowed(dir, "fpresta0607/northwind-web")
	againErr := AllowPublic(dir, "fpresta0607/northwind-web")
	both, bothErr := IsPublicAllowed(dir, "fpresta0607/northwind-api")

	// Assert
	if err := errors.Join(beforeErr, allowErr, afterErr, otherErr, againErr, bothErr); err != nil {
		t.Fatal(err)
	}
	if before || !after || other || !both {
		t.Fatalf("allowed before %v, after %v, another repository %v, after allowing a second %v; want false, true, false, true", before, after, other, both)
	}
}

func TestRecordNamesTheHarnessItsLabelsCarry(t *testing.T) {
	record := Record{Labels: []string{"cfo: pr open", "goblin: codex"}}
	if got := record.Harness(); got != "codex" {
		t.Fatalf("harness = %q, want codex", got)
	}
	if got := (Record{Labels: []string{"cfo: queued"}}).Harness(); got != "" {
		t.Fatalf("harness = %q, want none for a ticket nobody worked", got)
	}
}

func TestShouldBackOffReadsGitHubsRefusals(t *testing.T) {
	cases := []struct {
		status int
		want   bool
	}{{403, true}, {429, true}, {502, false}, {404, false}}
	for _, tc := range cases {
		issue := "repos/" + northwind + "/issues/501"
		gh := &issueGitHub{failures: map[string]int{"PATCH " + issue: tc.status, "GET repos/" + northwind: tc.status}}
		_, err := GitHub{Commands: gh}.Apply(context.Background(), northwind, openedRecord(InProgress, "cfo: in progress", "goblin: claude"), Ticket{TaskID: "nw-sync", State: Paused, Harness: "claude"}, 0)
		if got := ShouldBackOff(err); got != tc.want {
			t.Fatalf("status %d: back off = %v (err %v), want %v", tc.status, got, err, tc.want)
		}
	}
	if ShouldBackOff(nil) || ShouldBackOff(errors.New("plain")) {
		t.Fatal("an error GitHub did not send reads as a refusal to back off from")
	}
}
