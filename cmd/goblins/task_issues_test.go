package main

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
	"github.com/fpresta0607/code-goblins/internal/githubtest"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// taskIssuesGit answers the two git reads openTaskIssues makes: the origin
// of the project checkout and the branch of the task's worktree.
type taskIssuesGit struct{}

func (taskIssuesGit) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	arguments := strings.Join(request.Args, " ")
	switch {
	case arguments == "symbolic-ref --quiet --short HEAD":
		return execx.Result{Stdout: []byte("fix/x\n")}, nil
	case strings.HasSuffix(arguments, "remote get-url origin"):
		return execx.Result{Stdout: []byte("https://github.com/acme/widgets.git\n")}, nil
	}
	return execx.Result{ExitCode: 129, Stderr: []byte("unexpected git " + arguments)}, nil
}

// taskIssuesGitHub answers what the task's pull request names, each
// repository as one GitHub shows unless it was told to hide them, and the
// state of each issue it knows, "pull request" answering as GitHub answers an
// issue read of a pull request, and it refuses an issue it does not know and
// a pull request it was told to hide.
type taskIssuesGitHub struct {
	states               map[string]string
	isPullGone           bool
	isRepositoriesHidden bool
}

func (g taskIssuesGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/graphql" {
		pull := map[string]any{"url": "https://github.com/acme/widgets/pull/50", "body": "Part of acme/gadgets#3", "closingIssuesReferences": map[string]any{"nodes": []any{map[string]any{"number": 2, "repository": map[string]any{"nameWithOwner": "acme/widgets"}}}}}
		if g.isPullGone {
			pull = nil
		}
		githubtest.Answer(w, map[string]any{"pull0": pull})
		return
	}
	repository, number, isIssue := strings.Cut(strings.TrimPrefix(r.URL.Path, "/repos/"), "/issues/")
	if r.Method == http.MethodGet && !isIssue && !g.isRepositoriesHidden && strings.Count(repository, "/") == 1 {
		githubtest.Write(w, http.StatusOK, map[string]any{"full_name": repository})
		return
	}
	issueState, isKnown := g.states[repository+"#"+number]
	issueNumber, err := strconv.Atoi(number)
	if r.Method != http.MethodGet || !isIssue || !isKnown || err != nil {
		githubtest.Refuse(w, http.StatusNotFound, "Not Found")
		return
	}
	if issueState == "pull request" {
		githubtest.Write(w, http.StatusOK, map[string]any{"number": issueNumber, "html_url": "https://github.com/" + repository + "/pull/" + number, "state": "open", "pull_request": map[string]any{}})
		return
	}
	githubtest.Write(w, http.StatusOK, map[string]any{"number": issueNumber, "html_url": "https://github.com/" + repository + "/issues/" + number, "state": issueState})
}

// taskIssuesFixture is a task of acme/widgets whose brief names issue 1 in
// its Task and issue 99 only in its Constraints, whose branch's gate run was
// started to close issue 9, and whose pull request closes issue 2 and is part
// of acme/gadgets#3.
func taskIssuesFixture(t *testing.T) (home.Home, state.TaskMeta, pipeline.Reader) {
	t.Helper()
	base := t.TempDir()
	h := home.Home{Root: base, Data: filepath.Join(base, "data"), State: filepath.Join(base, "state")}
	checkout := filepath.Join(base, "widgets")
	worktree := filepath.Join(base, "worktrees", "g1")
	for _, dir := range []string{checkout, worktree, h.State} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	brief := filepath.Join(h.Data, "g1", "brief.md")
	writeFixtureFile(t, brief, "# Brief g1\n\n## Task\n\nFix https://github.com/acme/widgets/issues/1.\n\n## Constraints\n\n- Leave issue #99 alone.\n")
	if err := state.AppendStatus(h.State, "g1", "done: PR https://github.com/acme/widgets/pull/50"); err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", filepath.Join(base, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.Exec(`CREATE TABLE repos(id TEXT, working_path TEXT); CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, closing_issue_refs TEXT, created_at INTEGER); INSERT INTO repos VALUES ('repo', ?); INSERT INTO runs VALUES ('run', 'repo', 'fix/x', '9', 100);`, checkout)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	meta := state.TaskMeta{ID: "g1", Project: checkout, Worktree: worktree}
	return h, meta, pipeline.Reader{Root: base, Commands: gatedb.Runner{Next: taskIssuesGit{}}}
}

func TestOpenTaskIssuesNamesTheOpenIssuesOfTheBriefTheGateAndThePullRequest(t *testing.T) {
	// Arrange
	h, meta, gate := taskIssuesFixture(t)
	answers := taskIssuesGitHub{states: map[string]string{"acme/widgets#1": "open", "acme/widgets#2": "closed", "acme/widgets#9": "open", "acme/gadgets#3": "open", "acme/widgets#99": "open"}}
	github := tickets.GitHub{Commands: taskIssuesGit{}, API: githubtest.Client(answers)}

	// Act
	open, gone, err := openTaskIssues(h, github, gate)(context.Background(), meta)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme/widgets#1", "acme/widgets#9", "acme/gadgets#3"}; !slices.Equal(open, want) || len(gone) != 0 {
		t.Fatalf("open issues = %q, gone %q; want %q open and none gone", open, gone, want)
	}
}

// GitHub answers 404 for an issue that was deleted or never filed, in a
// repository it shows, so the issue is gone, never closed; a number that is a
// pull request is no issue at all.
func TestOpenTaskIssuesNamesAnIssueGitHubReportsGoneApart(t *testing.T) {
	// Arrange
	h, meta, gate := taskIssuesFixture(t)
	answers := taskIssuesGitHub{states: map[string]string{"acme/widgets#1": "open", "acme/widgets#2": "pull request", "acme/gadgets#3": "closed"}}
	github := tickets.GitHub{Commands: taskIssuesGit{}, API: githubtest.Client(answers)}

	// Act
	open, gone, err := openTaskIssues(h, github, gate)(context.Background(), meta)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(open, []string{"acme/widgets#1"}) || !slices.Equal(gone, []string{"acme/widgets#9"}) {
		t.Fatalf("open issues = %q, gone %q; want acme/widgets#1 open and acme/widgets#9 gone", open, gone)
	}
}

func TestOpenTaskIssuesFailsWhatItCannotRead(t *testing.T) {
	tests := []struct {
		name    string
		answers taskIssuesGitHub
		want    string
	}{
		{"a pull request GitHub does not show", taskIssuesGitHub{isPullGone: true}, "GitHub shows no pull request at https://github.com/acme/widgets/pull/50"},
		{"an issue of a repository GitHub does not show", taskIssuesGitHub{states: map[string]string{"acme/widgets#1": "open"}, isRepositoriesHidden: true}, "read acme/widgets#9"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, meta, gate := taskIssuesFixture(t)
			github := tickets.GitHub{Commands: taskIssuesGit{}, API: githubtest.Client(test.answers)}

			// Act
			_, _, err := openTaskIssues(h, github, gate)(context.Background(), meta)

			// Assert
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want one with %q", err, test.want)
			}
		})
	}
}
