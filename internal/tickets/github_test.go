package tickets

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fakeGitHub answers gh the way GitHub would, from canned responses, and
// records every call so a test can check what was asked and how often.
type fakeGitHub struct {
	graphql []string
	rest    map[string]string
	remote  string
	failGh  string
	calls   [][]string
}

func (f *fakeGitHub) Run(_ context.Context, req execx.Request) (execx.Result, error) {
	f.calls = append(f.calls, append([]string{req.Name}, req.Args...))
	if req.Name == "git" {
		if f.remote == "" {
			return execx.Result{ExitCode: 2, Stderr: []byte("error: No such remote 'origin'")}, nil
		}
		return execx.Result{Stdout: []byte(f.remote + "\n")}, nil
	}
	if req.Name != "gh" || len(req.Args) < 2 || req.Args[0] != "api" {
		return execx.Result{}, fmt.Errorf("unexpected command %s %v", req.Name, req.Args)
	}
	if f.failGh != "" {
		return execx.Result{ExitCode: 1, Stderr: []byte(f.failGh)}, nil
	}
	if req.Args[1] == "graphql" {
		if len(f.graphql) == 0 {
			return execx.Result{}, fmt.Errorf("no GraphQL response left for %v", req.Args)
		}
		response := f.graphql[0]
		f.graphql = f.graphql[1:]
		return execx.Result{Stdout: []byte(response)}, nil
	}
	for _, arg := range req.Args {
		if body, ok := f.rest[arg]; ok {
			return execx.Result{Stdout: []byte(body)}, nil
		}
	}
	return execx.Result{}, fmt.Errorf("no REST response for %v", req.Args)
}

func (f *fakeGitHub) graphqlCalls() [][]string {
	var calls [][]string
	for _, call := range f.calls {
		if len(call) > 2 && call[0] == "gh" && call[2] == "graphql" {
			calls = append(calls, call)
		}
	}
	return calls
}

func argValue(call []string, name string) (string, bool) {
	for _, arg := range call {
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value, true
		}
	}
	return "", false
}

const firstPage = `{"data":{
 "viewer":{"login":"fpresta0607","name":"Franco Presta"},
 "repository":{
  "nameWithOwner":"fpresta0607/northwind-api",
  "defaultBranchRef":{"name":"main","target":{"oid":"main0001","history":{"nodes":[
   {"committedDate":"2026-10-01T19:00:00Z","author":{"name":"Franco Presta","user":{"login":"fpresta0607","avatarUrl":"https://a/fp"}}},
   {"committedDate":"2026-09-30T10:00:00Z","author":{"name":"Ben Teammate","user":null}}]}}},
  "recentIssues":{"nodes":[
   {"number":416,"createdAt":"2026-10-01T14:00:00Z","author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"}},
   {"number":390,"createdAt":"2026-09-20T14:00:00Z","author":null}]},
  "recentPullRequests":{"nodes":[
   {"number":413,"createdAt":"2026-10-01T13:30:00Z","author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"}},
   {"number":411,"createdAt":"2026-09-29T13:30:00Z","author":{"__typename":"Bot","login":"dependabot","avatarUrl":"https://a/db"}}]},
  "issues":{"pageInfo":{"hasNextPage":false,"endCursor":"I1"},"nodes":[
   {"number":416,"title":"Tax engine: rounding rules for mixed carts","body":"Touches services/tax.py","url":"https://github.com/fpresta0607/northwind-api/issues/416","createdAt":"2026-10-01T14:00:00Z",
    "author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"},"assignees":{"nodes":[{"login":"ana-teammate"}]},"labels":{"nodes":[{"name":"enhancement"}]}}]},
  "pullRequests":{"pageInfo":{"hasNextPage":false,"endCursor":"P1"},"nodes":[
   {"number":413,"title":"fix(refunds): honest currency totals","url":"https://github.com/fpresta0607/northwind-api/pull/413","isDraft":false,"createdAt":"2026-10-01T13:30:00Z","updatedAt":"2026-10-01T15:00:00Z",
    "headRefName":"fix/refund-currency-defects","isCrossRepository":false,"changedFiles":2,"author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"},
    "files":{"nodes":[{"path":"api/routes_orders.py"},{"path":"tasks/billing_sync.py"}]}},
   {"number":409,"title":"fix(cart): checkout defects","url":"https://github.com/fpresta0607/northwind-api/pull/409","isDraft":true,"createdAt":"2026-09-28T13:30:00Z","updatedAt":"2026-09-29T15:00:00Z",
    "headRefName":"fix/cart-session-defects","isCrossRepository":false,"changedFiles":102,"author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"},
    "files":{"nodes":[{"path":"schema/v1/contract.json"}]}}]},
  "refs":{"pageInfo":{"hasNextPage":false,"endCursor":"R1"},"nodes":[
   {"name":"main","target":{"oid":"main0001","committedDate":"2026-10-01T19:00:00Z","author":{"name":"Franco Presta","user":{"login":"fpresta0607","avatarUrl":"https://a/fp"}}}},
   {"name":"fix/refund-currency-defects","target":{"oid":"rf0001","committedDate":"2026-10-01T13:25:42Z","author":{"name":"ana-teammate","user":{"login":"ana-teammate","avatarUrl":"https://a/ana"}}}},
   {"name":"fix/118-tax-rounding","target":{"oid":"tax0001","committedDate":"2026-09-25T00:21:48Z","author":{"name":"Ben Teammate","user":{"login":"ben-teammate","avatarUrl":"https://a/ben"}}}},
   {"name":"fix/sync-says-why","target":{"oid":"sync0001","committedDate":"2026-10-01T18:00:00Z","author":{"name":"Franco Presta","user":{"login":"fpresta0607","avatarUrl":"https://a/fp"}}}}]}
 }}}`

func TestReadTurnsOneGitHubReadIntoActivity(t *testing.T) {
	// Arrange
	gh := &fakeGitHub{
		graphql: []string{firstPage},
		rest: map[string]string{
			"repos/fpresta0607/northwind-api/pulls/409/files?per_page=100": "schema/v1/contract.json\nschema/v1/openapi.json\n",
			"repos/fpresta0607/northwind-api/compare/main0001...tax0001":   "services/rounding.py\n",
		},
	}

	// Act
	activity, err := GitHub{Commands: gh}.Read(context.Background(), "fpresta0607/northwind-api", testNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if activity.Repository != "fpresta0607/northwind-api" || activity.Viewer.Login != "fpresta0607" || activity.Viewer.Name != "Franco Presta" || activity.DefaultBranch != "main" || activity.DefaultHead != "main0001" {
		t.Fatalf("header = %+v", activity)
	}
	if len(activity.Issues) != 1 {
		t.Fatalf("issues = %+v", activity.Issues)
	}
	issue := activity.Issues[0]
	if issue.Number != 416 || issue.Author.Login != "ana-teammate" || issue.Author.AvatarURL != "https://a/ana" || !slices.Equal(issue.Assignees, []string{"ana-teammate"}) || !slices.Equal(issue.Labels, []string{"enhancement"}) || issue.Body != "Touches services/tax.py" {
		t.Fatalf("issue = %+v", issue)
	}
	if len(activity.PullRequests) != 2 {
		t.Fatalf("pull requests = %+v", activity.PullRequests)
	}
	if pull := activity.PullRequests[0]; pull.Number != 413 || pull.HeadRef != "fix/refund-currency-defects" || pull.IsDraft || !slices.Equal(pull.Files, []string{"api/routes_orders.py", "tasks/billing_sync.py"}) {
		t.Fatalf("pull request 413 = %+v", pull)
	}
	if pull := activity.PullRequests[1]; !pull.IsDraft || !slices.Equal(pull.Files, []string{"schema/v1/contract.json", "schema/v1/openapi.json"}) {
		t.Fatalf("pull request 409 = %+v, want every file read past the first hundred", pull)
	}
	branches := map[string]Branch{}
	for _, branch := range activity.Branches {
		branches[branch.Name] = branch
	}
	if len(branches) != 4 || branches["fix/118-tax-rounding"].Author.Login != "ben-teammate" || branches["fix/118-tax-rounding"].Head != "tax0001" {
		t.Fatalf("branches = %+v", activity.Branches)
	}
	if got := branches["fix/118-tax-rounding"].Files; !slices.Equal(got, []string{"services/rounding.py"}) {
		t.Fatalf("teammate branch files = %v, want the compare against main", got)
	}
	if got := branches["fix/refund-currency-defects"].Files; got != nil {
		t.Fatalf("a branch an open pull request heads was compared: %v", got)
	}
	if got := branches["fix/sync-says-why"].Files; got != nil {
		t.Fatalf("the Overlord's own branch was compared: %v", got)
	}
	kinds := map[string]int{}
	for _, event := range activity.Events {
		kinds[event.Kind]++
		if event.Number == 411 && !event.Author.IsBot {
			t.Fatalf("pull request 411's author is typed Bot but read as a person: %+v", event.Author)
		}
		if event.Kind == EventCommit && event.Author.Login == "" && event.Author.Name != "Ben Teammate" {
			t.Fatalf("unlinked commit author = %+v", event.Author)
		}
	}
	if kinds[EventCommit] != 2 || kinds[EventIssue] != 2 || kinds[EventPullRequest] != 2 {
		t.Fatalf("events by kind = %v", kinds)
	}
	if calls := gh.graphqlCalls(); len(calls) != 1 {
		t.Fatalf("GraphQL calls = %d, want 1 when no connection has a next page", len(calls))
	}
	if since, _ := argValue(gh.graphqlCalls()[0], "since"); since != "2026-09-01T20:00:00Z" {
		t.Fatalf("history since = %q, want 30 days before now", since)
	}
}

func TestReadFollowsEveryPageOfAConnectionAndNothingElse(t *testing.T) {
	// Arrange
	first := strings.Replace(firstPage, `"refs":{"pageInfo":{"hasNextPage":false,"endCursor":"R1"}`, `"refs":{"pageInfo":{"hasNextPage":true,"endCursor":"R1"}`, 1)
	second := `{"data":{"repository":{"nameWithOwner":"fpresta0607/northwind-api","refs":{"pageInfo":{"hasNextPage":false,"endCursor":"R2"},"nodes":[
	 {"name":"fix/second-page","target":{"oid":"sp0001","committedDate":"2026-09-30T00:00:00Z","author":{"name":"ana-teammate","user":{"login":"ana-teammate","avatarUrl":"https://a/ana"}}}}]}}}}`
	gh := &fakeGitHub{
		graphql: []string{first, second},
		rest: map[string]string{
			"repos/fpresta0607/northwind-api/pulls/409/files?per_page=100": "schema/v1/contract.json\n",
			"repos/fpresta0607/northwind-api/compare/main0001...tax0001":   "",
			"repos/fpresta0607/northwind-api/compare/main0001...sp0001":    "app/x.py\n",
		},
	}

	// Act
	activity, err := GitHub{Commands: gh}.Read(context.Background(), "fpresta0607/northwind-api", testNow)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	calls := gh.graphqlCalls()
	if len(calls) != 2 {
		t.Fatalf("GraphQL calls = %d, want 2", len(calls))
	}
	want := map[string]string{"first": "false", "issues": "false", "pulls": "false", "refs": "true", "refsAfter": "R1"}
	for name, value := range want {
		if got, _ := argValue(calls[1], name); got != value {
			t.Fatalf("second call %s = %q, want %q (args %v)", name, got, value, calls[1])
		}
	}
	if len(activity.Branches) != 5 || activity.Branches[4].Name != "fix/second-page" {
		t.Fatalf("branches = %+v, want the second page appended", activity.Branches)
	}
	if len(activity.Issues) != 1 || len(activity.PullRequests) != 2 || len(activity.Events) != 6 {
		t.Fatalf("the second page duplicated or lost the first page's reads: issues %d, pull requests %d, events %d", len(activity.Issues), len(activity.PullRequests), len(activity.Events))
	}
}

func TestReadNamesWhatItCouldNotRead(t *testing.T) {
	cases := []struct {
		name string
		gh   *fakeGitHub
		want string
	}{
		{name: "gh fails", gh: &fakeGitHub{failGh: "HTTP 401: Bad credentials"}, want: "HTTP 401: Bad credentials"},
		{name: "repository not visible", gh: &fakeGitHub{graphql: []string{`{"data":{"viewer":{"login":"fpresta0607"},"repository":null},"errors":[{"message":"Could not resolve to a Repository with the name 'fpresta0607/missing'."}]}`}}, want: "Could not resolve to a Repository"},
		{name: "repository missing without an error", gh: &fakeGitHub{graphql: []string{`{"data":{"viewer":{"login":"fpresta0607"},"repository":null}}`}}, want: "not visible"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := GitHub{Commands: tc.gh}.Read(context.Background(), "fpresta0607/missing", testNow)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

func TestRepositoryOfReadsTheOriginRemote(t *testing.T) {
	cases := []struct {
		remote  string
		want    string
		wantErr string
	}{
		{remote: "https://github.com/fpresta0607/northwind-api.git", want: "fpresta0607/northwind-api"},
		{remote: "https://github.com/fpresta0607/code-goblins", want: "fpresta0607/code-goblins"},
		{remote: "git@github.com:fpresta0607/siqshift.git", want: "fpresta0607/siqshift"},
		{remote: "https://gitlab.com/someone/project.git", wantErr: "not a GitHub repository"},
		{remote: "", wantErr: "No such remote"},
	}
	for _, tc := range cases {
		t.Run(tc.remote, func(t *testing.T) {
			got, err := GitHub{Commands: &fakeGitHub{remote: tc.remote}}.RepositoryOf(context.Background(), `C:\dev\Project`)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("repository = %q, %v, want %q", got, err, tc.want)
			}
		})
	}
}
