package tickets

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// pullIssuesGitHub answers the GraphQL queries PullRequestIssues makes the way
// GitHub would: answers holds, by pull request address, the aliased field each
// is answered with, and an address it holds none for is answered null. Each
// query's text and variables are recorded.
type pullIssuesGitHub struct {
	answers   map[string]map[string]any
	queries   []string
	variables []map[string]any
	// graphqlErrors answers each query with GitHub's errors.
	graphqlErrors bool
}

func (f *pullIssuesGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query, variables, err := githubtest.Query(r)
	if err != nil {
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
		return
	}
	f.queries = append(f.queries, query)
	f.variables = append(f.variables, variables)
	if f.graphqlErrors {
		githubtest.Write(w, http.StatusOK, map[string]any{"data": nil, "errors": []map[string]any{{"message": "Something went wrong"}}})
		return
	}
	data := map[string]any{}
	for name, value := range variables {
		data["pull"+strings.TrimPrefix(name, "url")] = f.answers[fmt.Sprint(value)]
	}
	githubtest.Answer(w, data)
}

func pullAnswer(address, body string, closing ...IssueRef) map[string]any {
	nodes := []map[string]any{}
	for _, ref := range closing {
		nodes = append(nodes, map[string]any{"number": ref.Number, "repository": map[string]any{"nameWithOwner": ref.Repository}})
	}
	return map[string]any{"url": address, "body": body, "closingIssuesReferences": map[string]any{"nodes": nodes}}
}

func TestPullRequestIssuesNamesTheClosingReferencesAndTheBody(t *testing.T) {
	// Arrange
	const address = "https://github.com/Acme/Widgets/pull/8"
	fake := &pullIssuesGitHub{answers: map[string]map[string]any{
		address: pullAnswer(address, "Part of #12\nSee #13 too.\nFixes #3", IssueRef{"Acme/Widgets", 3}, IssueRef{"Other/Repo", 4}),
	}}

	// Act
	named, unread, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), []string{address})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []IssueRef{{"acme/widgets", 3}, {"other/repo", 4}, {"acme/widgets", 12}}
	if got := named[address]; !slices.Equal(got, want) {
		t.Fatalf("named = %v, want %v", got, want)
	}
	if len(unread) != 0 {
		t.Fatalf("unread = %v, want none", unread)
	}
}

func TestPullRequestIssuesReadsFiftyPullRequestsToAQuery(t *testing.T) {
	// Arrange
	fake := &pullIssuesGitHub{answers: map[string]map[string]any{}}
	var addresses []string
	for number := 1; number <= 51; number++ {
		address := fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number)
		addresses = append(addresses, address)
		fake.answers[address] = pullAnswer(address, fmt.Sprintf("Closes #%d", number+1000))
	}

	// Act
	named, unread, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), addresses)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.queries) != 2 || len(fake.variables[0]) != 50 || len(fake.variables[1]) != 1 {
		t.Fatalf("queries = %d with %d and %d addresses, want two with 50 and 1", len(fake.queries), len(fake.variables[0]), len(fake.variables[1]))
	}
	if len(named) != 51 || len(unread) != 0 {
		t.Fatalf("named %d pull requests, unread = %v, want 51 and none", len(named), unread)
	}
	if got, want := named[addresses[50]], []IssueRef{{"acme/widgets", 1051}}; !slices.Equal(got, want) {
		t.Fatalf("last pull request named %v, want %v", got, want)
	}
}

func TestPullRequestIssuesSendsAddressesAsVariablesNeverInTheQuery(t *testing.T) {
	// Arrange
	const address = "https://github.com/acme/widgets/pull/8"
	fake := &pullIssuesGitHub{answers: map[string]map[string]any{address: pullAnswer(address, "")}}

	// Act
	_, _, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), []string{address})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fake.queries[0], "acme") || strings.Contains(fake.queries[0], "github.com") {
		t.Fatalf("query spliced the address in: %s", fake.queries[0])
	}
	if got := slices.Collect(maps.Values(fake.variables[0])); !slices.Equal(got, []any{address}) {
		t.Fatalf("variables = %v, want the address", fake.variables[0])
	}
}

func TestPullRequestIssuesReadsADuplicateAddressOnce(t *testing.T) {
	// Arrange
	const address = "https://github.com/acme/widgets/pull/8"
	fake := &pullIssuesGitHub{answers: map[string]map[string]any{address: pullAnswer(address, "Fixes #3")}}

	// Act
	named, _, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), []string{address, address})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.variables) != 1 || len(fake.variables[0]) != 1 || len(named) != 1 {
		t.Fatalf("variables = %v, named = %v, want one address read once", fake.variables, named)
	}
}

func TestPullRequestIssuesReturnsWhatItCannotReadAsUnread(t *testing.T) {
	// Arrange
	const (
		missing   = "https://github.com/acme/widgets/pull/9"
		anIssue   = "https://github.com/acme/widgets/pull/10"
		malformed = "https://github.com/acme/widgets/issues/11"
		readable  = "https://github.com/acme/widgets/pull/8"
	)
	fake := &pullIssuesGitHub{answers: map[string]map[string]any{
		readable: pullAnswer(readable, "Fixes #3"),
		anIssue:  {},
	}}

	// Act
	named, unread, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), []string{malformed, missing, readable, anIssue, "not an address", malformed})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{malformed, "not an address", missing, anIssue}; !slices.Equal(unread, want) {
		t.Fatalf("unread = %v, want %v", unread, want)
	}
	if got, want := named[readable], []IssueRef{{"acme/widgets", 3}}; len(named) != 1 || !slices.Equal(got, want) {
		t.Fatalf("named = %v, want only %s as %v", named, readable, want)
	}
	if len(fake.variables) != 1 || len(fake.variables[0]) != 3 {
		t.Fatalf("variables = %v, want one query of the three pull request addresses", fake.variables)
	}
}

func TestPullRequestIssuesFailsWhenAQueryFails(t *testing.T) {
	// Arrange
	fake := &pullIssuesGitHub{graphqlErrors: true}

	// Act
	named, unread, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), []string{"https://github.com/acme/widgets/pull/8"})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "Something went wrong") {
		t.Fatalf("err = %v, want GitHub's message", err)
	}
	if named != nil || unread != nil {
		t.Fatalf("named = %v, unread = %v, want neither from a failed read", named, unread)
	}
}

func TestPullRequestIssuesReadsNoAddressesWithoutAQuery(t *testing.T) {
	// Arrange
	fake := &pullIssuesGitHub{}

	// Act
	named, unread, err := GitHub{API: githubtest.Client(fake)}.PullRequestIssues(context.Background(), nil)

	// Assert
	if err != nil || len(named) != 0 || len(unread) != 0 || len(fake.queries) != 0 {
		t.Fatalf("named = %v, unread = %v, err = %v, queries = %d, want nothing", named, unread, err, len(fake.queries))
	}
}
