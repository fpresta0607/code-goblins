package tickets

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/githubtest"
)

// openIssuesGitHub answers GitHub's list of open issues a page of a hundred
// at a time: issues is every open issue, as GitHub lists them. It records the
// path of each request.
type openIssuesGitHub struct {
	issues []map[string]any
	paths  []string
}

func (f *openIssuesGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call, err := readCall(r)
	if err != nil {
		githubtest.Refuse(w, http.StatusBadRequest, err.Error())
		return
	}
	f.paths = append(f.paths, call.Path)
	var page int
	if _, err := fmt.Sscanf(call.Path[strings.Index(call.Path, "&page=")+len("&page="):], "%d", &page); err != nil || !strings.HasPrefix(call.Path, "repos/"+rootRepository+"/issues?state=open&per_page=100&page=") {
		githubtest.Refuse(w, http.StatusNotFound, "Not Found")
		return
	}
	start := min((page-1)*100, len(f.issues))
	githubtest.Write(w, http.StatusOK, f.issues[start:min(start+100, len(f.issues))])
}

func numberedIssues(count int) []map[string]any {
	issues := make([]map[string]any, count)
	for index := range issues {
		issues[index] = map[string]any{"number": index + 1, "title": fmt.Sprint("issue ", index+1), "html_url": fmt.Sprintf("https://github.com/%s/issues/%d", rootRepository, index+1)}
	}
	return issues
}

func TestOpenIssuesSkipsPullRequestsAndKeepsLabels(t *testing.T) {
	// Arrange
	fake := &openIssuesGitHub{issues: []map[string]any{
		{"number": 1, "title": "first", "html_url": "https://github.com/acme/widgets/issues/1", "labels": []map[string]any{{"name": "bug"}, {"name": "ops-root"}}},
		{"number": 2, "title": "a pull request", "html_url": "https://github.com/acme/widgets/pull/2", "pull_request": map[string]any{"url": "x"}},
		{"number": 3, "title": "third", "html_url": "https://github.com/acme/widgets/issues/3"},
	}}

	// Act
	open, err := GitHub{API: githubtest.Client(fake)}.OpenIssues(context.Background(), rootRepository)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	want := []OpenIssue{
		{Number: 1, Title: "first", URL: "https://github.com/acme/widgets/issues/1", Labels: []string{"bug", "ops-root"}},
		{Number: 3, Title: "third", URL: "https://github.com/acme/widgets/issues/3"},
	}
	if fmt.Sprint(open) != fmt.Sprint(want) {
		t.Fatalf("open = %+v, want %+v", open, want)
	}
	if len(fake.paths) != 1 {
		t.Fatalf("paths = %v, want one page", fake.paths)
	}
}

func TestOpenIssuesReadsAnotherPageWhenTheFirstIsFull(t *testing.T) {
	// Arrange
	fake := &openIssuesGitHub{issues: numberedIssues(150)}

	// Act
	open, err := GitHub{API: githubtest.Client(fake)}.OpenIssues(context.Background(), rootRepository)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 150 || open[149].Number != 150 {
		t.Fatalf("read %d issues, want 150", len(open))
	}
	if want := []string{
		"repos/acme/widgets/issues?state=open&per_page=100&page=1",
		"repos/acme/widgets/issues?state=open&per_page=100&page=2",
	}; !slices.Equal(fake.paths, want) {
		t.Fatalf("paths = %v, want %v", fake.paths, want)
	}
}

func TestOpenIssuesRefusesMoreThanItCanRead(t *testing.T) {
	// Arrange
	fake := &openIssuesGitHub{issues: numberedIssues(maxPageRounds*100 + 1)}

	// Act
	open, err := GitHub{API: githubtest.Client(fake)}.OpenIssues(context.Background(), rootRepository)

	// Assert
	if err == nil || !strings.Contains(err.Error(), "more than 1000 open issues") || open != nil {
		t.Fatalf("open = %d issues, err = %v, want an error and no partial answer", len(open), err)
	}
	if len(fake.paths) != maxPageRounds {
		t.Fatalf("read %d pages, want %d", len(fake.paths), maxPageRounds)
	}
}

func TestOpenIssuesFailsWhenGitHubRefuses(t *testing.T) {
	// Arrange
	refuse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		githubtest.Refuse(w, http.StatusUnauthorized, "Bad credentials")
	})

	// Act
	open, err := GitHub{API: githubtest.Client(refuse)}.OpenIssues(context.Background(), rootRepository)

	// Assert
	if err == nil || open != nil {
		t.Fatalf("open = %v, err = %v, want an error", open, err)
	}
}
