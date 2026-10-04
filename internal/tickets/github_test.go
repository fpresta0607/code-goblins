package tickets

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fakeGitHub answers gh the way GitHub would, from canned responses, and
// records every call so a test can check what was asked and how often.
type fakeGitHub struct {
	graphql         []string
	graphqlExitCode int
	graphqlStderr   string
	rest            map[string]string
	restFail        map[string]string
	remote          string
	failGh          string
	calls           [][]string
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
		return execx.Result{Stdout: []byte(response), ExitCode: f.graphqlExitCode, Stderr: []byte(f.graphqlStderr)}, nil
	}
	for _, arg := range req.Args {
		if stderr, ok := f.restFail[arg]; ok {
			return execx.Result{ExitCode: 1, Stderr: []byte(stderr)}, nil
		}
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
    "headRefName":"fix/cart-session-defects","isCrossRepository":false,"changedFiles":2,"author":{"__typename":"User","login":"ana-teammate","avatarUrl":"https://a/ana"},
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
			"repos/fpresta0607/northwind-api/pulls/409/files?per_page=100&page=1": "schema/v1/contract.json\nschema/v1/openapi.json\n",
			"repos/fpresta0607/northwind-api/compare/main0001...tax0001":          "services/rounding.py\n",
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
			"repos/fpresta0607/northwind-api/pulls/409/files?per_page=100&page=1": "schema/v1/contract.json\nschema/v1/openapi.json\n",
			"repos/fpresta0607/northwind-api/compare/main0001...tax0001":          "",
			"repos/fpresta0607/northwind-api/compare/main0001...sp0001":           "app/x.py\n",
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

func TestReadNamesAFailedPerItemReadAndKeepsTheRest(t *testing.T) {
	withPages := strings.Replace(firstPage, `{"name":"fix/sync-says-why"`, `{"name":"gh-pages","target":{"oid":"pages0001","committedDate":"2026-09-30T00:00:00Z","author":{"name":"github-actions[bot]","user":null}}},
   {"name":"fix/sync-says-why"`, 1)
	const (
		pullFiles   = "repos/fpresta0607/northwind-api/pulls/409/files?per_page=100&page=1"
		taxCompare  = "repos/fpresta0607/northwind-api/compare/main0001...tax0001"
		pageCompare = "repos/fpresta0607/northwind-api/compare/main0001...pages0001"
	)
	cases := []struct {
		name         string
		failing      string
		stderr       string
		wantUnread   string
		wantPull409  []string
		wantBranches map[string][]string
	}{
		{
			name:         "a branch compare",
			failing:      pageCompare,
			stderr:       "gh: No common ancestor between main0001 and pages0001. (HTTP 404)",
			wantUnread:   "the changed files of branch gh-pages: ",
			wantPull409:  []string{"schema/v1/contract.json", "schema/v1/openapi.json"},
			wantBranches: map[string][]string{"fix/118-tax-rounding": {"services/rounding.py"}, "gh-pages": nil},
		},
		{
			name:         "a pull request's files past the first hundred",
			failing:      pullFiles,
			stderr:       "gh: Server Error (HTTP 502)",
			wantUnread:   "the changed files of pull request 409 past the first 1: ",
			wantPull409:  []string{"schema/v1/contract.json"},
			wantBranches: map[string][]string{"fix/118-tax-rounding": {"services/rounding.py"}, "gh-pages": {"index.html"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			rest := map[string]string{
				pullFiles:   "schema/v1/contract.json\nschema/v1/openapi.json\n",
				taxCompare:  "services/rounding.py\n",
				pageCompare: "index.html\n",
			}
			delete(rest, tc.failing)
			gh := &fakeGitHub{graphql: []string{withPages}, rest: rest, restFail: map[string]string{tc.failing: tc.stderr}}

			// Act
			activity, err := GitHub{Commands: gh}.Read(context.Background(), "fpresta0607/northwind-api", testNow)

			// Assert
			if err != nil {
				t.Fatalf("one failed read failed the whole report: %v", err)
			}
			if len(activity.Unread) != 1 || !strings.HasPrefix(activity.Unread[0], tc.wantUnread) || !strings.Contains(activity.Unread[0], tc.stderr) {
				t.Fatalf("unread = %q, want one entry starting %q and naming %q", activity.Unread, tc.wantUnread, tc.stderr)
			}
			if got := activity.PullRequests[1].Files; !slices.Equal(got, tc.wantPull409) {
				t.Fatalf("pull request 409 files = %v, want %v", got, tc.wantPull409)
			}
			for _, branch := range activity.Branches {
				if want, ok := tc.wantBranches[branch.Name]; ok && !slices.Equal(branch.Files, want) {
					t.Fatalf("branch %s files = %v, want %v", branch.Name, branch.Files, want)
				}
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
		{remote: "git@github.com:fpresta0607/northwind-web.git", want: "fpresta0607/northwind-web"},
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

func TestReadKeepsEarlierPagesWhenALaterGraphQLReadFails(t *testing.T) {
	first := firstPageWithPullFileCount(1)
	first = strings.Replace(first, `"refs":{"pageInfo":{"hasNextPage":false,"endCursor":"R1"}`, `"refs":{"pageInfo":{"hasNextPage":true,"endCursor":"R1"}`, 1)
	gh := &fakeGitHub{graphql: []string{first, `{"data":{"repository":null},"errors":[{"message":"later page unavailable"}]}`}, rest: map[string]string{"repos/fpresta0607/northwind-api/compare/main0001...tax0001": "services/rounding.py\n"}}

	activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

	if err != nil {
		t.Fatalf("a later page discarded valid earlier activity: %v", err)
	}
	if len(activity.Issues) != 1 || len(activity.PullRequests) != 2 || len(activity.Branches) != 4 || len(activity.Events) != 6 {
		t.Fatalf("earlier activity was lost: %+v", activity)
	}
	if len(activity.Unread) != 1 || !strings.Contains(activity.Unread[0], "later page unavailable") || !strings.Contains(activity.Unread[0], "branches") {
		t.Fatalf("unread = %v, want the failed pending branch page", activity.Unread)
	}
}

func TestReadNamesGraphQLErrorsWhileKeepingReadableConnections(t *testing.T) {
	response := `{"errors":[{"message":"collaboration history unavailable","path":["repository","defaultBranchRef"]}],"data":{
	"viewer":{"login":"fpresta0607"},"repository":{
	"nameWithOwner":"fpresta0607/northwind-api","defaultBranchRef":null,"recentIssues":null,"recentPullRequests":null,
	"issues":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":416,"title":"tax rounding","createdAt":"2026-10-01T14:00:00Z","author":{"__typename":"User","login":"ana-teammate"}}]},
	"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":413,"title":"tax correction","createdAt":"2026-10-01T13:30:00Z","changedFiles":1,"files":{"nodes":[{"path":"services/tax.py"}]},"author":{"__typename":"User","login":"ana-teammate"}}]},
	"refs":{"pageInfo":{"hasNextPage":false},"nodes":[]}
	}}}`
	gh := &fakeGitHub{graphql: []string{response}}

	activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

	if err != nil {
		t.Fatalf("readable item connections were discarded: %v", err)
	}
	if len(activity.Issues) != 1 || len(activity.PullRequests) != 1 || !slices.Equal(activity.PullRequests[0].Files, []string{"services/tax.py"}) {
		t.Fatalf("valid items were lost: %+v", activity)
	}
	if len(activity.Unread) != 1 || !strings.Contains(activity.Unread[0], "collaboration history unavailable") {
		t.Fatalf("GraphQL optional metadata error was silently ignored: unread %v", activity.Unread)
	}
}

func TestReadKeepsReadableGraphQLErrorDataOnNonzeroExit(t *testing.T) {
	cases := []struct {
		name             string
		hasGraphQLErrors bool
		stderr           string
	}{
		{name: "readable resolver failure", hasGraphQLErrors: true, stderr: "gh: collaboration history unavailable"},
		{name: "ordinary refusal without GraphQL errors", stderr: "gh: forbidden (HTTP 403)"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			first := firstPageWithPullFileCount(1)
			if testCase.hasGraphQLErrors {
				first = strings.Replace(first, `{"data":`, `{"errors":[{"message":"collaboration history unavailable"}],"data":`, 1)
			}
			gh := &fakeGitHub{graphql: []string{first}, graphqlExitCode: 1, graphqlStderr: testCase.stderr, rest: map[string]string{"repos/fpresta0607/northwind-api/compare/main0001...tax0001": "services/rounding.py\n"}}

			activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

			if !testCase.hasGraphQLErrors {
				if err == nil || !strings.Contains(err.Error(), "HTTP 403") || len(activity.Issues) != 0 || len(activity.PullRequests) != 0 {
					t.Fatalf("ordinary nonzero/refusal was accepted: activity %+v, error %v", activity, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodable items accompanying GraphQL errors were discarded: %v", err)
			}
			if len(activity.Issues) != 1 || len(activity.PullRequests) != 2 {
				t.Fatalf("valid item connections were lost: %+v", activity)
			}
			unread := strings.Join(activity.Unread, "; ")
			if !strings.Contains(unread, "collaboration history unavailable") || !strings.Contains(unread, "exited 1") {
				t.Fatalf("partial response failure lacks honest error/exit evidence: %v", activity.Unread)
			}
		})
	}
}

func TestReadOpenWorkKeepsMetadataWithoutBranchCompares(t *testing.T) {
	first := firstPageWithPullFileCount(1)
	gh := &fakeGitHub{graphql: []string{first}}

	activity, err := (GitHub{Commands: gh}).ReadOpenWork(context.Background(), "fpresta0607/northwind-api", testNow)

	if err != nil {
		t.Fatal(err)
	}
	if len(activity.Branches) != 4 || len(activity.Events) != 6 || len(activity.PullRequests) != 2 || len(activity.Issues) != 1 || activity.Viewer.Login != "fpresta0607" || activity.DefaultHead != "main0001" {
		t.Fatalf("narrow read lost collaboration or open-item metadata: %+v", activity)
	}
	if len(activity.Unread) != 0 || len(gh.calls) != 1 {
		t.Fatalf("narrow read attempted a branch compare: calls %v, unread %v", gh.calls, activity.Unread)
	}
}

func TestReadPullFilesUsesExplicitPagesAndKeepsPartialFiles(t *testing.T) {
	const endpoint = "repos/fpresta0607/northwind-api/pulls/409/files?per_page=100"
	var firstFiles []string
	for number := 0; number < 100; number++ {
		firstFiles = append(firstFiles, fmt.Sprintf("schema/file-%03d.json", number))
	}
	firstFiles[0] = "schema/v1/contract.json"
	cases := []struct {
		name       string
		secondBody string
		secondFail string
		wantFiles  []string
		wantUnread string
	}{
		{name: "complete", secondBody: "schema/last-a.json\nschema/last-b.json\n", wantFiles: append(slices.Clone(firstFiles), "schema/last-a.json", "schema/last-b.json")},
		{name: "later failure", secondFail: "HTTP 502: page unavailable", wantFiles: firstFiles, wantUnread: "page unavailable"},
		{name: "short page", secondBody: "schema/last-a.json\n", wantFiles: append(slices.Clone(firstFiles), "schema/last-a.json"), wantUnread: "101 of 102"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			gh := &fakeGitHub{graphql: []string{firstPageWithPullFileCount(102)}, rest: map[string]string{
				endpoint + "&page=1": strings.Join(firstFiles, "\n") + "\n",
				endpoint + "&page=2": testCase.secondBody,
				"repos/fpresta0607/northwind-api/compare/main0001...tax0001": "services/rounding.py\n",
			}, restFail: map[string]string{}}
			if testCase.secondFail != "" {
				delete(gh.rest, endpoint+"&page=2")
				gh.restFail[endpoint+"&page=2"] = testCase.secondFail
			}

			activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

			if err != nil {
				t.Fatal(err)
			}
			if got := activity.PullRequests[1].Files; !slices.Equal(got, testCase.wantFiles) {
				t.Fatalf("large PR files = %v, want %v", got, testCase.wantFiles)
			}
			if testCase.wantUnread == "" {
				if len(activity.Unread) != 0 {
					t.Fatalf("complete pages marked unread: %v", activity.Unread)
				}
			} else if len(activity.Unread) != 1 || !strings.Contains(activity.Unread[0], testCase.wantUnread) {
				t.Fatalf("unread = %v, want %q", activity.Unread, testCase.wantUnread)
			}
			for _, call := range gh.calls {
				if slices.Contains(call, "--paginate") {
					t.Fatalf("hidden pagination bypasses per-response allowance checks: %v", call)
				}
			}
		})
	}
}

func TestReadPullFilesNamesTheExplicitPageBound(t *testing.T) {
	const endpoint = "repos/fpresta0607/northwind-api/pulls/409/files?per_page=100"
	first := firstPageWithPullFileCount(1001)
	gh := &fakeGitHub{graphql: []string{first}, rest: map[string]string{"repos/fpresta0607/northwind-api/compare/main0001...tax0001": "services/rounding.py\n"}}
	for page := 1; page <= maxPageRounds; page++ {
		var files []string
		for number := 0; number < 100; number++ {
			files = append(files, fmt.Sprintf("schema/file-%04d.json", (page-1)*100+number))
		}
		if page == 1 {
			files[0] = "schema/v1/contract.json"
		}
		gh.rest[fmt.Sprintf("%s&page=%d", endpoint, page)] = strings.Join(files, "\n") + "\n"
	}

	activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

	if err != nil {
		t.Fatal(err)
	}
	if got := len(activity.PullRequests[1].Files); got != 1000 {
		t.Fatalf("bounded partial files = %d, want 1000 distinct files", got)
	}
	if len(activity.Unread) != 1 || !strings.Contains(activity.Unread[0], "first 10 pages") {
		t.Fatalf("unread = %v, want the concrete page bound", activity.Unread)
	}
	if len(gh.calls) != 12 {
		t.Fatalf("calls = %d, want one GraphQL, ten file pages and one branch compare", len(gh.calls))
	}
}

func firstPageWithPullFileCount(count int) string {
	return strings.Replace(firstPage,
		`"headRefName":"fix/cart-session-defects","isCrossRepository":false,"changedFiles":2`,
		fmt.Sprintf(`"headRefName":"fix/cart-session-defects","isCrossRepository":false,"changedFiles":%d`, count), 1)
}

func TestReadKeepsReadableConnectionsWhenAnotherResolverFails(t *testing.T) {
	page := func(missing string, hasNextPage bool, number int, hasErrors bool) string {
		issues := fmt.Sprintf(`{"pageInfo":{"hasNextPage":%t,"endCursor":"I%d"},"nodes":[{"number":%d,"title":"tax rounding","createdAt":"2026-10-01T14:00:00Z","author":{"__typename":"User","login":"ana-teammate"}}]}`, hasNextPage, number, number)
		pulls := fmt.Sprintf(`{"pageInfo":{"hasNextPage":%t,"endCursor":"P%d"},"nodes":[{"number":%d,"title":"tax correction","createdAt":"2026-10-01T13:30:00Z","changedFiles":1,"files":{"nodes":[{"path":"services/tax.py"}]},"author":{"__typename":"User","login":"ana-teammate"}}]}`, hasNextPage, number, number+1)
		refs := fmt.Sprintf(`{"pageInfo":{"hasNextPage":%t,"endCursor":"R%d"},"nodes":[{"name":"fix/tax-%d","target":{"oid":"tax%d","committedDate":"2026-10-01T13:00:00Z","author":{"name":"Ana","user":{"login":"ana-teammate"}}}}]}`, hasNextPage, number, number, number)
		switch missing {
		case "issues":
			issues = "null"
		case "pulls":
			pulls = "null"
		case "refs":
			refs = "null"
		}
		errors := ""
		if hasErrors {
			errors = `"errors":[{"message":"resolver unavailable"}],`
		}
		return fmt.Sprintf(`{%s"data":{"viewer":{"login":"fpresta0607"},"repository":{
		"nameWithOwner":"fpresta0607/northwind-api",
		"defaultBranchRef":{"name":"main","target":{"oid":"main0001","history":{"nodes":[{"committedDate":"2026-10-01T12:00:00Z","author":{"name":"Ana","user":{"login":"ana-teammate"}}}]}}},
		"issues":%s,"pullRequests":%s,"refs":%s}}}`, errors, issues, pulls, refs)
	}
	for _, missing := range []string{"issues", "pulls", "refs"} {
		for _, exitCode := range []int{0, 1} {
			for _, isLaterPage := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/exit%d/later%t", missing, exitCode, isLaterPage), func(t *testing.T) {
					responses := []string{page(missing, false, 200, true)}
					wantCalls := 1
					if isLaterPage {
						responses = append([]string{page("", true, 100, true)}, responses...)
						wantCalls = 2
					}
					gh := &fakeGitHub{graphql: responses, graphqlExitCode: exitCode, graphqlStderr: "gh: resolver unavailable", rest: map[string]string{
						"repos/fpresta0607/northwind-api/compare/main0001...tax100": "services/rounding.py\n",
						"repos/fpresta0607/northwind-api/compare/main0001...tax200": "services/rounding.py\n",
					}}

					activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

					if err != nil {
						t.Fatalf("a failed sibling connection discarded readable data: %v", err)
					}
					counts := map[string]int{"issues": len(activity.Issues), "pulls": len(activity.PullRequests), "refs": len(activity.Branches)}
					for connection, count := range counts {
						want := wantCalls
						if connection == missing {
							want--
						}
						if count != want {
							t.Fatalf("%s count = %d, want %d", connection, count, want)
						}
					}
					if activity.Viewer.Login != "fpresta0607" || activity.DefaultHead != "main0001" || len(activity.Events) != 1 || len(contributors(activity, testNow)) == 0 {
						t.Fatalf("readable collaboration evidence was lost: %+v", activity)
					}
					for _, branch := range activity.Branches {
						if !slices.Equal(branch.Files, []string{"services/rounding.py"}) {
							t.Fatalf("readable branch compare was lost: %+v", branch)
						}
					}
					unread := strings.Join(activity.Unread, "; ")
					if !strings.Contains(unread, "open "+connectionNoun[missing]) || !strings.Contains(unread, "resolver unavailable") || exitCode != 0 && !strings.Contains(unread, "exited 1") {
						t.Fatalf("missing connection was not explicitly unread: %v", activity.Unread)
					}
					if got := len(gh.graphqlCalls()); got != wantCalls {
						t.Fatalf("missing connection kept paging: calls %d, want %d", got, wantCalls)
					}
				})
			}
		}
	}
	t.Run("ordinary refusal remains fatal with missing issues", func(t *testing.T) {
		gh := &fakeGitHub{graphql: []string{page("issues", false, 200, false)}, graphqlExitCode: 1, graphqlStderr: "gh: forbidden (HTTP 403)"}

		activity, err := (GitHub{Commands: gh}).Read(context.Background(), "fpresta0607/northwind-api", testNow)

		if err == nil || !strings.Contains(err.Error(), "HTTP 403") || len(activity.PullRequests) != 0 || len(gh.calls) != 1 {
			t.Fatalf("ordinary refusal was accepted as partial data: activity %+v, calls %v, error %v", activity, gh.calls, err)
		}
	})
}

func TestCollaborationKeepsNonzeroResolverFailureFatal(t *testing.T) {
	response := fmt.Sprintf(`{"errors":[{"message":"viewer unavailable"}],"data":{"viewer":null,"repository":{
	"isPrivate":true,"defaultBranchRef":{"name":"main","target":{"oid":"base","history":{"nodes":[]}}},
	"recentIssues":{"nodes":[]},"recentPullRequests":{"nodes":[]},
	"refs":{"nodes":[{"name":"feat/owner","target":{"oid":"head","committedDate":"%s","author":{"name":"Overlord","user":{"login":"fpresta0607"}}}}],"pageInfo":{"hasNextPage":false}}
	}}}`, testNow.Format(time.RFC3339))
	gh := &fakeGitHub{graphql: []string{response}, graphqlExitCode: 1, graphqlStderr: "gh: viewer unavailable"}

	result, err := (GitHub{Commands: gh}).Collaboration(context.Background(), "fpresta0607/northwind-api", testNow)

	if err == nil || !strings.Contains(err.Error(), "exited 1") || result.IsCollaborative {
		t.Fatalf("failed identity read enabled collaboration: result %+v, error %v", result, err)
	}
}
